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

This is a **plakar importer plugin** for GitHub. Plakar v1.1 loads plugins as gRPC subprocess binaries. The plugin lifecycle is:

1. `plakar` reads `manifest.yaml` to discover which protocol (`github://`) maps to which binary (`integration-github-importer`)
2. plakar spawns the binary; the binary starts a gRPC server via `sdk.EntrypointImporter`
3. plakar calls `Init` → `Import` → reads records from the channel → stores them

### Key interface (kloset v1.1 — `connectors/importer`)

```go
type Importer interface {
    Origin() string
    Type() string
    Root() string
    Flags() location.Flags
    Ping(ctx) error
    Import(ctx, chan<- *connectors.Record, <-chan *connectors.Result) error
    Close(ctx) error
}
```

Records are emitted via `connectors.NewRecord(pathname, target, fileInfo, xattrs, readerFn)`. `Import` must close the records channel when done. The exporter mirrors this with `Export(ctx, <-chan *connectors.Record, chan<- *connectors.Result)` and must send one result per record, then close the results channel.

### File layout

| File               | Role                                                                                             |
| ------------------ | ------------------------------------------------------------------------------------------------ |
| `connector.go`     | `init()` — registers `"github"` protocol with `importer.Register`                                |
| `importer.go`      | `GitHubImporter` struct + `NewImporter` constructor + `Import` + all `emit*` helpers             |
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

- Uses `github.com/PlakarKorp/kloset v1.1.x` and `go-kloset-sdk v1.1.x`, which need plakar v1.1+
- Plugins built against kloset v1.0.x (`snapshot/importer`, `Scan`) do not load in plakar v1.1, and the reverse is also true

### Location parsing

`github://owner` → all repos for owner (user or org, auto-detected via API)
`github://owner/repo` → single repo
Owner/repo can also be passed via explicit `owner=` / `repo=` config keys.
