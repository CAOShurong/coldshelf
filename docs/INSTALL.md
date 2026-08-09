# Install and verify ColdShelf

ColdShelf publishes archives for Windows, macOS, and Linux on the GitHub release page. Each archive contains the executable, README, project license, and dependency notices.

## 1. Download the correct archive

| System | Archive |
|---|---|
| Windows on Intel/AMD | `windows_amd64.zip` |
| Windows on ARM | `windows_arm64.zip` |
| macOS on Intel | `darwin_amd64.tar.gz` |
| macOS on Apple silicon | `darwin_arm64.tar.gz` |
| Linux on Intel/AMD | `linux_amd64.tar.gz` |
| Linux on ARM64 | `linux_arm64.tar.gz` |

## 2. Verify SHA-256

Download `SHA256SUMS` from the same release.

Windows PowerShell:

```powershell
(Get-FileHash .\coldshelf_0.1.0_windows_amd64.zip -Algorithm SHA256).Hash
Get-Content .\SHA256SUMS
```

macOS:

```console
shasum -a 256 coldshelf_0.1.0_darwin_arm64.tar.gz
grep darwin_arm64 SHA256SUMS
```

Linux:

```console
sha256sum --check SHA256SUMS
```

GitHub also publishes a signed build-provenance attestation for the archives:

```console
gh attestation verify coldshelf_0.1.0_linux_amd64.tar.gz --repo CAOShurong/coldshelf
```

## 3. Extract and run

Windows:

```powershell
Expand-Archive .\coldshelf_0.1.0_windows_amd64.zip
.\coldshelf_0.1.0_windows_amd64\coldshelf.exe version
```

macOS or Linux:

```console
tar -xzf coldshelf_0.1.0_linux_amd64.tar.gz
./coldshelf version
```

Release binaries are not currently Authenticode-signed or Apple-notarized. Windows SmartScreen or macOS Gatekeeper may therefore show an unknown-publisher warning. Verify the checksum and GitHub attestation before choosing to run the binary. Do not bypass an OS warning for an archive obtained from another site.

## Update or uninstall

The executable is self-contained. Replace it to update; delete it to uninstall. Catalog data is separate and is never removed automatically. Run `coldshelf serve` to see its exact path, then back up or remove that database deliberately if it is no longer needed.
