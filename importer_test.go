package plakar_github_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/PlakarKorp/kloset/connectors"
	plakar_github "github.com/damoun/plakar-github"
	"github.com/google/go-github/v71/github"
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

func TestNewImporter_MissingToken(t *testing.T) {
	ctx := context.Background()
	_, err := plakar_github.NewImporter(ctx, &connectors.Options{}, "github", map[string]string{
		"location": "github://testowner",
	})
	if err == nil {
		t.Fatal("expected error for missing token, got nil")
	}
}

func TestNewImporter_MissingOwner(t *testing.T) {
	ctx := context.Background()
	_, err := plakar_github.NewImporter(ctx, &connectors.Options{}, "github", map[string]string{
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

	repos, err := plakar_github.ListRepos(context.Background(), client, "alice", false)
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

	repos, err := plakar_github.ListRepos(context.Background(), client, "myorg", true)
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

	issues, err := plakar_github.ListIssues(context.Background(), client, "alice", "repo-a")
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
		owner, repo := plakar_github.ParseLocation(c.input)
		if owner != c.wantOwner || repo != c.wantRepo {
			t.Errorf("ParseLocation(%q) = (%q, %q), want (%q, %q)",
				c.input, owner, repo, c.wantOwner, c.wantRepo)
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
	client := plakar_github.NewGitHubClient(ctx, token)

	org, err := plakar_github.IsOrg(ctx, client, owner)
	if err != nil {
		t.Fatalf("IsOrg: %v", err)
	}

	repos, err := plakar_github.ListRepos(ctx, client, owner, org)
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	fmt.Printf("found %d repos for %s (org=%v)\n", len(repos), owner, org)
	if len(repos) == 0 {
		t.Errorf("expected at least 1 repo for %s", owner)
	}
}
