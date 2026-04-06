package plakar_github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/PlakarKorp/kloset/location"
	"github.com/PlakarKorp/kloset/objects"
	"github.com/PlakarKorp/kloset/snapshot/importer"
	"github.com/google/go-github/v71/github"
)

// GitHubImporter implements the plakar importer interface for GitHub.
type GitHubImporter struct {
	client *github.Client
	owner  string
	repo   string
}

// NewGitHubImporter creates a GitHubImporter directly (used in tests).
func NewGitHubImporter(client *github.Client, owner, repo string) *GitHubImporter {
	return &GitHubImporter{client: client, owner: owner, repo: repo}
}

// NewImporter is the constructor called by the plakar SDK.
func NewImporter(ctx context.Context, _ *importer.Options, _ string, config map[string]string) (importer.Importer, error) {
	token := config["token"]
	if token == "" {
		return nil, fmt.Errorf("github: missing required config key: token")
	}

	owner, repo := ParseLocation(config["location"])
	if v := config["owner"]; v != "" {
		owner = v
	}
	if v := config["repo"]; v != "" {
		repo = v
	}
	if owner == "" {
		return nil, fmt.Errorf("github: missing owner: use github://owner[/repo] location")
	}

	return &GitHubImporter{
		client: NewGitHubClient(ctx, token),
		owner:  owner,
		repo:   repo,
	}, nil
}

func (g *GitHubImporter) Origin(_ context.Context) (string, error) { return "github.com", nil }
func (g *GitHubImporter) Type(_ context.Context) (string, error)   { return "github", nil }
func (g *GitHubImporter) Root(_ context.Context) (string, error)   { return g.owner, nil }
func (g *GitHubImporter) Close(_ context.Context) error            { return nil }

// Scan returns a channel of ScanResults covering all repos, their manifests, git archives, and issues.
func (g *GitHubImporter) Scan(ctx context.Context) (<-chan *importer.ScanResult, error) {
	repos, err := g.resolveRepos(ctx)
	if err != nil {
		return nil, err
	}

	ch := make(chan *importer.ScanResult)
	go func() {
		defer close(ch)
		for _, repo := range repos {
			if err := g.scanRepo(ctx, repo, ch); err != nil {
				ch <- importer.NewScanError(repo.GetName(), err)
				return
			}
		}
	}()
	return ch, nil
}

func (g *GitHubImporter) resolveRepos(ctx context.Context) ([]*github.Repository, error) {
	if g.repo != "" {
		repo, _, err := g.client.Repositories.Get(ctx, g.owner, g.repo)
		if err != nil {
			return nil, fmt.Errorf("github: fetching repo %s/%s: %w", g.owner, g.repo, err)
		}
		return []*github.Repository{repo}, nil
	}

	org, err := IsOrg(ctx, g.client, g.owner)
	if err != nil {
		return nil, fmt.Errorf("github: resolving owner type for %q: %w", g.owner, err)
	}

	repos, err := ListRepos(ctx, g.client, g.owner, org)
	if err != nil {
		return nil, fmt.Errorf("github: listing repos for %s: %w", g.owner, err)
	}
	return repos, nil
}

func (g *GitHubImporter) scanRepo(ctx context.Context, repo *github.Repository, ch chan<- *importer.ScanResult) error {
	repoName := repo.GetName()

	if err := g.emitJSON(repoName+"/manifest.json", "manifest.json", repo, time.Now(), ch); err != nil {
		return err
	}
	if err := g.emitGitArchive(ctx, repoName, ch); err != nil {
		return err
	}
	return g.emitIssues(ctx, repoName, ch)
}

func (g *GitHubImporter) emitJSON(pathname, name string, obj any, modTime time.Time, ch chan<- *importer.ScanResult) error {
	data, err := json.Marshal(obj)
	if err != nil {
		return fmt.Errorf("github: marshaling %s: %w", pathname, err)
	}
	fi := objects.FileInfo{
		Lname:    name,
		Lsize:    int64(len(data)),
		Lmode:    0o444,
		LmodTime: modTime,
	}
	ch <- importer.NewScanRecord(pathname, "", fi, nil, func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	})
	return nil
}

func (g *GitHubImporter) emitGitArchive(ctx context.Context, repoName string, ch chan<- *importer.ScanResult) error {
	archiveURL, _, err := g.client.Repositories.GetArchiveLink(ctx, g.owner, repoName, github.Tarball, &github.RepositoryContentGetOptions{}, 5)
	if err != nil {
		return fmt.Errorf("github: getting archive link for %s: %w", repoName, err)
	}

	urlStr := archiveURL.String()
	client := g.client
	fi := objects.FileInfo{
		Lname:    "git.tar.gz",
		Lmode:    0o444,
		LmodTime: time.Now(),
	}
	ch <- importer.NewScanRecord(repoName+"/git.tar.gz", "", fi, nil, func() (io.ReadCloser, error) {
		rc, err := client.Client().Get(urlStr)
		if err != nil {
			return nil, err
		}
		return rc.Body, nil
	})
	return nil
}

func (g *GitHubImporter) emitIssues(ctx context.Context, repoName string, ch chan<- *importer.ScanResult) error {
	issues, err := ListIssues(ctx, g.client, g.owner, repoName)
	if err != nil {
		return fmt.Errorf("github: listing issues for %s: %w", repoName, err)
	}

	for _, issue := range issues {
		// GitHub API returns PRs alongside issues; skip them.
		if issue.IsPullRequest() {
			continue
		}
		pathname := fmt.Sprintf("%s/issues/%d.json", repoName, issue.GetNumber())
		name := fmt.Sprintf("%d.json", issue.GetNumber())
		if err := g.emitJSON(pathname, name, issue, issue.GetUpdatedAt().Time, ch); err != nil {
			return err
		}
	}
	return nil
}

// ParseLocation extracts owner and optional repo from a location string like
// "github://owner/repo" or "github://owner".
func ParseLocation(loc string) (owner, repo string) {
	if idx := strings.Index(loc, "://"); idx >= 0 {
		loc = loc[idx+3:]
	}
	parts := strings.SplitN(loc, "/", 2)
	owner = parts[0]
	if len(parts) == 2 {
		repo = parts[1]
	}
	return
}

// Flags returns the location flags for this importer (0 = remote API).
func (g *GitHubImporter) Flags() location.Flags { return 0 }
