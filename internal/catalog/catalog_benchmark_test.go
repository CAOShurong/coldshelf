package catalog_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/CAOShurong/coldshelf/internal/catalog"
)

func BenchmarkFTSSearch10000Entries(b *testing.B) {
	ctx := context.Background()
	db, err := catalog.Open(filepath.Join(b.TempDir(), "benchmark.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	drive, err := db.CreateDrive(ctx, catalog.NewDrive{Name: "Benchmark"})
	if err != nil {
		b.Fatal(err)
	}
	writer, err := db.StartSnapshot(ctx, drive.ID, "/benchmark", "none")
	if err != nil {
		b.Fatal(err)
	}
	for index := 0; index < 10_000; index++ {
		name := fmt.Sprintf("invoice_%05d.pdf", index)
		if err := writer.Add(catalog.Entry{Path: "Documents/" + name, ParentPath: "Documents", Name: name, Extension: "pdf", Kind: "file", Size: int64(index)}); err != nil {
			b.Fatal(err)
		}
	}
	if _, err := writer.Complete(); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		if _, err := db.Search(ctx, "invoice 042", "", 100); err != nil {
			b.Fatal(err)
		}
	}
}
