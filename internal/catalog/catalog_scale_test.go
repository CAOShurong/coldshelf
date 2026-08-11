package catalog_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/CAOShurong/coldshelf/internal/catalog"
)

const (
	defaultScaleEntries = 1_000_000
	scaleFixtureSeed    = uint64(0x5eed_c01d_5e1f)
	scaleSearchRepeats  = 25
)

type scaleSearchCheck struct {
	metric string
	query  string
	want   int
}

var scaleSearchChecks = []scaleSearchCheck{
	{metric: "name", query: "coldprooftarget", want: 1},
	{metric: "path", query: "projectlantern", want: 37},
	{metric: "miss", query: "missingconstellation", want: 0},
}

type scaleGenerator struct {
	state uint64
}

func (g *scaleGenerator) next() uint64 {
	// A fixed-seed linear congruential generator is sufficient here: the fixture
	// needs reproducible variety, not cryptographic randomness.
	g.state = g.state*6_364_136_223_846_793_005 + 1_442_695_040_888_963_407
	return g.state
}

func populateScaleFixture(writer *catalog.SnapshotWriter, entries int) error {
	if entries < 43 {
		return fmt.Errorf("scale fixture needs at least 43 entries, got %d", entries)
	}
	generator := scaleGenerator{state: scaleFixtureSeed}
	extensions := [...]string{"jpg", "mov", "pdf", "wav", "zip", "txt", "raw", "csv"}
	modifiedBase := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	for index := 0; index < entries; index++ {
		value := generator.next()
		parent := fmt.Sprintf("Archive/group-%04d/batch-%03d", value%4_096, (value>>12)%128)
		if index < 37 {
			parent = fmt.Sprintf("Archive/projectlantern/segment-%02d", index%7)
		}
		extension := extensions[value%uint64(len(extensions))]
		name := fmt.Sprintf("asset-%09d-%08x.%s", index, uint32(value>>32), extension)
		if index == 42 {
			name = "coldprooftarget-000042.pdf"
			extension = "pdf"
		}
		if err := writer.Add(catalog.Entry{
			Path:       parent + "/" + name,
			ParentPath: parent,
			Name:       name,
			Extension:  extension,
			Kind:       "file",
			Size:       int64(value%64_000_000 + 1),
			ModifiedAt: modifiedBase.Add(time.Duration(index%86_400) * time.Second),
		}); err != nil {
			return err
		}
	}
	return nil
}

func configuredScaleEntries(tb testing.TB) int {
	tb.Helper()
	raw := os.Getenv("COLDSHELF_BENCH_ENTRIES")
	if raw == "" {
		return defaultScaleEntries
	}
	entries, err := strconv.Atoi(raw)
	if err != nil || entries < 43 || entries > 10_000_000 {
		tb.Fatalf("COLDSHELF_BENCH_ENTRIES must be an integer from 43 to 10000000, got %q", raw)
	}
	return entries
}

func assertScaleSearches(tb testing.TB, ctx context.Context, db *catalog.Catalog) {
	tb.Helper()
	for _, check := range scaleSearchChecks {
		hits, err := db.Search(ctx, check.query, "", 100)
		if err != nil {
			tb.Fatalf("search %q: %v", check.query, err)
		}
		if len(hits) != check.want {
			tb.Fatalf("search %q returned %d hits, want %d", check.query, len(hits), check.want)
		}
	}
}

func catalogStorageBytes(path string) int64 {
	var total int64
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(candidate)
		if err == nil {
			total += info.Size()
		}
	}
	return total
}

func TestScaleFixtureSearchCounts(t *testing.T) {
	ctx := context.Background()
	db, err := catalog.Open(filepath.Join(t.TempDir(), "scale-smoke.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	drive, err := db.CreateDrive(ctx, catalog.NewDrive{Name: "Scale fixture"})
	if err != nil {
		t.Fatal(err)
	}
	writer, err := db.StartSnapshot(ctx, drive.ID, "/synthetic", "none")
	if err != nil {
		t.Fatal(err)
	}
	if err := populateScaleFixture(writer, 1_000); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Complete(); err != nil {
		t.Fatal(err)
	}

	assertScaleSearches(t, ctx, db)
}

func BenchmarkCatalogScale(b *testing.B) {
	if b.N != 1 {
		b.Fatalf("run this fixture with -benchtime=1x; got b.N=%d", b.N)
	}
	entries := configuredScaleEntries(b)
	ctx := context.Background()
	databasePath := filepath.Join(b.TempDir(), "catalog-scale.db")
	db, err := catalog.Open(databasePath)
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	drive, err := db.CreateDrive(ctx, catalog.NewDrive{Name: "Deterministic scale fixture"})
	if err != nil {
		b.Fatal(err)
	}
	writer, err := db.StartSnapshot(ctx, drive.ID, "/synthetic", "none")
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	ingestStarted := time.Now()
	if err := populateScaleFixture(writer, entries); err != nil {
		b.Fatal(err)
	}
	snapshot, err := writer.Complete()
	ingestElapsed := time.Since(ingestStarted)
	b.StopTimer()
	if err != nil {
		b.Fatal(err)
	}
	if snapshot.FileCount != int64(entries) {
		b.Fatalf("snapshot contains %d files, want %d", snapshot.FileCount, entries)
	}

	// Correctness is checked before any timing metrics are reported. A fast
	// fixture with the wrong index contents is not a useful baseline.
	assertScaleSearches(b, ctx, db)
	b.ReportMetric(float64(entries), "entries")
	b.ReportMetric(float64(entries)/ingestElapsed.Seconds(), "entries/s")
	b.ReportMetric(float64(ingestElapsed.Nanoseconds())/float64(entries), "ns/entry")
	b.ReportMetric(float64(catalogStorageBytes(databasePath)), "catalog-bytes")

	for _, check := range scaleSearchChecks {
		started := time.Now()
		for iteration := 0; iteration < scaleSearchRepeats; iteration++ {
			hits, err := db.Search(ctx, check.query, "", 100)
			if err != nil {
				b.Fatalf("search %q: %v", check.query, err)
			}
			if len(hits) != check.want {
				b.Fatalf("search %q returned %d hits, want %d", check.query, len(hits), check.want)
			}
		}
		elapsed := time.Since(started)
		b.ReportMetric(float64(check.want), check.metric+"-hits/query")
		b.ReportMetric(float64(elapsed.Nanoseconds())/scaleSearchRepeats, check.metric+"-ns/query")
	}
}
