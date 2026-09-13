package scanner

import (
	"os"
	"strings"
	"syscall"
)

func fileIsHidden(name string, info os.FileInfo) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	if info == nil {
		return false
	}
	stat, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return false
	}
	return stat.FileAttributes&syscall.FILE_ATTRIBUTE_HIDDEN != 0
}
