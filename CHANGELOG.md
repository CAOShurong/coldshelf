# Changelog

All notable changes are documented here. ColdShelf follows semantic versioning after the initial public release.

## [Unreleased]

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

[Unreleased]: https://github.com/CAOShurong/coldshelf/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/CAOShurong/coldshelf/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/CAOShurong/coldshelf/releases/tag/v0.1.0
