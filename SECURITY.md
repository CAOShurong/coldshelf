# Security policy

## Supported versions

Until `v1.0.0`, only the latest published release receives security fixes.

## Reporting

Please use GitHub's **Report a vulnerability** form under the repository Security tab. If private reporting is unavailable, open a minimal issue asking for a private contact channel; do not disclose exploit details there.

Include:

- ColdShelf version and operating system;
- the affected command or API route;
- a minimal reproduction with private names removed;
- the expected impact;
- whether the issue can modify source media, expose catalog data, or escape the loopback boundary.

Do not upload a real catalog, EFU file, drive image, or private path list.

You should receive an acknowledgement within seven days. A fix and coordinated disclosure timeline will be proposed after reproduction and severity assessment.

## Scope

High-priority reports include source-media writes, unauthenticated non-loopback access, cross-origin state changes, catalog corruption that replaces a known-good snapshot, and release supply-chain compromise.

The limitations documented in [the threat model](docs/THREAT_MODEL.md)—including same-user local access and the absence of at-rest encryption—are not vulnerabilities by themselves.
