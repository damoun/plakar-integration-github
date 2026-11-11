package integration_github_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	integration_github "github.com/damoun/plakar-integration-github"
	"github.com/google/go-github/v71/github"

	"github.com/PlakarKorp/kloset/connectors"
)

// newTestClient returns a GitHub client pointed at a test HTTP server.
func newTestClient(t *testing.T, mux *http.ServeMux) (*httptest.Server, *github.Client) {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client, err := github.NewClient(nil).WithAuthToken("test-token").WithEnterpriseURLs(srv.URL+"/", srv.URL+"/")
	if err != nil {
		t.Fatalf("setting enterprise URLs: %v", err)
	}
	return srv, client
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// importAll runs Import to completion and returns every emitted record.
func importAll(t *testing.T, imp *integration_github.GitHubImporter) []*connectors.Record {
	t.Helper()
	records := make(chan *connectors.Record)
	errc := make(chan error, 1)
	go func() { errc <- imp.Import(context.Background(), records, nil) }()

	var out []*connectors.Record
	for record := range records {
		out = append(out, record)
	}
	if err := <-errc; err != nil {
		t.Fatalf("Import: %v", err)
	}
	return out
}

func TestNewImporter_MissingToken(t *testing.T) {
	ctx := context.Background()
	_, err := integration_github.NewImporter(ctx, &connectors.Options{}, "github", map[string]string{
		"location": "github://testowner",
	})
	if err == nil {
		t.Fatal("expected error for missing token, got nil")
	}
}

func TestNewImporter_MissingOwner(t *testing.T) {
	ctx := context.Background()
	_, err := integration_github.NewImporter(ctx, &connectors.Options{}, "github", map[string]string{
		"token":    "ghp_test",
		"location": "github://",
	})
	if err == nil {
		t.Fatal("expected error for missing owner, got nil")
	}
}

func TestListRepos_User(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/users/alice/repos", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, []map[string]any{
			{"id": 1, "name": "repo-a", "full_name": "alice/repo-a"},
			{"id": 2, "name": "repo-b", "full_name": "alice/repo-b"},
		})
	})
	_, client := newTestClient(t, mux)

	repos, err := integration_github.ListRepos(context.Background(), client, "alice", false)
	if err != nil {
		t.Fatalf("listRepos: %v", err)
	}
	if len(repos) != 2 {
		t.Fatalf("expected 2 repos, got %d", len(repos))
	}
}

func TestListRepos_Org(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/orgs/myorg/repos", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, []map[string]any{
			{"id": 10, "name": "infra", "full_name": "myorg/infra"},
		})
	})
	_, client := newTestClient(t, mux)

	repos, err := integration_github.ListRepos(context.Background(), client, "myorg", true)
	if err != nil {
		t.Fatalf("listRepos org: %v", err)
	}
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(repos))
	}
}

func TestListIssues(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/alice/repo-a/issues", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, []map[string]any{
			{"id": 100, "number": 1, "title": "First issue", "state": "open"},
			{"id": 101, "number": 2, "title": "Second issue", "state": "closed"},
		})
	})
	_, client := newTestClient(t, mux)

	issues, err := integration_github.ListIssues(context.Background(), client, "alice", "repo-a")
	if err != nil {
		t.Fatalf("listIssues: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("expected 2 issues, got %d", len(issues))
	}
}

func TestParseLocation(t *testing.T) {
	cases := []struct {
		input     string
		wantOwner string
		wantRepo  string
	}{
		{"github://alice", "alice", ""},
		{"github://alice/myrepo", "alice", "myrepo"},
		{"alice", "alice", ""},
		{"alice/myrepo", "alice", "myrepo"},
	}
	for _, c := range cases {
		owner, repo := integration_github.ParseLocation(c.input)
		if owner != c.wantOwner || repo != c.wantRepo {
			t.Errorf("ParseLocation(%q) = (%q, %q), want (%q, %q)",
				c.input, owner, repo, c.wantOwner, c.wantRepo)
		}
	}
}

func TestIsOrg(t *testing.T) {
	cases := []struct {
		owner   string
		ghType  string
		wantOrg bool
	}{
		{"alice", "User", false},
		{"myorg", "Organization", true},
	}
	for _, c := range cases {
		t.Run(c.owner, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/api/v3/users/"+c.owner, func(w http.ResponseWriter, _ *http.Request) {
				writeJSON(w, map[string]any{"login": c.owner, "type": c.ghType})
			})
			_, client := newTestClient(t, mux)

			org, err := integration_github.IsOrg(context.Background(), client, c.owner)
			if err != nil {
				t.Fatalf("IsOrg: %v", err)
			}
			if org != c.wantOrg {
				t.Errorf("IsOrg() = %v, want %v", org, c.wantOrg)
			}
		})
	}
}

func TestImporterMethods(t *testing.T) {
	ctx := context.Background()
	imp, err := integration_github.NewImporter(ctx, &connectors.Options{}, "github", map[string]string{
		"token":    "ghp_test",
		"location": "github://alice",
	})
	if err != nil {
		t.Fatalf("NewImporter: %v", err)
	}

	if v := imp.Origin(); v != "github.com" {
		t.Errorf("Origin() = %q", v)
	}
	if v := imp.Type(); v != "github" {
		t.Errorf("Type() = %q", v)
	}
	if v := imp.Root(); v != "/" {
		t.Errorf("Root() = %q", v)
	}
	if err := imp.Close(ctx); err != nil {
		t.Errorf("Close() = %v", err)
	}
}

func TestImport_SingleRepo(t *testing.T) {
	mux := http.NewServeMux()
	srv, client := newTestClient(t, mux)

	mux.HandleFunc("/api/v3/repos/alice/repo-a", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"id": 1, "name": "repo-a", "full_name": "alice/repo-a"})
	})
	mux.HandleFunc("/api/v3/repos/alice/repo-a/issues", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, []map[string]any{
			{"id": 1, "number": 1, "title": "Bug", "state": "open"},
		})
	})
	mux.HandleFunc("/archive/repo-a.tar.gz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/v3/repos/alice/repo-a/tarball", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv.URL+"/archive/repo-a.tar.gz", http.StatusFound)
	})

	imp := integration_github.NewGitHubImporter(client, "alice", "repo-a")

	var paths []string
	for _, record := range importAll(t, imp) {
		if record.Err != nil {
			t.Errorf("import error: %v", record.Err)
			continue
		}
		if record.Reader != nil {
			func() {
				defer record.Reader.Close() //nolint:errcheck
				_, _ = io.ReadAll(record.Reader)
			}()
		}
		paths = append(paths, record.Pathname)
	}

	want := map[string]bool{
		"/repo-a/manifest.json": false,
		"/repo-a/git.tar.gz":    false,
		"/repo-a/issues/1.json": false,
	}
	for _, p := range paths {
		want[p] = true
	}
	for path, found := range want {
		if !found {
			t.Errorf("missing path in scan results: %s", path)
		}
	}
}

func TestFlags(t *testing.T) {
	imp := integration_github.NewGitHubImporter(nil, "alice", "")
	if imp.Flags() != 0 {
		t.Errorf("Flags() = %v, want 0", imp.Flags())
	}
}

func TestImport_AllRepos(t *testing.T) {
	mux := http.NewServeMux()

	// IsOrg
	mux.HandleFunc("/api/v3/users/alice", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"login": "alice", "type": "User"})
	})
	// ListRepos
	mux.HandleFunc("/api/v3/users/alice/repos", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, []map[string]any{
			{"id": 1, "name": "repo-a", "full_name": "alice/repo-a"},
		})
	})
	// Issues (no issues)
	mux.HandleFunc("/api/v3/repos/alice/repo-a/issues", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, []map[string]any{})
	})

	srv, client := newTestClient(t, mux)

	mux.HandleFunc("/archive/repo-a.tar.gz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/v3/repos/alice/repo-a/tarball", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv.URL+"/archive/repo-a.tar.gz", http.StatusFound)
	})

	imp := integration_github.NewGitHubImporter(client, "alice", "")

	var paths []string
	for _, record := range importAll(t, imp) {
		if record.Err != nil {
			t.Errorf("import error: %v", record.Err)
			continue
		}
		paths = append(paths, record.Pathname)
	}

	want := map[string]bool{
		"/repo-a/manifest.json": false,
		"/repo-a/git.tar.gz":    false,
	}
	for _, p := range paths {
		want[p] = true
	}
	for path, found := range want {
		if !found {
			t.Errorf("missing path in scan results: %s", path)
		}
	}
}

// TestIntegration_ListRepos runs against the real GitHub API.
// Skipped unless GITHUB_TOKEN and GITHUB_OWNER are set.
func TestIntegration_ListRepos(t *testing.T) {
	token := os.Getenv("GITHUB_TOKEN")
	owner := os.Getenv("GITHUB_OWNER")
	if token == "" || owner == "" {
		t.Skip("GITHUB_TOKEN and GITHUB_OWNER not set")
	}

	ctx := context.Background()
	client := integration_github.NewGitHubClient(ctx, token)

	org, err := integration_github.IsOrg(ctx, client, owner)
	if err != nil {
		t.Fatalf("IsOrg: %v", err)
	}

	repos, err := integration_github.ListRepos(ctx, client, owner, org)
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	fmt.Printf("found %d repos for %s (org=%v)\n", len(repos), owner, org)
	if len(repos) == 0 {
		t.Errorf("expected at least 1 repo for %s", owner)
	}
}
