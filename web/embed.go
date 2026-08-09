package web

import "embed"

// Static contains the complete browser application. Keeping the UI embedded
// makes every ColdShelf release a single, portable executable.
//
//go:embed static/*
var Static embed.FS
