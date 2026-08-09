# Contributing to ColdShelf

Thank you for helping make offline archives easier to navigate. Focused bug reports, platform-specific testing, accessible interface improvements, and import fixtures are especially useful.

## Before opening an issue

1. Search existing issues and the [roadmap](ROADMAP.md).
2. Confirm the behavior on the latest release or `main`.
3. Remove private file names, paths, volume labels, and catalog data from logs or samples.
4. Use the security process for anything that could expose a catalog or modify source media.

## Development setup

Install Go 1.25 or newer, then run:

```console
go test ./...
go vet ./...
go run ./cmd/coldshelf demo --serve
```

The frontend has no package-manager step. Edit `web/static`, rebuild, and reload. Static assets are embedded at compile time.

Before submitting a pull request:

```console
gofmt -w ./cmd ./internal ./web ./build
go test -race ./...   # Linux/macOS with a race-capable toolchain
go vet ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...
node --check web/static/app.js
git diff --check
```

Windows builds without a C compiler because the SQLite driver is pure Go. `go test -race` requires CGO and is therefore run by CI on Linux.

## Design rules

- Scanning must remain read-only. Any source-media mutation is out of scope.
- A partial or failed scan must never replace the latest complete snapshot.
- Quick fingerprints must never be labeled exact duplicates.
- The default HTTP boundary remains loopback-only until authenticated remote mode exists.
- New network behavior requires a threat-model update and must be opt-in.
- Schema changes require a forward migration and compatibility test.
- Browser features must work without a CDN, remote font, analytics script, or account.

## Pull requests

Keep each pull request centered on one problem. Explain the user workflow, failure cases, and checks you ran. Include screenshots for visible changes and benchmark context for performance claims.

By contributing, you agree that your contribution is licensed under the repository's MIT License.
