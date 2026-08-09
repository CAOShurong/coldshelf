package main

import (
	"reflect"
	"testing"
)

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
