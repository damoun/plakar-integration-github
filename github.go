package integration_github

import (
	"context"

	"github.com/google/go-github/v71/github"
)

// NewGitHubClient returns an authenticated GitHub client.
func NewGitHubClient(_ context.Context, token string) *github.Client {
	return github.NewClient(nil).WithAuthToken(token)
}

// IsOrg returns true if the owner is a GitHub organization.
func IsOrg(ctx context.Context, client *github.Client, owner string) (bool, error) {
	user, _, err := client.Users.Get(ctx, owner)
	if err != nil {
		return false, err
	}
	return user.GetType() == "Organization", nil
}

// ListRepos returns all repositories for the given owner (user or org).
func ListRepos(ctx context.Context, client *github.Client, owner string, org bool) ([]*github.Repository, error) {
	return paginateAll(func(page int) ([]*github.Repository, *github.Response, error) {
		opts := github.ListOptions{Page: page, PerPage: 100}
		if org {
			return client.Repositories.ListByOrg(ctx, owner, &github.RepositoryListByOrgOptions{ListOptions: opts})
		}
		return client.Repositories.ListByUser(ctx, owner, &github.RepositoryListByUserOptions{ListOptions: opts})
	})
}

// ListIssues returns all open and closed issues for the given repo.
func ListIssues(ctx context.Context, client *github.Client, owner, repo string) ([]*github.Issue, error) {
	return paginateAll(func(page int) ([]*github.Issue, *github.Response, error) {
		opts := &github.IssueListByRepoOptions{
			State:       "all",
			ListOptions: github.ListOptions{Page: page, PerPage: 100},
		}
		return client.Issues.ListByRepo(ctx, owner, repo, opts)
	})
}

func paginateAll[T any](fn func(page int) ([]T, *github.Response, error)) ([]T, error) {
	var all []T
	for page := 1; ; {
		items, resp, err := fn(page)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if resp.NextPage == 0 {
			break
		}
		page = resp.NextPage
	}
	return all, nil
}
