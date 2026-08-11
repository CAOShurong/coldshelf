# Roadmap

ColdShelf is intentionally centered on locating offline media. Items are ordered by user value, not novelty.

## Near term

- Use the reproducible million-entry baseline to reduce ingestion allocation and
  indexing cost without weakening atomic snapshots or search correctness.
- Add optional bounded image thumbnails with an explicit storage budget.
- Improve automatic drive matching without treating a mutable path as hardware identity.
- Add package-manager manifests after binary releases stabilize.

## Shipped

- `v0.1.6`: preserve stable drive IDs and atomically merge complete snapshots
  from catalogs created on another computer, with dry-run, idempotency, and
  explicit name/hash trust policies.

## Later, only with a safe design

- Authenticated LAN/reverse-proxy mode.
- Encrypted catalogs with a documented recovery story.
- Team catalog bundles with conflict-aware metadata merge.
- Archive-content indexing for ZIP and tar files.

## Non-goals

- editing or organizing files on source media;
- replacing backups, checksums stored with backups, or restore tests;
- always-online NAS indexing;
- cloud accounts or hosted catalog storage;
- claiming quick fingerprints prove file equality.

Open an issue with a concrete workflow and sample scale before proposing a major feature. The project favors small, composable data formats over a broad media-management suite.
