# Changelog

All notable changes are documented here. ColdShelf follows semantic versioning after the initial public release.

## [Unreleased]

## [0.1.6] - 2026-08-11

### Added

- Add atomic `import-catalog` merges that preserve stable drive IDs and copy all
  complete snapshots from another ColdShelf database.
- Add dry-run and JSON output, source-snapshot identity receipts, deterministic
  `--rename-conflicts`, reversible imported-hash policy, source-schema/integrity
  validation, and migration from a real version-1 schema fixture.

### Security

- Require closed, checkpointed imported catalogs and open them in immutable
  read-only mode without creating WAL sidecars. Validate paths, fingerprints,
  metadata, timestamps, and overflow-safe declared counts, and roll back the
  target schema migration and merge together on any error.
- Remove imported full SHA-256 claims by default because the source files were
  not reread; `--trust-full-hashes` is an explicit opt-in.
- Run dry-runs against an in-memory SQLite online backup so neither a missing nor
  existing target, its directory, or the system temporary directory is changed.

## [0.1.5] - 2026-08-11

### Added

- Add an opt-in, fixed-seed one-million-entry catalog benchmark that verifies
  exact-name, path-term, and no-match search counts before reporting ingestion,
  search, storage, and allocation metrics.
- Run a 10,000-entry scale smoke benchmark in CI while keeping the normal test
  path fast, and document a reproducible one-million-entry Windows/NVMe baseline
  with explicit limitations.

## [0.1.4] - 2026-08-11

### Fixed

- Release binaries now record the tagged commit time instead of a fixed date,
  while remaining reproducible for the same source commit.
- Changelog comparison links now include every published patch release.

## [0.1.3] - 2026-08-11

### Fixed

- Explicit `serve` and `demo --serve` commands now run without launching a
  browser unless `--open` is supplied. Running `coldshelf` without a subcommand
  still opens the interface for interactive use.

## [0.1.2] - 2026-08-11

### Added

- Global search results now show the full catalog-relative path and provide an
  accessible one-click copy action with non-modal success or failure feedback.

### Security

- The local HTTP scan endpoint now rejects empty, relative, and NUL-containing
  paths, so browser-submitted scans always name an explicit absolute root.
- The threat model now makes the intentional path boundary explicit: users may
  catalog any mounted directory their OS account can read, and source access
  remains read-only.

## [0.1.1] - 2026-08-09

### Fixed

- Release SBOMs now inventory the Go modules and standard library inside the
  packaged Linux binary instead of describing only the archive directory.
- Release automation now fails closed if the dependency inventory is missing
  ColdShelf's key runtime modules.

## [0.1.0] - 2026-08-09

### Added

- Cross-platform, single-binary CLI and embedded local web interface.
- Read-only filesystem scanning with exclusions and `none`, `quick`, and `full` fingerprint modes.
- SQLite WAL catalog with atomic snapshot commits and FTS5 search.
- Offline tree browsing, library metrics, file-type distribution, and snapshot diffs.
- Exact duplicate detection restricted to complete SHA-256 fingerprints.
- Stable drive IDs, physical-location metadata, and printable QR SVG labels.
- Everything EFU import plus JSON and CSV export.
- Loopback enforcement, Host/Origin validation, CSP, and documented threat model.
- Windows, macOS, and Linux CI; CodeQL; deterministic archives; checksums; and release SBOM.

[Unreleased]: https://github.com/CAOShurong/coldshelf/compare/v0.1.6...HEAD
[0.1.6]: https://github.com/CAOShurong/coldshelf/compare/v0.1.5...v0.1.6
[0.1.5]: https://github.com/CAOShurong/coldshelf/compare/v0.1.4...v0.1.5
[0.1.4]: https://github.com/CAOShurong/coldshelf/compare/v0.1.3...v0.1.4
[0.1.3]: https://github.com/CAOShurong/coldshelf/compare/v0.1.2...v0.1.3
[0.1.2]: https://github.com/CAOShurong/coldshelf/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/CAOShurong/coldshelf/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/CAOShurong/coldshelf/releases/tag/v0.1.0
