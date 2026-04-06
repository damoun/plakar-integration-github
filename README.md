# plakar-github

A [plakar](https://github.com/PlakarKorp/plakar) importer for GitHub — backs up repositories (git history, files), and issues for both personal accounts and organizations.

## Features

- Backs up git repository content (full archive)
- Backs up issues as JSON records
- Supports personal accounts and organizations (auto-detected)
- Supports single repo or all repos under an owner
- Authenticates via GitHub PAT or bot token

## Installation

```sh
plakar pkg add plakar-github
```

Or build from source:

```sh
git clone https://github.com/damoun/plakar-github
cd plakar-github
make build
```

## Configuration

| Key     | Required | Description                                      |
| ------- | -------- | ------------------------------------------------ |
| `token` | yes      | GitHub Personal Access Token or bot token        |
| `owner` | yes      | GitHub username or organization name             |
| `repo`  | no       | Specific repository name (defaults to all repos) |

## Usage

Backup all repos for a user or org:

```sh
plakar backup github://owner --config token=ghp_xxx
```

Backup a single repo:

```sh
plakar backup github://owner/repo --config token=ghp_xxx
```

## Backup Layout

Each repository produces:

```
{repo}/manifest.json      # Repository metadata
{repo}/git.tar            # Full git archive (bare clone)
{repo}/issues/{id}.json   # One file per issue
```

## License

ISC — see [LICENSE](LICENSE).
