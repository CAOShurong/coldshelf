# Install and verify ColdShelf

ColdShelf publishes archives for Windows, macOS, and Linux on the GitHub release page. Each archive contains the executable, README, project license, and dependency notices.

## 1. Download the correct archive

| System | Archive |
|---|---|
| Windows on Intel/AMD | `coldshelf_<version>_windows_amd64.zip` |
| Windows on ARM | `coldshelf_<version>_windows_arm64.zip` |
| macOS on Intel | `coldshelf_<version>_darwin_amd64.tar.gz` |
| macOS on Apple silicon | `coldshelf_<version>_darwin_arm64.tar.gz` |
| Linux on Intel/AMD | `coldshelf_<version>_linux_amd64.tar.gz` |
| Linux on ARM64 | `coldshelf_<version>_linux_arm64.tar.gz` |

## 2. Verify SHA-256

Download `SHA256SUMS` from the same release.

Run the commands from a directory containing `SHA256SUMS` and exactly one
ColdShelf archive for your operating system and architecture. The wildcard
keeps these instructions valid across releases; the manifest comparison still
binds the downloaded filename to its exact expected digest.

Windows PowerShell:

```powershell
$archive = @(Get-ChildItem -File .\coldshelf_*_windows_amd64.zip)
if ($archive.Count -ne 1) { throw "Expected exactly one Windows AMD64 archive" }
$manifestLine = @(Get-Content .\SHA256SUMS | Where-Object {
    $parts = $_ -split '\s+', 2
    $parts.Count -eq 2 -and $parts[1] -eq $archive[0].Name
})
if ($manifestLine.Count -ne 1) { throw "Archive is missing or duplicated in SHA256SUMS" }
$expected = ($manifestLine[0] -split '\s+', 2)[0].ToLowerInvariant()
$actual = (Get-FileHash -LiteralPath $archive[0].FullName -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actual -ne $expected) { throw "SHA-256 mismatch for $($archive[0].Name)" }
"OK  $($archive[0].Name)"
```

macOS on Apple silicon (use `darwin_amd64` on Intel):

```console
grep 'coldshelf_.*_darwin_arm64.tar.gz$' SHA256SUMS | shasum -a 256 -c -
```

Linux on Intel/AMD (use `linux_arm64` on ARM64):

```console
grep 'coldshelf_.*_linux_amd64.tar.gz$' SHA256SUMS | sha256sum --check -
```

GitHub also publishes a signed build-provenance attestation for the archives:

```console
gh attestation verify coldshelf_*_linux_amd64.tar.gz --repo CAOShurong/coldshelf
```

## 3. Extract and run

Windows:

```powershell
$archive = @(Get-ChildItem -File .\coldshelf_*_windows_amd64.zip)
if ($archive.Count -ne 1) { throw "Expected exactly one Windows AMD64 archive" }
Expand-Archive -LiteralPath $archive[0].FullName -DestinationPath .\coldshelf
.\coldshelf\coldshelf.exe version
```

macOS or Linux:

```console
tar -xzf coldshelf_*_linux_amd64.tar.gz
./coldshelf version
```

Release binaries are not currently Authenticode-signed or Apple-notarized. Windows SmartScreen or macOS Gatekeeper may therefore show an unknown-publisher warning. Verify the checksum and GitHub attestation before choosing to run the binary. Do not bypass an OS warning for an archive obtained from another site.

## Update or uninstall

The executable is self-contained. Replace it to update; delete it to uninstall. Catalog data is separate and is never removed automatically. Run `coldshelf serve` to see its exact path, then back up or remove that database deliberately if it is no longer needed.
