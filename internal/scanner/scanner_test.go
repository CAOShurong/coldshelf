package scanner_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CAOShurong/coldshelf/internal/catalog"
	"github.com/CAOShurong/coldshelf/internal/scanner"
)

func TestScanMetadataHashAndExclusion(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "keep", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "skip-me"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "keep", "nested", "Report.PDF"), []byte("important bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skip-me", "secret.txt"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}

	var entries []catalog.Entry
	result, err := scanner.Scan(context.Background(), root,
		scanner.Options{HashMode: scanner.HashFull, Exclude: []string{"skip-me/**"}},
		func(entry catalog.Entry) error { entries = append(entries, entry); return nil }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Files != 1 || result.Directories != 2 || len(entries) != 3 {
		t.Fatalf("unexpected scan result: %#v (%d entries)", result, len(entries))
	}
	file := entries[len(entries)-1]
	if file.Path != "keep/nested/Report.PDF" || file.ParentPath != "keep/nested" || file.Extension != "pdf" || !strings.HasPrefix(file.Hash, "sha256:") {
		t.Fatalf("unexpected file entry: %#v", file)
	}
}

func TestScanRejectsFileRoot(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "not-a-drive")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := scanner.Scan(context.Background(), path, scanner.Options{}, func(catalog.Entry) error { return nil }, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "must be a directory") {
		t.Fatalf("expected directory error, got %v", err)
	}
}
