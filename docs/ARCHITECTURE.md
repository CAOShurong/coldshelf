# Architecture

ColdShelf is a Go executable with three boundaries: a read-only filesystem scanner, a SQLite catalog, and an embedded browser application. No separate frontend build or runtime is required.

## Packages

| Package | Responsibility |
|---|---|
| `cmd/coldshelf` | CLI parsing, OS integration, demo generation |
| `internal/scanner` | filesystem walk, glob exclusions, quick/full fingerprints |
| `internal/catalog` | schema, migrations, snapshot writer, FTS search, diff/export queries |
| `internal/importer` | Everything EFU parsing |
| `internal/label` | deterministic printable SVG and QR payload |
| `internal/server` | loopback HTTP API, scan jobs, security headers, static delivery |
| `web/static` | dependency-free HTML, CSS, and JavaScript interface |

## Snapshot transaction

A scan first creates a `scanning` row in `snapshots`. Entries stream into a single SQLite transaction through a prepared statement; the scanner does not keep a drive's full file list in memory. The transaction updates both the completed snapshot and `drives.latest_snapshot_id`, then commits once.

Readers continue to see the previous complete snapshot while the new transaction is open because the database uses WAL mode. If scanning or insertion fails, ColdShelf rolls the entry transaction back and marks the snapshot failed. A failed snapshot never becomes the drive's latest view.

## Schema

- `drives` holds stable ColdShelf IDs, user-facing names, source paths, physical locations, notes, and tags.
- `snapshots` holds scan status, fingerprint mode, counts, size totals, timestamps, and read-error totals.
- `entries` holds relative paths, parent paths, file type, size, modification time, and optional fingerprint.
- `catalog_imports` records a source-snapshot identity, target snapshot, and full-hash trust policy so repeats are idempotent and policy changes update in place.
- `entries_fts` is an external-content FTS5 index maintained by triggers. Search joins only entries belonging to each drive's latest snapshot.

The schema version is stored in `metadata`. Every migration must be forward-only, transactional, and covered by a fixture created with the previous version before `v1.0.0`.

## Path rules

Catalog paths use `/` on every platform and are relative to the scanned root. The real mounted path remains in snapshot metadata. This means an offline Windows drive can still be browsed after a catalog is moved to macOS or Linux.

Symbolic links are recorded as links but never followed. Exclusions use slash-normalized glob patterns; `folder/**` excludes both the folder and its descendants.

## Fingerprint semantics

- `quick:<sha256>` hashes the byte length, first 64 KiB, and last 64 KiB. It is useful for likely-change detection but is not proof of equality.
- `sha256:<sha256>` reads the complete file. Only these values feed the exact-duplicate view.

Prefixing the stored digest makes it impossible for a quick fingerprint to be silently interpreted as a complete hash.

## Catalog merge transaction

`import-catalog` requires a closed, checkpointed source without WAL sidecars,
opens it in SQLite immutable read-only mode, and keeps one read transaction. It
validates the source schema, SQLite integrity, foreign keys, stable drive IDs,
relative paths, fingerprints, metadata, timestamps, and declared snapshot
counts before committing anything. The target schema migration, drives,
snapshots, entries, FTS rows, and import receipts are written in one target
transaction. Any error rolls back the whole merge.

Complete snapshots are copied in source order and their database-local snapshot
IDs are remapped. Drive IDs remain stable. A receipt identity includes the
source snapshot ID and content, preserving repeated identical scans while making
an identical copied-catalog import a no-op. The receipt also records whether
full hashes were trusted, so rerunning with a different policy updates that
snapshot in place. The target catalog's existing drive metadata wins when a
stable drive ID is already present.

Dry runs use SQLite's online-backup API to copy a closed target into a uniquely
named shared in-memory database. The same migration and merge code runs there;
no target or temporary catalog file is created.

## HTTP boundary

The server accepts only loopback listen addresses. Every request also passes Host and Origin checks. The browser interface uses same-origin JSON requests, and content is served from `go:embed` with a restrictive Content Security Policy.

Scan requests are asynchronous. The in-memory job registry exposes progress for the lifetime of the process; durable outcomes live in the snapshot table. Restarting during a job leaves no partially visible file tree.
