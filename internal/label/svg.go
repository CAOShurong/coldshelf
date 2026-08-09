package label

import (
	"bytes"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/CAOShurong/coldshelf/internal/catalog"
	qrcode "github.com/skip2/go-qrcode"
)

const (
	canvasWidth  = 1200
	canvasHeight = 720
)

func SVG(drive catalog.Drive) ([]byte, error) {
	code, err := qrcode.New("coldshelf:drive:"+drive.ID, qrcode.Medium)
	if err != nil {
		return nil, fmt.Errorf("create label QR: %w", err)
	}
	bitmap := code.Bitmap()
	if len(bitmap) == 0 {
		return nil, fmt.Errorf("create label QR: empty bitmap")
	}
	const qrX, qrY, qrSize = 748, 92, 360
	module := float64(qrSize) / float64(len(bitmap))

	var out bytes.Buffer
	fmt.Fprintf(&out, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-labelledby="title desc">`, canvasWidth, canvasHeight, canvasWidth, canvasHeight)
	fmt.Fprintf(&out, `<title id="title">ColdShelf label for %s</title>`, html.EscapeString(drive.Name))
	fmt.Fprintf(&out, `<desc id="desc">Printable label identifying offline drive %s</desc>`, html.EscapeString(drive.ID))
	out.WriteString(`<rect width="1200" height="720" rx="48" fill="#f4f0e7"/>`)
	out.WriteString(`<rect x="24" y="24" width="1152" height="672" rx="34" fill="none" stroke="#18211b" stroke-width="4" stroke-dasharray="12 10"/>`)
	out.WriteString(`<path d="M88 122h74l21 26h116v72H88z" fill="#d7ff64" stroke="#18211b" stroke-width="5" stroke-linejoin="round"/>`)
	out.WriteString(`<text x="88" y="282" font-family="Arial, Helvetica, sans-serif" font-size="35" font-weight="700" letter-spacing="4" fill="#5b625c">COLDSHELF / OFFLINE DRIVE</text>`)
	fmt.Fprintf(&out, `<text x="88" y="370" font-family="Arial, Helvetica, sans-serif" font-size="72" font-weight="800" fill="#18211b">%s</text>`, html.EscapeString(truncate(drive.Name, 24)))
	fmt.Fprintf(&out, `<text x="88" y="432" font-family="ui-monospace, SFMono-Regular, Consolas, monospace" font-size="31" fill="#4e584f">%s</text>`, html.EscapeString(drive.ID))
	location := drive.Location
	if strings.TrimSpace(location) == "" {
		location = "Location: ____________________"
	} else {
		location = "Location: " + location
	}
	fmt.Fprintf(&out, `<text x="88" y="520" font-family="Arial, Helvetica, sans-serif" font-size="34" fill="#18211b">%s</text>`, html.EscapeString(truncate(location, 34)))
	meta := "Not scanned yet"
	if !drive.LastScannedAt.IsZero() {
		meta = fmt.Sprintf("%s files · %s · scanned %s", formatCount(drive.FileCount), formatBytes(drive.TotalBytes), drive.LastScannedAt.In(time.Local).Format("2006-01-02"))
	}
	fmt.Fprintf(&out, `<text x="88" y="576" font-family="Arial, Helvetica, sans-serif" font-size="27" fill="#5b625c">%s</text>`, html.EscapeString(meta))
	out.WriteString(`<text x="88" y="642" font-family="Arial, Helvetica, sans-serif" font-size="24" fill="#5b625c">Scan this label to recover the catalog ID. No cloud or account required.</text>`)
	out.WriteString(`<rect x="724" y="68" width="408" height="408" rx="24" fill="#ffffff" stroke="#18211b" stroke-width="4"/>`)
	for row, line := range bitmap {
		for column, filled := range line {
			if !filled {
				continue
			}
			x := float64(qrX) + float64(column)*module
			y := float64(qrY) + float64(row)*module
			fmt.Fprintf(&out, `<rect x="%.3f" y="%.3f" width="%.3f" height="%.3f" fill="#18211b"/>`, x, y, module+0.02, module+0.02)
		}
	}
	out.WriteString(`<text x="928" y="535" text-anchor="middle" font-family="Arial, Helvetica, sans-serif" font-size="24" font-weight="700" fill="#18211b">KNOW WHAT'S INSIDE</text>`)
	out.WriteString(`<text x="928" y="570" text-anchor="middle" font-family="Arial, Helvetica, sans-serif" font-size="20" fill="#5b625c">even while this drive is unplugged</text>`)
	out.WriteString(`</svg>`)
	return out.Bytes(), nil
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit-1]) + "…"
}

func formatCount(value int64) string {
	if value < 1000 {
		return fmt.Sprintf("%d", value)
	}
	if value < 1_000_000 {
		return fmt.Sprintf("%.1fk", float64(value)/1000)
	}
	return fmt.Sprintf("%.1fM", float64(value)/1_000_000)
}

func formatBytes(value int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	amount := float64(value)
	unit := 0
	for amount >= 1000 && unit < len(units)-1 {
		amount /= 1000
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d %s", value, units[unit])
	}
	return fmt.Sprintf("%.1f %s", amount, units[unit])
}
