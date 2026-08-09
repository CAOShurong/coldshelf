## What changed

<!-- Describe the user-visible outcome. -->

## Why

<!-- Link the issue or explain the concrete workflow. -->

## Safety and compatibility

- [ ] Source-media scanning remains read-only.
- [ ] Failed work cannot replace the latest complete snapshot.
- [ ] Privacy/network behavior is unchanged or documented in the threat model.
- [ ] Schema behavior is unchanged or includes a migration and compatibility test.

## Verification

- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] `node --check web/static/app.js`
- [ ] Visible changes were checked in a real browser.

<!-- Add exact commands, operating systems, screenshots, and benchmark context. -->
