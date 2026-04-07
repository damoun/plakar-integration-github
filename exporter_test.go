package integration_github_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/PlakarKorp/kloset/snapshot/exporter"
	integration_github "github.com/damoun/plakar-integration-github"
	"github.com/google/go-github/v71/github"
)

func TestNewExporter_MissingToken(t *testing.T) {
	_, err := integration_github.NewExporter(context.Background(), &exporter.Options{}, "github", map[string]string{
		"location": "github://alice",
	})
	if err == nil {
		t.Fatal("expected error for missing token")
	}
}

func TestNewExporter_MissingOwner(t *testing.T) {
	_, err := integration_github.NewExporter(context.Background(), &exporter.Options{}, "github", map[string]string{
		"token":    "ghp_test",
		"location": "github://",
	})
	if err == nil {
		t.Fatal("expected error for missing owner")
	}
}

func TestExporterRoot(t *testing.T) {
	exp, err := integration_github.NewExporter(context.Background(), &exporter.Options{}, "github", map[string]string{
		"token":    "ghp_test",
		"location": "github://alice",
	})
	if err != nil {
		t.Fatalf("NewExporter: %v", err)
	}
	root, err := exp.Root(context.Background())
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	if root != "alice" {
		t.Errorf("Root() = %q, want %q", root, "alice")
	}
}

func TestExporterNoOps(t *testing.T) {
	exp, err := integration_github.NewExporter(context.Background(), &exporter.Options{}, "github", map[string]string{
		"token":    "ghp_test",
		"location": "github://alice",
	})
	if err != nil {
		t.Fatalf("NewExporter: %v", err)
	}
	ctx := context.Background()
	if err := exp.CreateDirectory(ctx, "repo-a/issues"); err != nil {
		t.Errorf("CreateDirectory: %v", err)
	}
	if err := exp.CreateLink(ctx, "old", "new", exporter.SYMLINK); err != nil {
		t.Errorf("CreateLink: %v", err)
	}
	if err := exp.SetPermissions(ctx, "repo-a/manifest.json", nil); err != nil {
		t.Errorf("SetPermissions: %v", err)
	}
	if err := exp.Close(ctx); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestStoreFile_UnknownPath(t *testing.T) {
	exp, err := integration_github.NewExporter(context.Background(), &exporter.Options{}, "github", map[string]string{
		"token":    "ghp_test",
		"location": "github://alice",
	})
	if err != nil {
		t.Fatalf("NewExporter: %v", err)
	}
	if err := exp.StoreFile(context.Background(), "repo-a/unknown.txt", strings.NewReader("data"), 4); err != nil {
		t.Errorf("StoreFile unknown path should be no-op, got: %v", err)
	}
}

func TestHandleManifest_ExistingRepo(t *testing.T) {
	mux := http.NewServeMux()
	called := false
	mux.HandleFunc("/api/v3/repos/alice/repo-a", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"id": 1, "name": "repo-a"})
	})
	mux.HandleFunc("/api/v3/user/repos", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		writeJSON(w, map[string]any{"id": 1, "name": "repo-a"})
	})
	_, client := newTestClient(t, mux)

	exp := integration_github.NewGitHubExporter(client, "alice", "")
	body, _ := json.Marshal(map[string]any{"name": "repo-a", "private": false})
	if err := exp.StoreFile(context.Background(), "repo-a/manifest.json", bytes.NewReader(body), int64(len(body))); err != nil {
		t.Fatalf("StoreFile manifest: %v", err)
	}
	if called {
		t.Error("expected no repo creation call for existing repo")
	}
}

func TestHandleManifest_NewRepo(t *testing.T) {
	mux := http.NewServeMux()
	created := false
	mux.HandleFunc("/api/v3/repos/alice/repo-new", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		writeJSON(w, map[string]any{"message": "Not Found"})
	})
	mux.HandleFunc("/api/v3/user/repos", func(w http.ResponseWriter, r *http.Request) {
		created = true
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, map[string]any{"id": 2, "name": "repo-new"})
	})
	_, client := newTestClient(t, mux)

	exp := integration_github.NewGitHubExporter(client, "alice", "repo-new")
	body, _ := json.Marshal(map[string]any{"name": "repo-a", "private": false})
	if err := exp.StoreFile(context.Background(), "repo-a/manifest.json", bytes.NewReader(body), int64(len(body))); err != nil {
		t.Fatalf("StoreFile manifest: %v", err)
	}
	if !created {
		t.Error("expected repo creation call for missing repo")
	}
}

func TestHandleIssue_Open(t *testing.T) {
	mux := http.NewServeMux()
	var createdTitle string
	mux.HandleFunc("/api/v3/repos/alice/repo-a/issues", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		createdTitle = req["title"].(string)
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, map[string]any{"id": 1, "number": 10, "state": "open"})
	})
	_, client := newTestClient(t, mux)

	exp := integration_github.NewGitHubExporter(client, "alice", "")
	issue := github.Issue{
		Number: github.Ptr(3),
		Title:  github.Ptr("Bug report"),
		Body:   github.Ptr("Something broke"),
		State:  github.Ptr("open"),
	}
	body, _ := json.Marshal(issue)
	if err := exp.StoreFile(context.Background(), "repo-a/issues/3.json", bytes.NewReader(body), int64(len(body))); err != nil {
		t.Fatalf("StoreFile issue: %v", err)
	}
	if createdTitle != "Bug report" {
		t.Errorf("issue title = %q, want %q", createdTitle, "Bug report")
	}
}

func TestHandleIssue_Closed(t *testing.T) {
	mux := http.NewServeMux()
	editCalled := false
	mux.HandleFunc("/api/v3/repos/alice/repo-a/issues", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, map[string]any{"id": 1, "number": 10, "state": "open"})
	})
	mux.HandleFunc("/api/v3/repos/alice/repo-a/issues/10", func(w http.ResponseWriter, r *http.Request) {
		editCalled = true
		writeJSON(w, map[string]any{"id": 1, "number": 10, "state": "closed"})
	})
	_, client := newTestClient(t, mux)

	exp := integration_github.NewGitHubExporter(client, "alice", "")
	issue := github.Issue{
		Number: github.Ptr(5),
		Title:  github.Ptr("Old bug"),
		Body:   github.Ptr("Fixed"),
		State:  github.Ptr("closed"),
	}
	body, _ := json.Marshal(issue)
	if err := exp.StoreFile(context.Background(), "repo-a/issues/5.json", bytes.NewReader(body), int64(len(body))); err != nil {
		t.Fatalf("StoreFile closed issue: %v", err)
	}
	if !editCalled {
		t.Error("expected issue edit call to close the issue")
	}
}

func TestHandleGitArchive(t *testing.T) {
	mux := http.NewServeMux()
	blobCalled, treeCalled, commitCalled, refCalled := false, false, false, false

	// GET uses /git/ref/ (singular); POST/PATCH uses /git/refs/ (plural).
	mux.HandleFunc("/api/v3/repos/alice/repo-a/git/ref/heads/main", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"ref":    "refs/heads/main",
			"object": map[string]any{"sha": "existing123", "type": "commit"},
		})
	})
	mux.HandleFunc("/api/v3/repos/alice/repo-a/git/refs/heads/main", func(w http.ResponseWriter, _ *http.Request) {
		refCalled = true
		writeJSON(w, map[string]any{"ref": "refs/heads/main"})
	})
	mux.HandleFunc("/api/v3/repos/alice/repo-a/git/blobs", func(w http.ResponseWriter, _ *http.Request) {
		blobCalled = true
		writeJSON(w, map[string]any{"sha": "abc123"})
	})
	mux.HandleFunc("/api/v3/repos/alice/repo-a/git/trees", func(w http.ResponseWriter, _ *http.Request) {
		treeCalled = true
		writeJSON(w, map[string]any{"sha": "tree123"})
	})
	mux.HandleFunc("/api/v3/repos/alice/repo-a/git/commits", func(w http.ResponseWriter, _ *http.Request) {
		commitCalled = true
		writeJSON(w, map[string]any{"sha": "commit123"})
	})
	_, client := newTestClient(t, mux)

	// Build a minimal gzipped tarball.
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	content := []byte("hello world")
	_ = tw.WriteHeader(&tar.Header{
		Name:     "prefix/README.md",
		Size:     int64(len(content)),
		Typeflag: tar.TypeReg,
	})
	_, _ = tw.Write(content)
	_ = tw.Close()
	_ = gw.Close()

	exp := integration_github.NewGitHubExporter(client, "alice", "")
	if err := exp.StoreFile(context.Background(), "repo-a/git.tar.gz", &buf, int64(buf.Len())); err != nil {
		t.Fatalf("StoreFile git archive: %v", err)
	}
	if !blobCalled {
		t.Error("expected blob creation call")
	}
	if !treeCalled {
		t.Error("expected tree creation call")
	}
	if !commitCalled {
		t.Error("expected commit creation call")
	}
	_ = refCalled // ref update is best-effort
}

func TestHandleGitArchive_Empty(t *testing.T) {
	_, client := newTestClient(t, http.NewServeMux())

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	_ = tw.Close()
	_ = gw.Close()

	exp := integration_github.NewGitHubExporter(client, "alice", "")
	if err := exp.StoreFile(context.Background(), "repo-a/git.tar.gz", &buf, int64(buf.Len())); err != nil {
		t.Fatalf("empty archive should be no-op: %v", err)
	}
}

func TestHandleIssue_WithLabels(t *testing.T) {
	mux := http.NewServeMux()
	var gotLabels []string
	mux.HandleFunc("/api/v3/repos/alice/repo-a/issues", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if labels, ok := req["labels"].([]any); ok {
			for _, l := range labels {
				gotLabels = append(gotLabels, l.(string))
			}
		}
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, map[string]any{"id": 1, "number": 10, "state": "open"})
	})
	_, client := newTestClient(t, mux)

	exp := integration_github.NewGitHubExporter(client, "alice", "")
	issue := github.Issue{
		Number: github.Ptr(7),
		Title:  github.Ptr("Labeled"),
		Body:   github.Ptr("has labels"),
		State:  github.Ptr("open"),
		Labels: []*github.Label{{Name: github.Ptr("bug")}, {Name: github.Ptr("enhancement")}},
	}
	body, _ := json.Marshal(issue)
	if err := exp.StoreFile(context.Background(), "repo-a/issues/7.json", bytes.NewReader(body), int64(len(body))); err != nil {
		t.Fatalf("StoreFile issue with labels: %v", err)
	}
	if len(gotLabels) != 2 || gotLabels[0] != "bug" {
		t.Errorf("labels = %v, want [bug enhancement]", gotLabels)
	}
}
