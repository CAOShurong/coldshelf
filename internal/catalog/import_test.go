package catalog_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CAOShurong/coldshelf/internal/catalog"
)

func TestImportCatalogIsAtomicIdempotentAndReadOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sourcePath := filepath.Join(t.TempDir(), "source.db")
	source, err := catalog.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	drive, err := source.CreateDrive(ctx, catalog.NewDrive{
		Name: "Blue Archive", SourcePath: "E:/", Location: "Shelf B", Tags: []string{"video"},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeSnapshot(t, source, drive.ID, []catalog.Entry{
		{Path: "Projects", Name: "Projects", Kind: "directory"},
		{Path: "Projects/first.mov", ParentPath: "Projects", Name: "first.mov", Extension: "mov", Kind: "file", Size: 10, Hash: "sha256:" + strings.Repeat("a", 64)},
	})
	failed, err := source.StartSnapshot(ctx, drive.ID, "E:/", "full")
	if err != nil {
		t.Fatal(err)
	}
	if err := failed.Fail(fmt.Errorf("fixture read failure")); err != nil {
		t.Fatal(err)
	}
	writeSnapshot(t, source, drive.ID, []catalog.Entry{
		{Path: "Projects", Name: "Projects", Kind: "directory"},
		{Path: "Projects/final.mov", ParentPath: "Projects", Name: "final.mov", Extension: "mov", Kind: "file", Size: 20, Hash: "sha256:" + strings.Repeat("b", 64)},
	})
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	assertNoSQLiteSidecars(t, sourcePath)
	sourceHashBefore := fileSHA256(t, sourcePath)

	target, err := catalog.Open(filepath.Join(t.TempDir(), "target.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	preview, err := target.ImportCatalog(ctx, sourcePath, catalog.ImportCatalogOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !preview.DryRun || preview.DrivesAdded != 1 || preview.SnapshotsImported != 2 || preview.IncompleteSnapshotsSkipped != 1 {
		t.Fatalf("unexpected preview: %#v", preview)
	}
	if drives, err := target.ListDrives(ctx); err != nil || len(drives) != 0 {
		t.Fatalf("dry run changed target: %#v, %v", drives, err)
	}

	result, err := target.ImportCatalog(ctx, sourcePath, catalog.ImportCatalogOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.DrivesAdded != 1 || result.SnapshotsImported != 2 || result.EntriesImported != 4 || result.FullHashesStripped != 2 || result.IncompleteSnapshotsSkipped != 1 {
		t.Fatalf("unexpected import result: %#v", result)
	}
	if got := fileSHA256(t, sourcePath); got != sourceHashBefore {
		t.Fatalf("source catalog changed during import: %s -> %s", sourceHashBefore, got)
	}
	assertNoSQLiteSidecars(t, sourcePath)
	drives, err := target.ListDrives(ctx)
	if err != nil || len(drives) != 1 || drives[0].ID != drive.ID || drives[0].Location != "Shelf B" {
		t.Fatalf("imported drive mismatch: %#v, %v", drives, err)
	}
	snapshots, err := target.ListSnapshots(ctx, drive.ID)
	if err != nil || len(snapshots) != 2 || snapshots[0].HashMode != "imported" {
		t.Fatalf("imported snapshots mismatch: %#v, %v", snapshots, err)
	}
	entries, err := target.ListEntries(ctx, snapshots[0].ID, "Projects", 20, 0)
	if err != nil || len(entries) != 1 || entries[0].Path != "Projects/final.mov" || entries[0].Hash != "" {
		t.Fatalf("latest imported entries mismatch: %#v, %v", entries, err)
	}
	hits, err := target.Search(ctx, "final", drive.ID, 10)
	if err != nil || len(hits) != 1 {
		t.Fatalf("imported snapshot is not searchable: %#v, %v", hits, err)
	}

	repeated, err := target.ImportCatalog(ctx, sourcePath, catalog.ImportCatalogOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if repeated.DrivesMerged != 1 || repeated.SnapshotsImported != 0 || repeated.SnapshotsSkipped != 2 {
		t.Fatalf("repeated import was not idempotent: %#v", repeated)
	}
	if snapshots, err := target.ListSnapshots(ctx, drive.ID); err != nil || len(snapshots) != 2 {
		t.Fatalf("repeated import duplicated snapshots: %#v, %v", snapshots, err)
	}
}

func TestImportCatalogNameConflictAndTrustedHashes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sourcePath := filepath.Join(t.TempDir(), "source.db")
	source, err := catalog.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	drive, err := source.CreateDrive(ctx, catalog.NewDrive{Name: "Archive"})
	if err != nil {
		t.Fatal(err)
	}
	trustedHash := "sha256:" + strings.Repeat("c", 64)
	writeSnapshot(t, source, drive.ID, []catalog.Entry{{Path: "copy.bin", Name: "copy.bin", Kind: "file", Size: 42, Hash: trustedHash}})
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}

	target, err := catalog.Open(filepath.Join(t.TempDir(), "target.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	local, err := target.CreateDrive(ctx, catalog.NewDrive{Name: "Archive"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.ImportCatalog(ctx, sourcePath, catalog.ImportCatalogOptions{}); err == nil || !strings.Contains(err.Error(), "--rename-conflicts") {
		t.Fatalf("name collision was not rejected clearly: %v", err)
	}
	if drives, err := target.ListDrives(ctx); err != nil || len(drives) != 1 || drives[0].ID != local.ID {
		t.Fatalf("failed import was not atomic: %#v, %v", drives, err)
	}

	result, err := target.ImportCatalog(ctx, sourcePath, catalog.ImportCatalogOptions{RenameConflicts: true, TrustFullHashes: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.RenamedDrives) != 1 || result.RenamedDrives[0].From != "Archive" || result.FullHashesStripped != 0 {
		t.Fatalf("unexpected renamed import: %#v", result)
	}
	imported, err := target.GetDrive(ctx, drive.ID)
	if err != nil || imported.Name == "Archive" || !strings.HasPrefix(imported.Name, "Archive (imported ") {
		t.Fatalf("imported drive was not renamed deterministically: %#v, %v", imported, err)
	}
	snapshots, err := target.ListSnapshots(ctx, drive.ID)
	if err != nil || len(snapshots) != 1 || snapshots[0].HashMode != "full" {
		t.Fatalf("trusted snapshot mismatch: %#v, %v", snapshots, err)
	}
	entries, err := target.ListEntries(ctx, snapshots[0].ID, "", 20, 0)
	if err != nil || len(entries) != 1 || entries[0].Hash != trustedHash {
		t.Fatalf("trusted full hash was not preserved: %#v, %v", entries, err)
	}

	stripped, err := target.ImportCatalog(ctx, sourcePath, catalog.ImportCatalogOptions{})
	if err != nil || stripped.SnapshotsUpdated != 1 || stripped.SnapshotsImported != 0 || stripped.FullHashesStripped != 1 {
		t.Fatalf("trusted-to-default policy update failed: %#v, %v", stripped, err)
	}
	snapshots, _ = target.ListSnapshots(ctx, drive.ID)
	entries, err = target.ListEntries(ctx, snapshots[0].ID, "", 20, 0)
	if err != nil || len(snapshots) != 1 || snapshots[0].HashMode != "imported" || entries[0].Hash != "" {
		t.Fatalf("default policy was not applied in place: %#v, %#v, %v", snapshots, entries, err)
	}
	restored, err := target.ImportCatalog(ctx, sourcePath, catalog.ImportCatalogOptions{TrustFullHashes: true})
	if err != nil || restored.SnapshotsUpdated != 1 || restored.FullHashesPreserved != 1 {
		t.Fatalf("default-to-trusted policy update failed: %#v, %v", restored, err)
	}
	snapshots, _ = target.ListSnapshots(ctx, drive.ID)
	entries, err = target.ListEntries(ctx, snapshots[0].ID, "", 20, 0)
	if err != nil || len(snapshots) != 1 || snapshots[0].HashMode != "full" || entries[0].Hash != trustedHash {
		t.Fatalf("trusted policy was not restored in place: %#v, %#v, %v", snapshots, entries, err)
	}
}

func TestImportCatalogRejectsMismatchedCountsAndFutureSchema(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sourcePath := filepath.Join(t.TempDir(), "source.db")
	source, err := catalog.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	drive, err := source.CreateDrive(ctx, catalog.NewDrive{Name: "Damaged"})
	if err != nil {
		t.Fatal(err)
	}
	writeSnapshot(t, source, drive.ID, []catalog.Entry{{Path: "one.bin", Name: "one.bin", Kind: "file", Size: 1}})
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`UPDATE snapshots SET file_count=2 WHERE status='complete'`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	target, err := catalog.Open(filepath.Join(t.TempDir(), "target.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if _, err := target.ImportCatalog(ctx, sourcePath, catalog.ImportCatalogOptions{}); err == nil || !strings.Contains(err.Error(), "declared counts") {
		t.Fatalf("mismatched source counts were not rejected: %v", err)
	}
	if drives, err := target.ListDrives(ctx); err != nil || len(drives) != 0 {
		t.Fatalf("invalid import changed target: %#v, %v", drives, err)
	}

	if raw, err = sql.Open("sqlite", sourcePath); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`UPDATE metadata SET value='99' WHERE key='schema_version'`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := target.ImportCatalog(ctx, sourcePath, catalog.ImportCatalogOptions{}); err == nil || !strings.Contains(err.Error(), "newer than supported") {
		t.Fatalf("future source schema was not rejected: %v", err)
	}
	if _, err := target.ImportCatalog(ctx, target.Path(), catalog.ImportCatalogOptions{}); err == nil || !strings.Contains(err.Error(), "same file") {
		t.Fatalf("self-import was not rejected: %v", err)
	}
}

func TestVersionOneCatalogMigratesAndImports(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sourcePath := filepath.Join(t.TempDir(), "schema-v1.db")
	createVersionOneFixture(t, sourcePath)

	targetPath := filepath.Join(t.TempDir(), "target.db")
	target, err := catalog.Open(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	result, err := target.ImportCatalog(ctx, sourcePath, catalog.ImportCatalogOptions{})
	if err != nil || result.SourceSchemaVersion != 1 || result.SnapshotsImported != 1 {
		t.Fatalf("version-one source import failed: %#v, %v", result, err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}

	legacyTargetPath := filepath.Join(t.TempDir(), "legacy-target.db")
	createVersionOneFixture(t, legacyTargetPath)
	migrated, err := catalog.Open(legacyTargetPath)
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	check, err := sql.Open("sqlite", legacyTargetPath)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	var version string
	if err := check.QueryRow(`SELECT value FROM metadata WHERE key='schema_version'`).Scan(&version); err != nil || version != "2" {
		t.Fatalf("target schema did not migrate to version 2: %q, %v", version, err)
	}
}

func createVersionOneFixture(t *testing.T, database string) {
	t.Helper()
	schema, err := os.ReadFile(filepath.Join("testdata", "schema-v1.sql"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", database)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
}

func TestImportedSnapshotHistoryUsesCompletionTimeNotNewRowID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sourcePath := filepath.Join(t.TempDir(), "source.db")
	source, err := catalog.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	drive, err := source.CreateDrive(ctx, catalog.NewDrive{Name: "Archive chronology"})
	if err != nil {
		t.Fatal(err)
	}
	firstSource := writeSnapshot(t, source, drive.ID, []catalog.Entry{{Path: "old.txt", Name: "old.txt", Kind: "file", Size: 1}})
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	setSnapshotTimes(t, sourcePath, firstSource.ID, 100, 110)

	target, err := catalog.Open(filepath.Join(t.TempDir(), "target.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if _, err := target.ImportCatalog(ctx, sourcePath, catalog.ImportCatalogOptions{}); err != nil {
		t.Fatal(err)
	}
	localLatest := writeSnapshot(t, target, drive.ID, []catalog.Entry{{Path: "local.txt", Name: "local.txt", Kind: "file", Size: 2}})

	source, err = catalog.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	secondSource := writeSnapshot(t, source, drive.ID, []catalog.Entry{{Path: "less-old.txt", Name: "less-old.txt", Kind: "file", Size: 3}})
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	setSnapshotTimes(t, sourcePath, secondSource.ID, 200, 210)
	if _, err := target.ImportCatalog(ctx, sourcePath, catalog.ImportCatalogOptions{}); err != nil {
		t.Fatal(err)
	}

	snapshots, err := target.ListSnapshots(ctx, drive.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 3 || snapshots[0].ID != localLatest.ID || snapshots[1].CompletedAt.Unix() != 210 || snapshots[2].CompletedAt.Unix() != 110 {
		t.Fatalf("snapshot history is not chronological: %#v", snapshots)
	}
}

func TestImportPreservesIdenticalSnapshotsWithDistinctSourceIDs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sourcePath := filepath.Join(t.TempDir(), "source.db")
	source, err := catalog.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	drive, err := source.CreateDrive(ctx, catalog.NewDrive{Name: "Repeated scan"})
	if err != nil {
		t.Fatal(err)
	}
	first := writeSnapshot(t, source, drive.ID, []catalog.Entry{{Path: "same.txt", Name: "same.txt", Kind: "file", Size: 1}})
	second := writeSnapshot(t, source, drive.ID, []catalog.Entry{{Path: "same.txt", Name: "same.txt", Kind: "file", Size: 1}})
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	setSnapshotTimes(t, sourcePath, first.ID, 100, 110)
	setSnapshotTimes(t, sourcePath, second.ID, 100, 110)

	target, err := catalog.Open(filepath.Join(t.TempDir(), "target.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	result, err := target.ImportCatalog(ctx, sourcePath, catalog.ImportCatalogOptions{})
	if err != nil || result.SnapshotsImported != 2 || result.SnapshotsSkipped != 0 {
		t.Fatalf("distinct identical snapshots were collapsed: %#v, %v", result, err)
	}
	if snapshots, err := target.ListSnapshots(ctx, drive.ID); err != nil || len(snapshots) != 2 {
		t.Fatalf("expected both snapshots in history: %#v, %v", snapshots, err)
	}
}

func TestImportRejectsOverflowAndInvalidDriveTags(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	makeSource := func(name string) (string, catalog.Drive) {
		path := filepath.Join(t.TempDir(), name+".db")
		db, err := catalog.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		drive, err := db.CreateDrive(ctx, catalog.NewDrive{Name: name})
		if err != nil {
			t.Fatal(err)
		}
		writeSnapshot(t, db, drive.ID, []catalog.Entry{
			{Path: "one.bin", Name: "one.bin", Kind: "file", Size: math.MaxInt64},
			{Path: "two.bin", Name: "two.bin", Kind: "file", Size: math.MaxInt64},
		})
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		return path, drive
	}

	overflowPath, _ := makeSource("Overflow")
	raw, err := sql.Open("sqlite", overflowPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`UPDATE snapshots SET total_bytes=0`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	target, err := catalog.Open(filepath.Join(t.TempDir(), "target.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if _, err := target.ImportCatalog(ctx, overflowPath, catalog.ImportCatalogOptions{}); err == nil || !strings.Contains(err.Error(), "overflows int64") {
		t.Fatalf("overflow fixture was not rejected: %v", err)
	}

	badTagsPath, badTagsDrive := makeSource("Bad tags")
	raw, err = sql.Open("sqlite", badTagsPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`UPDATE drives SET tags='{"not":"an array"}' WHERE id=?`, badTagsDrive.ID); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := target.ImportCatalog(ctx, badTagsPath, catalog.ImportCatalogOptions{}); err == nil || !strings.Contains(err.Error(), "drive tags") {
		t.Fatalf("invalid tag JSON was not rejected: %v", err)
	}
}

func TestFailedImportDoesNotMigrateVersionOneTarget(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sourcePath := filepath.Join(t.TempDir(), "invalid-source.db")
	source, err := catalog.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	drive, err := source.CreateDrive(ctx, catalog.NewDrive{Name: "Invalid source"})
	if err != nil {
		t.Fatal(err)
	}
	writeSnapshot(t, source, drive.ID, []catalog.Entry{{Path: "one.bin", Name: "one.bin", Kind: "file", Size: 1}})
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`UPDATE snapshots SET file_count=2`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	targetPath := filepath.Join(t.TempDir(), "version-one-target.db")
	target, err := catalog.Open(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err = sql.Open("sqlite", targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`DROP TABLE catalog_imports; UPDATE metadata SET value='1' WHERE key='schema_version'`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	target, err = catalog.OpenImportTarget(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.ImportCatalog(ctx, sourcePath, catalog.ImportCatalogOptions{}); err == nil || !strings.Contains(err.Error(), "declared counts") {
		t.Fatalf("invalid source was not rejected: %v", err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err = sql.Open("sqlite", targetPath)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var version string
	if err := raw.QueryRow(`SELECT value FROM metadata WHERE key='schema_version'`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	var importTable int
	if err := raw.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='catalog_imports')`).Scan(&importTable); err != nil {
		t.Fatal(err)
	}
	if version != "1" || importTable != 0 {
		t.Fatalf("failed import migrated target: version=%q catalog_imports=%d", version, importTable)
	}
}

func TestImportRejectsSourceWithWALSidecars(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sourcePath := filepath.Join(t.TempDir(), "active-source.db")
	source, err := catalog.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath+"-wal", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	target, err := catalog.Open(filepath.Join(t.TempDir(), "target.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if _, err := target.ImportCatalog(ctx, sourcePath, catalog.ImportCatalogOptions{}); err == nil || !strings.Contains(err.Error(), "active SQLite -wal sidecar") {
		t.Fatalf("active source sidecar was not rejected: %v", err)
	}
}

func setSnapshotTimes(t *testing.T, database string, snapshotID, started, completed int64) {
	t.Helper()
	raw, err := sql.Open("sqlite", database)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`UPDATE snapshots SET started_at=?, completed_at=? WHERE id=?`, started, completed, snapshotID); err != nil {
		t.Fatal(err)
	}
}

func fileSHA256(t *testing.T, filename string) string {
	t.Helper()
	content, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(content))
}

func assertNoSQLiteSidecars(t *testing.T, filename string) {
	t.Helper()
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(filename + suffix); !os.IsNotExist(err) {
			t.Fatalf("unexpected SQLite sidecar %s: %v", filename+suffix, err)
		}
	}
}
