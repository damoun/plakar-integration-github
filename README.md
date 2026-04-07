# plakar-integration-github

A [plakar](https://github.com/PlakarKorp/plakar) importer and exporter for GitHub — backs up and restores repositories (git content and files) and issues for both personal accounts and organizations.

## Features

- Backs up full git repository content as a tarball (via GitHub archive API)
- Backs up issues as individual JSON records (pull requests excluded)
- Supports personal accounts and organizations (auto-detected)
- Backs up a single repo or all repos under an owner
- Restores repositories to GitHub: creates the repo if missing, pushes git content, recreates issues
- Skips duplicate issues on restore (use `force=true` to override)
- Authenticates via GitHub PAT or fine-grained PAT

## Requirements

- plakar v1.0.6+

## Installation

Build from source:

```sh
git clone https://github.com/damoun/plakar-integration-github
cd plakar-integration-github
make build
plakar pkg create manifest.yaml
plakar pkg add ./integration-github_v0.0.1_darwin_arm64.ptar
```

## Token Permissions

### Classic PAT

Scope: `repo` (full repository access).

### Fine-grained PAT

| Permission    | Backup | Restore |
| ------------- | ------ | ------- |
| **Contents**  | read   | write   |
| **Issues**    | read   | write   |
| **Metadata**  | read   | read    |
| **Workflows** | —      | write   |

The `Workflows` permission is required to restore `.github/workflows/` files. Without it, workflow files are skipped and all other content is still restored.

## Configuration

### Backup (importer)

```sh
plakar source add mygithub "github://owner[/repo]" token=ghp_xxx
```

| Key     | Required | Description                                               |
| ------- | -------- | --------------------------------------------------------- |
| `token` | yes      | GitHub PAT                                                |
| `owner` | no       | GitHub username or org (extracted from location if unset) |
| `repo`  | no       | Specific repo name (defaults to all repos for the owner)  |

### Restore (exporter)

```sh
plakar destination add mydest "github://owner[/repo]" token=ghp_xxx
```

| Key     | Required | Description                                                |
| ------- | -------- | ---------------------------------------------------------- |
| `token` | yes      | GitHub PAT with write access                               |
| `owner` | no       | Destination owner (extracted from location if unset)       |
| `repo`  | no       | Override destination repo name (single-repo restores only) |
| `force` | no       | Set `true` to recreate issues even if they already exist   |

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

Restore a snapshot to GitHub:

```sh
plakar destination add mydest "github://damoun/new-repo" token=ghp_xxx
plakar at /path/to/repo restore -latest -to @mydest
```

Restore and rename the repo:

```sh
plakar destination add mydest "github://damoun" token=ghp_xxx repo=new-name
plakar at /path/to/repo restore -latest -to @mydest
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
