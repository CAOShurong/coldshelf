package importer

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/CAOShurong/coldshelf/internal/catalog"
)

type EFUResult struct {
	Files       int64
	Directories int64
	Bytes       int64
}

func EFU(reader io.Reader, stripPrefix string, yield func(catalog.Entry) error) (EFUResult, error) {
	if yield == nil {
		return EFUResult{}, errors.New("EFU importer requires a yield function")
	}
	csvReader := csv.NewReader(reader)
	csvReader.FieldsPerRecord = -1
	csvReader.ReuseRecord = false
	header, err := csvReader.Read()
	if err != nil {
		return EFUResult{}, fmt.Errorf("read EFU header: %w", err)
	}
	if len(header) > 0 {
		header[0] = strings.TrimPrefix(header[0], "\ufeff")
	}
	columns := make(map[string]int)
	for index, name := range header {
		columns[strings.ToLower(strings.TrimSpace(name))] = index
	}
	filenameColumn, ok := columns["filename"]
	if !ok {
		return EFUResult{}, errors.New("invalid EFU: missing Filename column")
	}
	sizeColumn := columns["size"]
	modifiedColumn := columns["date modified"]
	attributesColumn := columns["attributes"]
	stripPrefix = normalizeWindowsPath(stripPrefix)

	var result EFUResult
	for line := 2; ; line++ {
		record, err := csvReader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return result, fmt.Errorf("read EFU line %d: %w", line, err)
		}
		if filenameColumn >= len(record) {
			continue
		}
		fullPath := normalizeWindowsPath(record[filenameColumn])
		catalogPath := strings.TrimPrefix(fullPath, stripPrefix)
		catalogPath = strings.TrimLeft(catalogPath, "/")
		if catalogPath == "" {
			continue
		}
		entry := catalog.Entry{
			Path:       catalogPath,
			ParentPath: parentPath(catalogPath),
			Name:       path.Base(catalogPath),
			Hidden:     strings.HasPrefix(path.Base(catalogPath), "."),
			Kind:       "file",
		}
		attributes := field(record, attributesColumn)
		if strings.Contains(strings.ToUpper(attributes), "D") || strings.HasSuffix(fullPath, "/") {
			entry.Kind = "directory"
			result.Directories++
		} else {
			entry.Extension = strings.ToLower(strings.TrimPrefix(path.Ext(entry.Name), "."))
			if rawSize := field(record, sizeColumn); rawSize != "" {
				entry.Size, _ = strconv.ParseInt(rawSize, 10, 64)
			}
			entry.ModifiedAt = parseEFUTime(field(record, modifiedColumn))
			result.Files++
			result.Bytes += entry.Size
		}
		if err := yield(entry); err != nil {
			return result, err
		}
	}
	return result, nil
}

func field(record []string, index int) string {
	if index < 0 || index >= len(record) {
		return ""
	}
	return strings.TrimSpace(record[index])
}

func normalizeWindowsPath(value string) string {
	value = strings.TrimSpace(strings.Trim(value, `"`))
	value = strings.ReplaceAll(value, `\`, "/")
	for strings.Contains(value, "//") {
		value = strings.ReplaceAll(value, "//", "/")
	}
	return strings.TrimSuffix(value, "/")
}

func parentPath(value string) string {
	parent := path.Dir(value)
	if parent == "." || parent == "/" {
		return ""
	}
	return parent
}

func parseEFUTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	if number, err := strconv.ParseInt(value, 10, 64); err == nil {
		// Everything commonly exports Windows FILETIME: 100 ns intervals since 1601.
		if number > 116444736000000000 {
			return time.Unix((number-116444736000000000)/10_000_000, 0).UTC()
		}
		if number > 0 {
			return time.Unix(number, 0).UTC()
		}
	}
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02 15:04:05",
		"1/2/2006 3:04 PM",
		"02/01/2006 15:04",
	} {
		if parsed, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}
