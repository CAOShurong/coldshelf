package scanner_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/CAOShurong/coldshelf/internal/catalog"
	"github.com/CAOShurong/coldshelf/internal/scanner"
)

func BenchmarkScanMetadata1000Files(b *testing.B) {
	root := b.TempDir()
	for index := 0; index < 1000; index++ {
		directory := filepath.Join(root, fmt.Sprintf("group-%02d", index%20))
		if err := os.MkdirAll(directory, 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, fmt.Sprintf("entry-%05d.txt", index)), []byte("fixture"), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		_, err := scanner.Scan(context.Background(), root, scanner.Options{}, func(catalog.Entry) error { return nil }, nil, nil)
		if err != nil {
			b.Fatal(err)
		}
	}
}
