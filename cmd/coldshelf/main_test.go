package main

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/CAOShurong/coldshelf/internal/catalog"
)

func TestNormalizeCommandArgsKeepsBareLaunchInteractive(t *testing.T) {
	t.Parallel()
	got := normalizeCommandArgs(nil)
	want := []string{"serve", "--open"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestParseServeOptionsRequiresExplicitOpen(t *testing.T) {
	t.Parallel()

	background, err := parseServeOptions(nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if background.openBrowser {
		t.Fatal("explicit serve must not open a browser by default")
	}

	interactive, err := parseServeOptions([]string{"--open"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !interactive.openBrowser {
		t.Fatal("--open must opt in to opening the browser")
	}
}

func TestParseDemoOptionsRequiresExplicitOpen(t *testing.T) {
	t.Parallel()

	background, err := parseDemoOptions([]string{"--serve"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !background.serve || background.openBrowser {
		t.Fatalf("unexpected background demo options: %#v", background)
	}

	interactive, err := parseDemoOptions([]string{"--serve", "--open"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !interactive.serve || !interactive.openBrowser {
		t.Fatalf("unexpected interactive demo options: %#v", interactive)
	}

	if _, err := parseDemoOptions([]string{"--open"}, io.Discard); err == nil {
		t.Fatal("--open without --serve must be rejected")
	}
}

func TestOpenBrowserIfRequested(t *testing.T) {
	t.Parallel()
	called := make(chan string, 1)
	opener := func(url string) error {
		called <- url
		return nil
	}

	openBrowserIfRequested(false, "http://127.0.0.1:4877", io.Discard, opener, 0)
	select {
	case url := <-called:
		t.Fatalf("background mode unexpectedly opened %s", url)
	default:
	}

	openBrowserIfRequested(true, "http://127.0.0.1:4877", io.Discard, opener, 0)
	select {
	case url := <-called:
		if url != "http://127.0.0.1:4877" {
			t.Fatalf("unexpected URL: %s", url)
		}
	case <-time.After(time.Second):
		t.Fatal("interactive mode did not call the browser opener")
	}
}

func TestInterspersedFlags(t *testing.T) {
	t.Parallel()
	got := interspersed([]string{"Archive Drive", "--from", "1", "--json", "--to=2"}, "json")
	want := []string{"--from", "1", "--json", "--to=2", "Archive Drive"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestParseImportCatalogOptionsSupportsInterspersedFlags(t *testing.T) {
	t.Parallel()
	options, err := parseImportCatalogOptions([]string{
		"source.db", "--db", "target.db", "--dry-run", "--rename-conflicts", "--trust-full-hashes", "--json",
	}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if options.sourcePath != "source.db" || options.dbPath != "target.db" {
		t.Fatalf("unexpected paths: %#v", options)
	}
	if !options.dryRun || !options.renameConflicts || !options.trustFullHashes || !options.jsonOutput {
		t.Fatalf("boolean flags were not parsed: %#v", options)
	}
}

func TestParseImportCatalogOptionsRequiresOneSource(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{nil, {"first.db", "second.db"}} {
		if _, err := parseImportCatalogOptions(args, io.Discard); err == nil {
			t.Fatalf("expected usage error for %#v", args)
		}
	}
}

func TestImportCatalogDryRunDoesNotCreateOrChangeTarget(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	sourcePath := filepath.Join(root, "source.db")
	source, err := catalog.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}

	missingTarget := filepath.Join(root, "missing-target.db")
	if err := importCatalogCommand([]string{sourcePath, "--db", missingTarget, "--dry-run"}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(missingTarget); !os.IsNotExist(err) {
		t.Fatalf("dry run created a missing target: %v", err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(missingTarget + suffix); !os.IsNotExist(err) {
			t.Fatalf("dry run created a missing target sidecar %s: %v", suffix, err)
		}
	}

	existingTarget := filepath.Join(root, "existing-target.db")
	target, err := catalog.Open(existingTarget)
	if err != nil {
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(existingTarget)
	if err != nil {
		t.Fatal(err)
	}
	if err := importCatalogCommand([]string{sourcePath, "--db", existingTarget, "--dry-run"}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(existingTarget)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("dry run changed the bytes of an existing target")
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(existingTarget + suffix); !os.IsNotExist(err) {
			t.Fatalf("dry run created target sidecar %s: %v", suffix, err)
		}
	}

	if err := importCatalogCommand([]string{sourcePath, "--db", sourcePath, "--dry-run"}, io.Discard, io.Discard); err == nil {
		t.Fatal("dry-run self-import must be rejected")
	}
}

func TestHumanBytes(t *testing.T) {
	t.Parallel()
	if got := humanBytes(1_500_000); got != "1.50 MB" {
		t.Fatalf("unexpected formatting: %s", got)
	}
}
