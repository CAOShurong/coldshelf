package catalog_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CAOShurong/coldshelf/internal/catalog"
)

func TestCatalogLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := catalog.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	drive, err := db.CreateDrive(ctx, catalog.NewDrive{
		Name: "Blue Archive", Location: "Shelf B", Tags: []string{"video", "Video", " 2026 "},
	})
	if err != nil {
		t.Fatal(err)
	}
	if drive.ID == "" || len(drive.Tags) != 2 {
		t.Fatalf("unexpected drive: %#v", drive)
	}

	first := writeSnapshot(t, db, drive.ID, []catalog.Entry{
		{Path: "Projects", ParentPath: "", Name: "Projects", Kind: "directory"},
		{Path: "Projects/Aurora.mov", ParentPath: "Projects", Name: "Aurora.mov", Extension: "mov", Kind: "file", Size: 100, ModifiedAt: time.Unix(100, 0), Hash: "sha256:aaa"},
	})
	second := writeSnapshot(t, db, drive.ID, []catalog.Entry{
		{Path: "Projects", ParentPath: "", Name: "Projects", Kind: "directory"},
		{Path: "Projects/Aurora-final.mov", ParentPath: "Projects", Name: "Aurora-final.mov", Extension: "mov", Kind: "file", Size: 120, ModifiedAt: time.Unix(200, 0), Hash: "sha256:bbb"},
	})

	drives, err := db.ListDrives(ctx)
	if err != nil || len(drives) != 1 || drives[0].FileCount != 1 || drives[0].TotalBytes != 120 {
		t.Fatalf("list drives: %#v, %v", drives, err)
	}
	hits, err := db.Search(ctx, "Aurora final", "", 10)
	if err != nil || len(hits) != 1 || hits[0].Path != "Projects/Aurora-final.mov" {
		t.Fatalf("search: %#v, %v", hits, err)
	}
	entries, err := db.ListEntries(ctx, second.ID, "Projects", 20, 0)
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries: %#v, %v", entries, err)
	}
	changes, err := db.Diff(ctx, drive.ID, first.ID, second.ID, 10)
	if err != nil || len(changes) != 2 {
		t.Fatalf("diff: %#v, %v", changes, err)
	}
	stats, err := db.Stats(ctx)
	if err != nil || stats.DriveCount != 1 || stats.SnapshotCount != 2 || stats.FileCount != 1 {
		t.Fatalf("stats: %#v, %v", stats, err)
	}
	patchLocation := "Fireproof cabinet"
	updated, err := db.UpdateDrive(ctx, drive.ID, catalog.DrivePatch{Location: &patchLocation})
	if err != nil || updated.Location != patchLocation {
		t.Fatalf("update: %#v, %v", updated, err)
	}

	var jsonExport bytes.Buffer
	if err := db.ExportJSON(ctx, &jsonExport); err != nil {
		t.Fatal(err)
	}
	var exported map[string]any
	if err := json.Unmarshal(jsonExport.Bytes(), &exported); err != nil || exported["version"].(float64) != 1 {
		t.Fatalf("JSON export is invalid: %v", err)
	}
	var csvExport bytes.Buffer
	if err := db.ExportCSV(ctx, &csvExport); err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(&csvExport).ReadAll()
	if err != nil || len(records) != 3 {
		t.Fatalf("CSV export: %d records, %v", len(records), err)
	}
}

func TestDuplicatesRequireFullSHA256(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := catalog.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for index, hash := range []string{"sha256:same", "sha256:same", "quick:same", "quick:same"} {
		drive, err := db.CreateDrive(ctx, catalog.NewDrive{Name: "Drive " + string(rune('A'+index))})
		if err != nil {
			t.Fatal(err)
		}
		writeSnapshot(t, db, drive.ID, []catalog.Entry{{Path: "copy.bin", Name: "copy.bin", Extension: "bin", Kind: "file", Size: 42, Hash: hash}})
	}
	groups, err := db.Duplicates(ctx, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || len(groups[0].Files) != 2 || !strings.HasPrefix(groups[0].Hash, "sha256:") {
		t.Fatalf("unexpected duplicate groups: %#v", groups)
	}
}

func TestDiffDoesNotTreatHashModeChangeAsContentChange(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := catalog.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	drive, err := db.CreateDrive(ctx, catalog.NewDrive{Name: "Hash Modes"})
	if err != nil {
		t.Fatal(err)
	}
	modified := time.Unix(300, 0)
	first := writeSnapshot(t, db, drive.ID, []catalog.Entry{{Path: "same.bin", Name: "same.bin", Kind: "file", Size: 42, ModifiedAt: modified, Hash: "sha256:full"}})
	second := writeSnapshot(t, db, drive.ID, []catalog.Entry{{Path: "same.bin", Name: "same.bin", Kind: "file", Size: 42, ModifiedAt: modified, Hash: "quick:sample"}})
	changes, err := db.Diff(ctx, drive.ID, first.ID, second.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Fatalf("hash algorithm change alone is not a content change: %#v", changes)
	}
}

func writeSnapshot(t *testing.T, db *catalog.Catalog, driveID string, entries []catalog.Entry) catalog.Snapshot {
	t.Helper()
	writer, err := db.StartSnapshot(context.Background(), driveID, "/mounted", "full")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if err := writer.Add(entry); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := writer.Complete()
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
