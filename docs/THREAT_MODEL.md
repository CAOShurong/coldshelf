# Threat model

ColdShelf catalogs file names and paths, which may reveal client names, personal topics, or research projects even though file contents are never copied. The catalog should therefore be treated as private data.

## Protected assets

- the contents and metadata of a mounted source drive;
- the local SQLite catalog and its exported copies;
- the user's browser session and machine;
- the integrity of release binaries.

## Defended boundaries

### Source media

The scanner uses read-only filesystem calls and optional reads for hashing. It has no source-file write, rename, move, chmod, or delete path. Symbolic links are not followed. A path supplied by the user must resolve to a directory.

### Local HTTP API

The server refuses non-loopback listen addresses. It rejects non-loopback Host headers and cross-origin requests, requires JSON for state-changing endpoints, limits JSON bodies to 1 MiB, and sets CSP, frame, referrer, and MIME-sniffing protections.

These controls reduce DNS-rebinding and malicious-web-page attacks, but they do not defend against another process already running as the same OS user. A same-user process can read the database directly.

### Catalog integrity

SQLite foreign keys, uniqueness constraints, enumerated status checks, and atomic snapshot commits prevent a partial scan from replacing the last complete snapshot. WAL and the database files should be backed up together, or JSON export should be used for a portable logical backup.

### Releases

CI tests Windows, macOS, and Linux. Tagged releases contain SHA-256 checksums and a Syft-generated SPDX JSON SBOM. GitHub's release page and workflow history are the distribution source of record.

## Explicit non-goals in v0.1

- defending against a malicious local administrator or compromised OS;
- encrypting the catalog at rest;
- safely exposing the UI on a LAN or the public internet;
- verifying that source media is malware-free;
- proving that a quick fingerprint identifies identical content;
- functioning as a backup or recovery system.

## Reporting a vulnerability

Follow [SECURITY.md](../SECURITY.md). Do not attach a real catalog, private path list, or source-media sample to a public issue.
