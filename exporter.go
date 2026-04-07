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

	"github.com/PlakarKorp/kloset/objects"
	kloset_exporter "github.com/PlakarKorp/kloset/snapshot/exporter"
	"github.com/google/go-github/v71/github"
)

// GitHubExporter implements the plakar exporter interface for GitHub.
type GitHubExporter struct {
	client       *github.Client
	owner        string
	repoOverride string
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
		client:       NewGitHubClient(ctx, token),
		owner:        owner,
		repoOverride: repo,
	}, nil
}

// NewGitHubExporter creates an exporter directly from an existing client (used in tests).
func NewGitHubExporter(client *github.Client, owner, repoOverride string) *GitHubExporter {
	return &GitHubExporter{client: client, owner: owner, repoOverride: repoOverride}
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

	// Create blobs and build tree entries.
	var entries []*github.TreeEntry
	for _, f := range files {
		encoded := string(f.content)
		blob, _, err := e.client.Git.CreateBlob(ctx, e.owner, repoName, &github.Blob{
			Content:  github.Ptr(encoded),
			Encoding: github.Ptr("utf-8"),
		})
		if err != nil {
			// Fall back to base64 encoding for binary files.
			blob, _, err = e.client.Git.CreateBlob(ctx, e.owner, repoName, &github.Blob{
				Content:  github.Ptr(encoded),
				Encoding: github.Ptr("base64"),
			})
			if err != nil {
				return fmt.Errorf("github: creating blob for %s: %w", f.path, err)
			}
		}
		entries = append(entries, &github.TreeEntry{
			Path: github.Ptr(f.path),
			Mode: github.Ptr("100644"),
			Type: github.Ptr("blob"),
			SHA:  blob.SHA,
		})
	}

	tree, _, err := e.client.Git.CreateTree(ctx, e.owner, repoName, "", entries)
	if err != nil {
		return fmt.Errorf("github: creating tree for %s: %w", repoName, err)
	}

	// Get current HEAD commit if the repo has one.
	var parentSHAs []string
	ref, _, err := e.client.Git.GetRef(ctx, e.owner, repoName, "refs/heads/main")
	if err == nil && ref.Object != nil {
		parentSHAs = []string{ref.Object.GetSHA()}
	}

	var parents []*github.Commit
	for _, sha := range parentSHAs {
		s := sha
		parents = append(parents, &github.Commit{SHA: &s})
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
	_, _, err = e.client.Git.UpdateRef(ctx, e.owner, repoName, &github.Reference{
		Ref:    &refName,
		Object: &github.GitObject{SHA: commit.SHA},
	}, true)
	if err != nil {
		return fmt.Errorf("github: updating ref for %s: %w", repoName, err)
	}
	return nil
}

func (e *GitHubExporter) handleIssue(ctx context.Context, repoName string, r io.Reader) error {
	var issue github.Issue
	if err := json.NewDecoder(r).Decode(&issue); err != nil {
		return fmt.Errorf("github: decoding issue for %s: %w", repoName, err)
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
