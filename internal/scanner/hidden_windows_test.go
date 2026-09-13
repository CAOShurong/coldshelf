//go:build windows

package scanner_test

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/CAOShurong/coldshelf/internal/catalog"
	"github.com/CAOShurong/coldshelf/internal/scanner"
)

func TestScanMarksWindowsHiddenAttribute(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "desktop.ini")
	if err := os.WriteFile(path, []byte("[.ShellClassInfo]"), 0o644); err != nil {
		t.Fatal(err)
	}
	wide, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.SetFileAttributes(wide, syscall.FILE_ATTRIBUTE_HIDDEN); err != nil {
		t.Fatal(err)
	}

	var entries []catalog.Entry
	_, err = scanner.Scan(context.Background(), root, scanner.Options{},
		func(entry catalog.Entry) error { entries = append(entries, entry); return nil }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "desktop.ini" || !entries[0].Hidden {
		t.Fatalf("expected hidden desktop.ini, got %#v", entries)
	}
}
