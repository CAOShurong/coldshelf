# Performance notes

ColdShelf streams entries through a prepared SQLite statement and does not retain the full inventory in memory. The dominant cost depends on the selected mode:

- `none`: directory enumeration, metadata reads, SQLite inserts, and FTS indexing;
- `quick`: the above plus at most 128 KiB read per regular file;
- `full`: the above plus every byte of every regular file.

WAL mode lets the browser keep reading the previous complete snapshot during a rescan. The new snapshot becomes visible in one atomic commit.

Performance depends heavily on filesystem, drive latency, antivirus scanning, path depth, and the ratio of small files to large files. Published benchmark numbers must identify those variables; a single files-per-second headline is not portable.

For a reproducible local smoke benchmark:

```console
go test -bench=. -benchmem ./internal/scanner ./internal/catalog
```

For real media, time all three modes against a copied, non-sensitive fixture and report the OS, filesystem, connection type, entry count, byte count, ColdShelf version, and whether real-time antivirus was enabled. Do not publish the resulting database if its names are private.
