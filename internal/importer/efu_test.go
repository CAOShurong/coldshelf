package importer_test

import (
	"strings"
	"testing"

	"github.com/CAOShurong/coldshelf/internal/catalog"
	"github.com/CAOShurong/coldshelf/internal/importer"
)

func TestEFUImport(t *testing.T) {
	t.Parallel()
	input := "\ufeffFilename,Size,Date Modified,Attributes\n" +
		`"E:\Archive\Photos",,133801632000000000,D` + "\n" +
		`"E:\Archive\Photos\frame 01.jpg",2048,133801632000000000,A` + "\n"
	var entries []catalog.Entry
	result, err := importer.EFU(strings.NewReader(input), `E:\Archive`, func(entry catalog.Entry) error {
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Files != 1 || result.Directories != 1 || result.Bytes != 2048 || len(entries) != 2 {
		t.Fatalf("unexpected result: %#v %#v", result, entries)
	}
	if entries[1].Path != "Photos/frame 01.jpg" || entries[1].Extension != "jpg" || entries[1].ModifiedAt.IsZero() {
		t.Fatalf("unexpected imported entry: %#v", entries[1])
	}
}
