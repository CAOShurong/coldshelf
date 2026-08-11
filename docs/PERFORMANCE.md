# Performance notes

ColdShelf streams entries through a prepared SQLite statement and does not retain the full inventory in memory. The dominant cost depends on the selected mode:

- `none`: directory enumeration, metadata reads, SQLite inserts, and FTS indexing;
- `quick`: the above plus at most 128 KiB read per regular file;
- `full`: the above plus every byte of every regular file.

WAL mode lets the browser keep reading the previous complete snapshot during a rescan. The new snapshot becomes visible in one atomic commit.

Performance depends heavily on filesystem, drive latency, antivirus scanning, path depth, and the ratio of small files to large files. Published benchmark numbers must identify those variables; a single files-per-second headline is not portable.

For the fast scanner and search microbenchmarks:

```console
go test -run '^$' -bench 'Benchmark(ScanMetadata|FTSSearch)' -benchmem ./internal/scanner ./internal/catalog
```

For real media, time all three modes against a copied, non-sensitive fixture and report the OS, filesystem, connection type, entry count, byte count, ColdShelf version, and whether real-time antivirus was enabled. Do not publish the resulting database if its names are private.

## Deterministic scale benchmark

`BenchmarkCatalogScale` creates a catalog from code with a fixed seed. It writes
only to Go's temporary test directory and removes the database after a normal
benchmark run. No generated file tree, catalog, or million-row artifact belongs
in the repository.

The default fixture contains 1,000,000 files in synthetic archive paths. Before
reporting any timing, it asserts three search results:

- `coldprooftarget` finds exactly one deliberately named file;
- `projectlantern` finds exactly 37 files by a path term;
- `missingconstellation` finds nothing.

Run exactly one benchmark iteration. On macOS or Linux:

```console
COLDSHELF_BENCH_ENTRIES=1000000 go test -run '^$' -bench '^BenchmarkCatalogScale$' -benchtime=1x -count=1 -benchmem ./internal/catalog
```

In PowerShell:

```powershell
$env:COLDSHELF_BENCH_ENTRIES = "1000000"
go test -run '^$' -bench '^BenchmarkCatalogScale$' -benchtime=1x -count=1 -benchmem ./internal/catalog
```

The database lives on the volume selected by the operating system's temporary
directory. Set `TMPDIR` on macOS/Linux or `TEMP` and `TMP` on Windows when the
storage medium matters, and record whether that volume is an HDD, SATA SSD,
NVMe SSD, network mount, or something else. The normal `go test ./...` path does
not run this benchmark. CI runs 10,000 entries as a correctness smoke test.

### One observed baseline

This is an observation, not a promise or target. It was measured on 2026-08-11
against catalog code at `0b150c9`, with the candidate benchmark change applied.

| Variable | Observed value |
|---|---:|
| OS | Windows 11 10.0.26200, amd64 |
| Go | 1.25.12 |
| CPU | Intel Core Ultra 5 125H, 18 logical processors |
| Temporary volume | E:, WD PC SN560 1 TB NVMe SSD |
| Entries | 1,000,000 |
| Ingestion elapsed | 213.818 s |
| Ingestion throughput | 4,677 entries/s |
| Catalog + WAL + shared-memory bytes | 805,884,832 bytes |
| Unique-name search, 25 warm queries | 0.274 ms/query, 1 hit |
| Path-term search, 25 warm queries | 0.457 ms/query, 37 hits |
| No-match search, 25 warm queries | 0.122 ms/query, 0 hits |
| Timed ingestion allocations | 2,226,305,872 B and 51,415,110 allocations |

The allocation figures include deterministic fixture generation, `database/sql`,
and the pure-Go SQLite driver; they are not a claim about SQLite alone. Search
queries are warm-cache measurements after ingestion. The fixture uses short
ASCII names, one completed snapshot, no source filesystem walk, no antivirus or
USB latency, and no hashing. Real archives can be faster or slower, especially
with deeper paths, cold caches, permission errors, or millions of tiny source
files. The baseline exists so later changes can be compared on the same fixture,
not so one machine can stand in for everyone else's archive.
