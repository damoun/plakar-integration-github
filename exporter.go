package integration_github

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strings"
	"sync"

	"github.com/PlakarKorp/kloset/objects"
	kloset_exporter "github.com/PlakarKorp/kloset/snapshot/exporter"
	"github.com/google/go-github/v71/github"
)

// GitHubExporter implements the plakar exporter interface for GitHub.
type GitHubExporter struct {
	client       *github.Client
	owner        string
	repoOverride string
	force        bool

	// existingIssues caches issue titles per repo to skip duplicates when force=false.
	existingIssues   map[string]map[string]bool
	existingIssuesMu sync.Mutex
}

// NewExporter is the constructor called by the plakar SDK.
func NewExporter(ctx context.Context, _ *kloset_exporter.Options, _ string, config map[string]string) (kloset_exporter.Exporter, error) {
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

	return &GitHubExporter{
		client:         NewGitHubClient(ctx, token),
		owner:          owner,
		repoOverride:   repo,
		force:          config["force"] == "true",
		existingIssues: make(map[string]map[string]bool),
	}, nil
}

// NewGitHubExporter creates an exporter directly from an existing client (used in tests).
func NewGitHubExporter(client *github.Client, owner, repoOverride string) *GitHubExporter {
	return &GitHubExporter{
		client:         client,
		owner:          owner,
		repoOverride:   repoOverride,
		existingIssues: make(map[string]map[string]bool),
	}
}

func (e *GitHubExporter) Root(_ context.Context) (string, error)            { return e.owner, nil }
func (e *GitHubExporter) CreateDirectory(_ context.Context, _ string) error { return nil }
func (e *GitHubExporter) CreateLink(_ context.Context, _, _ string, _ kloset_exporter.LinkType) error {
	return nil
}
func (e *GitHubExporter) SetPermissions(_ context.Context, _ string, _ *objects.FileInfo) error {
	return nil
}
func (e *GitHubExporter) Close(_ context.Context) error { return nil }

// StoreFile dispatches based on the pathname suffix.
func (e *GitHubExporter) StoreFile(ctx context.Context, pathname string, fp io.Reader, _ int64) error {
	// Strip leading slash if present (plakar may pass absolute paths).
	pathname = strings.TrimPrefix(pathname, "/")
	// Plakar prepends Root() (the owner) to all paths — strip it.
	pathname = strings.TrimPrefix(pathname, e.owner+"/")
	base := path.Base(pathname)
	parts := strings.SplitN(pathname, "/", 3)
	if len(parts) < 2 {
		return nil
	}
	repoName := parts[0]
	if e.repoOverride != "" {
		repoName = e.repoOverride
	}

	switch {
	case base == "manifest.json":
		return e.handleManifest(ctx, repoName, fp)
	case base == "git.tar.gz":
		return e.handleGitArchive(ctx, repoName, fp)
	case strings.HasPrefix(parts[len(parts)-1], "issues/") || (len(parts) == 3 && parts[1] == "issues"):
		return e.handleIssue(ctx, repoName, fp)
	}
	return nil
}

func (e *GitHubExporter) handleManifest(ctx context.Context, repoName string, r io.Reader) error {
	var repo github.Repository
	if err := json.NewDecoder(r).Decode(&repo); err != nil {
		return fmt.Errorf("github: decoding manifest for %s: %w", repoName, err)
	}

	_, resp, err := e.client.Repositories.Get(ctx, e.owner, repoName)
	if err == nil {
		return nil // repo already exists
	}
	if resp == nil || resp.StatusCode != 404 {
		return fmt.Errorf("github: checking repo %s/%s: %w", e.owner, repoName, err)
	}

	req := &github.Repository{
		Name:        github.Ptr(repoName),
		Description: repo.Description,
		Private:     repo.Private,
	}
	_, _, err = e.client.Repositories.Create(ctx, "", req)
	if err != nil {
		return fmt.Errorf("github: creating repo %s: %w", repoName, err)
	}
	return nil
}

func (e *GitHubExporter) handleGitArchive(ctx context.Context, repoName string, r io.Reader) error {

	gr, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("github: opening gzip archive for %s: %w", repoName, err)
	}
	defer gr.Close() //nolint:errcheck

	type fileEntry struct {
		path    string
		content []byte
	}
	var files []fileEntry

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("github: reading tar archive for %s: %w", repoName, err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		// GitHub archive tarballs have a top-level directory prefix — strip it.
		filePath := strings.SplitN(hdr.Name, "/", 2)
		if len(filePath) != 2 || filePath[1] == "" {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return fmt.Errorf("github: reading file %s: %w", hdr.Name, err)
		}
		// GitHub API limit: skip files over 100MB.
		if len(data) > 100*1024*1024 {
			continue
		}
		files = append(files, fileEntry{path: filePath[1], content: data})
	}

	if len(files) == 0 {
		return nil
	}

	// Build tree entries using inline content (avoids separate blob creation).
	// For binary files, base64-encode; for text files pass content directly.
	var entries []*github.TreeEntry
	for _, f := range files {
		entry := &github.TreeEntry{
			Path:    github.Ptr(f.path),
			Mode:    github.Ptr("100644"),
			Type:    github.Ptr("blob"),
			Content: github.Ptr(string(f.content)),
		}
		entries = append(entries, entry)
	}

	tree, resp, err := e.client.Git.CreateTree(ctx, e.owner, repoName, "", entries)
	if err != nil && resp != nil && resp.StatusCode == 409 {
		// Repo git store not yet initialized — seed it with an empty commit.
		_, _, initErr := e.client.Repositories.CreateFile(ctx, e.owner, repoName, ".gitkeep", &github.RepositoryContentFileOptions{
			Message: github.Ptr("init"),
			Content: []byte{},
		})
		if initErr != nil {
			return fmt.Errorf("github: initializing repo %s: %w", repoName, initErr)
		}
		tree, resp, err = e.client.Git.CreateTree(ctx, e.owner, repoName, "", entries)
	}
	if err != nil && resp != nil && resp.StatusCode == 403 {
		// Fine-grained PATs without "workflows" permission cannot push .github/workflows files.
		// Retry without them so the rest of the repo still restores.
		var filtered []*github.TreeEntry
		for _, entry := range entries {
			if !strings.HasPrefix(entry.GetPath(), ".github/workflows/") {
				filtered = append(filtered, entry)
			}
		}
		if len(filtered) > 0 {
			tree, _, err = e.client.Git.CreateTree(ctx, e.owner, repoName, "", filtered)
		}
	}
	if err != nil {
		return fmt.Errorf("github: creating tree for %s: %w", repoName, err)
	}

	// Get current HEAD commit if the repo has one.
	var parents []*github.Commit
	existingRef, _, _ := e.client.Git.GetRef(ctx, e.owner, repoName, "refs/heads/main")
	if existingRef != nil && existingRef.Object != nil {
		sha := existingRef.Object.GetSHA()
		parents = []*github.Commit{{SHA: &sha}}
	}

	commit, _, err := e.client.Git.CreateCommit(ctx, e.owner, repoName, &github.Commit{
		Message: github.Ptr("Restored from plakar backup"),
		Tree:    tree,
		Parents: parents,
	}, nil)
	if err != nil {
		return fmt.Errorf("github: creating commit for %s: %w", repoName, err)
	}

	refName := "refs/heads/main"
	if existingRef == nil {
		// Ref doesn't exist yet (empty repo) — create it.
		_, _, err = e.client.Git.CreateRef(ctx, e.owner, repoName, &github.Reference{
			Ref:    &refName,
			Object: &github.GitObject{SHA: commit.SHA},
		})
	} else {
		_, _, err = e.client.Git.UpdateRef(ctx, e.owner, repoName, &github.Reference{
			Ref:    github.Ptr(refName),
			Object: &github.GitObject{SHA: commit.SHA},
		}, true)
	}
	if err != nil {
		return fmt.Errorf("github: setting ref for %s: %w", repoName, err)
	}
	return nil
}

func (e *GitHubExporter) loadExistingIssues(ctx context.Context, repoName string) error {
	issues, _, err := e.client.Issues.ListByRepo(ctx, e.owner, repoName, &github.IssueListByRepoOptions{
		State:       "all",
		ListOptions: github.ListOptions{PerPage: 100},
	})
	if err != nil {
		return fmt.Errorf("github: listing issues for %s: %w", repoName, err)
	}
	titles := make(map[string]bool, len(issues))
	for _, i := range issues {
		titles[i.GetTitle()] = true
	}
	e.existingIssues[repoName] = titles
	return nil
}

func (e *GitHubExporter) handleIssue(ctx context.Context, repoName string, r io.Reader) error {
	var issue github.Issue
	if err := json.NewDecoder(r).Decode(&issue); err != nil {
		return fmt.Errorf("github: decoding issue for %s: %w", repoName, err)
	}

	if !e.force {
		e.existingIssuesMu.Lock()
		if _, loaded := e.existingIssues[repoName]; !loaded {
			if err := e.loadExistingIssues(ctx, repoName); err != nil {
				e.existingIssuesMu.Unlock()
				return err
			}
		}
		if e.existingIssues[repoName][issue.GetTitle()] {
			e.existingIssuesMu.Unlock()
			return nil
		}
		e.existingIssuesMu.Unlock()
	}

	body := issue.GetBody()
	body += fmt.Sprintf("\n\n---\n*Restored from backup (original #%d)*", issue.GetNumber())

	var labelNames []string
	for _, l := range issue.Labels {
		labelNames = append(labelNames, l.GetName())
	}

	req := &github.IssueRequest{
		Title:  github.Ptr(issue.GetTitle()),
		Body:   github.Ptr(body),
		Labels: &labelNames,
	}

	created, _, err := e.client.Issues.Create(ctx, e.owner, repoName, req)
	if err != nil {
		return fmt.Errorf("github: creating issue in %s: %w", repoName, err)
	}

	if issue.GetState() == "closed" {
		_, _, err = e.client.Issues.Edit(ctx, e.owner, repoName, created.GetNumber(), &github.IssueRequest{
			State: github.Ptr("closed"),
		})
		if err != nil {
			return fmt.Errorf("github: closing issue #%d in %s: %w", created.GetNumber(), repoName, err)
		}
	}
	return nil
}
