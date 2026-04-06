# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```sh
make build          # build the integration-github-importer binary
go test -race ./... # run all tests
go mod tidy         # sync dependencies

# run a single test
go test -run TestListRepos_User ./...

# rebuild, repackage, and reinstall the plugin locally
make build
plakar pkg rm integration-github
plakar pkg create manifest.yaml
plakar pkg add ./integration-github_v0.0.1_darwin_arm64.ptar

# add a source and run a backup
plakar source add mygithub "github://owner[/repo]" token=<PAT>
plakar at /path/to/repo backup @mygithub
```

## Architecture

This is a **plakar importer plugin** for GitHub. Plakar v1.0.6 loads plugins as gRPC subprocess binaries. The plugin lifecycle is:

1. `plakar` reads `manifest.yaml` to discover which protocol (`github://`) maps to which binary (`integration-github-importer`)
2. plakar spawns the binary; the binary starts a gRPC server via `sdk.EntrypointImporter`
3. plakar calls `Init` → `Scan` → reads records from the channel → stores them

### Key interface (kloset v1.0.12 — `snapshot/importer`)

```go
type Importer interface {
    Origin(ctx) (string, error)
    Type(ctx)   (string, error)
    Root(ctx)   (string, error)
    Scan(ctx)   (<-chan *ScanResult, error)
    Close(ctx)  error
}
```

Records are emitted via `importer.NewScanRecord(pathname, target, fileInfo, xattrs, readerFn)`.

### File layout

| File               | Role                                                                                             |
| ------------------ | ------------------------------------------------------------------------------------------------ |
| `connector.go`     | `init()` — registers `"github"` protocol with `importer.Register`                                |
| `importer.go`      | `GitHubImporter` struct + `NewImporter` constructor + `Scan` + all `emit*` helpers               |
| `github.go`        | GitHub API client (`NewGitHubClient`), `IsOrg`, `ListRepos`, `ListIssues`, `paginateAll` generic |
| `importer/main.go` | Binary entrypoint — calls `sdk.EntrypointImporter`                                               |
| `manifest.yaml`    | Plugin metadata: name, version, `api_version: 1.0.0`, connector `protocols` list                 |

### Backup layout produced

```
{repo}/manifest.json       # json.Marshal of github.Repository
{repo}/git.tar.gz          # tarball from GitHub archive API (lazy fetch)
{repo}/issues/{n}.json     # one file per issue (PRs skipped)
```

### SDK / API version coupling

- plakar v1.0.6 requires `api_version: 1.0.0` in `manifest.yaml` (no `v` prefix)
- Uses `github.com/PlakarKorp/kloset v1.0.12` and `go-kloset-sdk v1.0.5`
- **Do not upgrade to the v1.1.x beta SDK** — the interface changed (`Import` → `Scan`, `connectors` → `snapshot/importer` package paths)

### Location parsing

`github://owner` → all repos for owner (user or org, auto-detected via API)  
`github://owner/repo` → single repo  
Owner/repo can also be passed via explicit `owner=` / `repo=` config keys.
