# plakar-github

A [plakar](https://github.com/PlakarKorp/plakar) importer for GitHub — backs up repositories (git history and files) and issues for both personal accounts and organizations.

## Features

- Backs up full git repository content as a tarball (via GitHub archive API)
- Backs up issues as individual JSON records (pull requests excluded)
- Supports personal accounts and organizations (auto-detected)
- Backs up a single repo or all repos under an owner
- Authenticates via GitHub PAT or bot token

## Requirements

- plakar v1.0.6+

## Installation

Build from source:

```sh
git clone https://github.com/damoun/plakar-integration-github
cd plakar-github
make build
plakar pkg create manifest.yaml
plakar pkg add ./integration-github_v0.0.1_darwin_arm64.ptar
```

## Configuration

Add a named source with your GitHub token:

```sh
plakar source add mygithub "github://owner[/repo]" token=ghp_xxx
```

| Key     | Required | Description                                               |
| ------- | -------- | --------------------------------------------------------- |
| `token` | yes      | GitHub Personal Access Token or bot token                 |
| `owner` | no       | GitHub username or org (extracted from location if unset) |
| `repo`  | no       | Specific repo name (defaults to all repos for the owner)  |

## Usage

Backup all repos for a user or org:

```sh
plakar source add mygithub "github://damoun" token=ghp_xxx
plakar at /path/to/repo backup @mygithub
```

Backup a single repository:

```sh
plakar source add myrepo "github://damoun/plakar-integration-github" token=ghp_xxx
plakar at /path/to/repo backup @myrepo
```

## Backup Layout

Each repository produces three record types:

```
{repo}/manifest.json        # Repository metadata (JSON)
{repo}/git.tar.gz           # Full git archive (tarball from GitHub API)
{repo}/issues/{id}.json     # One file per issue (pull requests skipped)
```

## License

ISC — see [LICENSE](LICENSE).
