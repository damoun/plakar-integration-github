package integration_github

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
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

	// repoCreated guards one-time lazy repo creation per repo name.
	repoCreated   map[string]bool
	repoCreatedMu sync.Mutex

	// existingIssues caches issue titles per repo to skip duplicates when force=false.
	existingIssues   map[string]map[string]bool
	existingIssuesMu sync.Mutex
}

// NewExporter is the constructor called by the plakar SDK.
func NewExporter(ctx context.Context, _ *kloset_exporter.Options, _ string, config map[string]string) (kloset_exporter.Exporter, error) {
	token, owner, repo, err := parseConfig(config)
	if err != nil {
		return nil, err
	}
	return &GitHubExporter{
		client:         NewGitHubClient(ctx, token),
		owner:          owner,
		repoOverride:   repo,
		force:          config["force"] == "true",
		repoCreated:    make(map[string]bool),
		existingIssues: make(map[string]map[string]bool),
	}, nil
}

// NewGitHubExporter creates an exporter directly from an existing client (used in tests).
func NewGitHubExporter(client *github.Client, owner, repoOverride string) *GitHubExporter {
	return &GitHubExporter{
		client:         client,
		owner:          owner,
		repoOverride:   repoOverride,
		repoCreated:    make(map[string]bool),
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
	case len(parts) == 3 && parts[1] == "issues":
		return e.handleIssue(ctx, repoName, fp)
	}
	return nil
}

// ensureRepo creates the repository if it hasn't been created yet.
// It is safe to call concurrently; only the first call creates the repo.
func (e *GitHubExporter) ensureRepo(ctx context.Context, repoName string, req *github.Repository) error {
	e.repoCreatedMu.Lock()
	if e.repoCreated[repoName] {
		e.repoCreatedMu.Unlock()
		return nil
	}
	e.repoCreatedMu.Unlock()

	_, resp, err := e.client.Repositories.Get(ctx, e.owner, repoName)
	if err == nil {
		e.repoCreatedMu.Lock()
		e.repoCreated[repoName] = true
		e.repoCreatedMu.Unlock()
		return nil
	}
	if resp == nil || resp.StatusCode != 404 {
		if resp != nil && resp.StatusCode == 401 {
			return fmt.Errorf("github: authentication failed checking repo %s/%s — check token validity: %w", e.owner, repoName, err)
		}
		if resp != nil && resp.StatusCode == 403 {
			return fmt.Errorf("github: permission denied checking repo %s/%s — ensure token has 'repo' scope: %w", e.owner, repoName, err)
		}
		return fmt.Errorf("github: checking repo %s/%s: %w", e.owner, repoName, err)
	}

	// Double-check under lock to avoid concurrent creation.
	e.repoCreatedMu.Lock()
	defer e.repoCreatedMu.Unlock()
	if e.repoCreated[repoName] {
		return nil
	}

	createReq := &github.Repository{Name: github.Ptr(repoName)}
	if req != nil {
		createReq.Description = req.Description
		createReq.Private = req.Private
	}
	_, createResp, err := e.client.Repositories.Create(ctx, e.owner, createReq)
	if err != nil {
		if createResp != nil && createResp.StatusCode == 422 {
			// Already created by a concurrent goroutine — that's fine.
			e.repoCreated[repoName] = true
			return nil
		}
		if createResp != nil && createResp.StatusCode == 401 {
			return fmt.Errorf("github: authentication failed creating repo %s — check token validity: %w", repoName, err)
		}
		if createResp != nil && createResp.StatusCode == 403 {
			return fmt.Errorf("github: permission denied creating repo %s — ensure token has 'repo' scope: %w", repoName, err)
		}
		return fmt.Errorf("github: creating repo %s: %w", repoName, err)
	}
	e.repoCreated[repoName] = true
	return nil
}

func (e *GitHubExporter) handleManifest(ctx context.Context, repoName string, r io.Reader) error {
	var repo github.Repository
	if err := json.NewDecoder(r).Decode(&repo); err != nil {
		return fmt.Errorf("github: decoding manifest for %s: %w", repoName, err)
	}
	return e.ensureRepo(ctx, repoName, &repo)
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

	if err := e.ensureRepo(ctx, repoName, nil); err != nil {
		return err
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
		var skipped int
		for _, entry := range entries {
			if strings.HasPrefix(entry.GetPath(), ".github/workflows/") {
				skipped++
			} else {
				filtered = append(filtered, entry)
			}
		}
		if skipped > 0 {
			log.Printf("github: token lacks 'workflows' permission — skipping %d workflow file(s) for %s/%s; grant the scope to restore them", skipped, e.owner, repoName)
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

func (e *GitHubExporter) loadExistingIssues(ctx context.Context, repoName string) (map[string]bool, error) {
	issues, err := ListIssues(ctx, e.client, e.owner, repoName)
	if err != nil {
		// 404 means the repo doesn't exist yet; treat as no existing issues.
		var ghErr *github.ErrorResponse
		if errors.As(err, &ghErr) && ghErr.Response != nil && ghErr.Response.StatusCode == 404 {
			return make(map[string]bool), nil
		}
		return nil, fmt.Errorf("github: listing issues for %s: %w", repoName, err)
	}
	titles := make(map[string]bool, len(issues))
	for _, i := range issues {
		titles[i.GetTitle()] = true
	}
	return titles, nil
}

func (e *GitHubExporter) handleIssue(ctx context.Context, repoName string, r io.Reader) error {
	var issue github.Issue
	if err := json.NewDecoder(r).Decode(&issue); err != nil {
		return fmt.Errorf("github: decoding issue for %s: %w", repoName, err)
	}

	if !e.force {
		e.existingIssuesMu.Lock()
		_, loaded := e.existingIssues[repoName]
		e.existingIssuesMu.Unlock()

		if !loaded {
			titles, err := e.loadExistingIssues(ctx, repoName)
			if err != nil {
				return err
			}
			e.existingIssuesMu.Lock()
			if _, still := e.existingIssues[repoName]; !still {
				e.existingIssues[repoName] = titles
			}
			e.existingIssuesMu.Unlock()
		}

		e.existingIssuesMu.Lock()
		skip := e.existingIssues[repoName][issue.GetTitle()]
		e.existingIssuesMu.Unlock()
		if skip {
			return nil
		}
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

	if err := e.ensureRepo(ctx, repoName, nil); err != nil {
		return err
	}

	created, createResp, err := e.client.Issues.Create(ctx, e.owner, repoName, req)
	if err != nil {
		if createResp != nil && createResp.StatusCode == 401 {
			return fmt.Errorf("github: authentication failed creating issue in %s/%s — check token validity: %w", e.owner, repoName, err)
		}
		if createResp != nil && createResp.StatusCode == 403 {
			return fmt.Errorf("github: permission denied creating issue in %s/%s — ensure token has 'issues:write' scope: %w", e.owner, repoName, err)
		}
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
