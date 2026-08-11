package main

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTarArchiveMakesBinaryExecutable(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	binary := filepath.Join(directory, "input-binary")
	readme := filepath.Join(directory, "README.md")
	if err := os.WriteFile(binary, []byte("binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(readme, []byte("readme"), 0o644); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(directory, "release.tar.gz")
	if err := writeTarGz(archivePath, map[string]string{"coldshelf": binary, "README.md": readme}); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	tarReader := tar.NewReader(gzipReader)
	modes := map[string]int64{}
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		modes[header.Name] = header.Mode
	}
	if modes["coldshelf"] != 0o755 || modes["README.md"] != 0o644 {
		t.Fatalf("unexpected archive modes: %#v", modes)
	}
}

func TestParseSourceDate(t *testing.T) {
	t.Parallel()
	want := time.Date(2026, time.August, 11, 5, 10, 8, 0, time.UTC)
	for _, value := range []string{"1786425008", "2026-08-11T05:10:08Z"} {
		value := value
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			got, err := parseSourceDate(value)
			if err != nil {
				t.Fatal(err)
			}
			if !got.Equal(want) {
				t.Fatalf("parseSourceDate(%q) = %s, want %s", value, got, want)
			}
		})
	}
}

func TestResolveBuildDateRejectsInvalidSourceDateEpoch(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "not-a-date")
	if _, err := resolveBuildDate(); err == nil {
		t.Fatal("resolveBuildDate() accepted an invalid SOURCE_DATE_EPOCH")
	}
}
