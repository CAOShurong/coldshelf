package label_test

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"

	"github.com/CAOShurong/coldshelf/internal/catalog"
	"github.com/CAOShurong/coldshelf/internal/label"
)

func TestSVGIsValidAndEscaped(t *testing.T) {
	t.Parallel()
	value, err := label.SVG(catalog.Drive{ID: "drv_123", Name: `Photos & "Work"`, Location: "Shelf <B>"})
	if err != nil {
		t.Fatal(err)
	}
	decoder := xml.NewDecoder(bytes.NewReader(value))
	for {
		if _, err := decoder.Token(); err != nil {
			if strings.Contains(err.Error(), "EOF") {
				break
			}
			t.Fatalf("invalid XML: %v", err)
		}
	}
	text := string(value)
	if !strings.Contains(text, "ColdShelf") || !strings.Contains(text, "&amp;") || strings.Contains(text, "Shelf <B>") {
		t.Fatalf("label did not escape content")
	}
}
