package main

import (
	"io"
	"reflect"
	"testing"
	"time"
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

func TestHumanBytes(t *testing.T) {
	t.Parallel()
	if got := humanBytes(1_500_000); got != "1.50 MB" {
		t.Fatalf("unexpected formatting: %s", got)
	}
}
