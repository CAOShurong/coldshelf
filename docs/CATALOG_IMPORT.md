# Import another ColdShelf catalog

Use `import-catalog` to combine catalogs made on different computers without
rescanning drives that are currently offline:

```console
coldshelf import-catalog laptop-coldshelf.db --dry-run
coldshelf import-catalog laptop-coldshelf.db
```

The target is the normal ColdShelf database. Use `--db` to choose a different
target:

```console
coldshelf import-catalog laptop-coldshelf.db --db combined.db --json
```

## What is merged

- Stable drive IDs and every complete snapshot are preserved.
- Snapshot IDs are remapped because those numbers are local to one database.
- Incomplete or failed snapshots are skipped.
- Import receipts combine the stable drive ID, source snapshot ID, metadata, and
  ordered entries, so importing a copied catalog again does not create
  duplicates while two legitimate identical scans remain distinct history.
- When the same stable drive ID already exists, its target-side name, location,
  notes, and tags stay unchanged; new source snapshots are added to it.

The source database must be closed and checkpointed. ColdShelf rejects `-wal`
or `-shm` sidecars, opens the main file with SQLite's immutable read-only mode,
and holds one read transaction. It checks the schema version, SQLite integrity,
foreign keys, drive IDs, paths, fingerprints, metadata, timestamps, and declared
file totals. The target schema migration and merge are one transaction: a
validation or write failure cannot leave a v1 target partly upgraded.

## Preview and name conflicts

`--dry-run` copies a closed target through SQLite's online-backup API into a
private shared in-memory database, performs the same migration and merge logic,
then rolls it back. It does not create the requested target when one is absent,
write the existing target or its directory, or place a catalog copy in a system
temporary directory. Its counts describe what a real import would do.

A new drive whose *name* is already used by a different drive ID is rejected by
default. Review the IDs and rerun with `--rename-conflicts` only when both are
genuinely different drives. ColdShelf then creates a deterministic name such as
`Archive (imported a1b2c3)` and reports the rename.

## Full-hash trust boundary

ColdShelf cannot prove imported `sha256:` values because it does not reread the
offline source files during a catalog merge. It therefore removes those values
by default, while preserving quick fingerprints as non-proof change candidates.
Use `--trust-full-hashes` only when you trust how the source catalog and its full
hashes were produced:

```console
coldshelf import-catalog trusted-catalog.db --trust-full-hashes
```

This option preserves a claim; it does not independently verify file content.
The receipt records the policy. Rerunning the same import with the other policy
updates hashes in the already imported snapshot atomically instead of creating
a duplicate: default mode removes full-hash claims, while
`--trust-full-hashes` restores them from the unchanged source catalog.

## Backups and live sources

The import does not modify the source file or create sidecars beside it. Close
ColdShelf before using a source catalog and wait for its WAL checkpoint; a
remaining `-wal` or `-shm` file is rejected with an explanation. To snapshot a
live catalog, use SQLite's `.backup` command or backup API instead of copying the
main, WAL, and shared-memory files one by one. Keep a backup of the target before
upgrading any early `v0.1.x` release.

Catalog merge is a common requirement in established offline catalog tools;
for example, DiskCatalogMaker documents merging catalog files and importing a
catalog from another Mac. ColdShelf keeps that workflow cross-platform and
scriptable while following SQLite's documented read-only URI and transaction
semantics.

- [DiskCatalogMaker catalog merge FAQ](https://diskcatalogmaker.com/faq/catalog/merge.html)
- [SQLite URI filename `mode=ro`](https://sqlite.org/uri.html)
- [SQLite transaction behavior](https://sqlite.org/lang_transaction.html)
