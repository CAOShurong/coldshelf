# Import an Everything File List

[Everything](https://www.voidtools.com/) can export an Everything File List (`.efu`) containing file metadata. ColdShelf can ingest that list without rereading the drive.

```console
coldshelf import-efu archive-01.efu \
  --name "Archive 01" \
  --location "Blue case · shelf B" \
  --strip-prefix "E:\Archive"
```

The importer understands the standard `Filename`, `Size`, `Date Modified`, and `Attributes` columns. It accepts UTF-8 with an optional byte-order mark, Windows FILETIME timestamps, RFC 3339 timestamps, and common localized CSV date forms.

`--strip-prefix` removes the mounted portion from every path. For example, `E:\Archive\Photos\01.jpg` becomes `Photos/01.jpg`, keeping the catalog portable when the drive later receives a different letter.

EFU does not contain file content hashes. Imported entries therefore cannot appear as exact duplicates until the physical drive is rescanned with `--hash full`.

Before sharing an EFU sample in an issue, replace private names and paths. File lists are often sensitive even without the files themselves.
