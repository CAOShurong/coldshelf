//go:build !windows

package scanner

import (
	"os"
	"strings"
)

func fileIsHidden(name string, info os.FileInfo) bool {
	_ = info
	return strings.HasPrefix(name, ".")
}
