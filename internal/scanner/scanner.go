package scanner

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/CAOShurong/coldshelf/internal/catalog"
)

type HashMode string

const (
	HashNone  HashMode = "none"
	HashQuick HashMode = "quick"
	HashFull  HashMode = "full"
)

type Options struct {
	HashMode HashMode
	Exclude  []string
}

type Progress struct {
	CurrentPath    string    `json:"current_path"`
	Files          int64     `json:"files"`
	Directories    int64     `json:"directories"`
	Bytes          int64     `json:"bytes"`
	Errors         int64     `json:"errors"`
	StartedAt      time.Time `json:"started_at"`
	LastReportedAt time.Time `json:"last_reported_at"`
}

type Result struct {
	Root        string
	Files       int64
	Directories int64
	Bytes       int64
	Errors      int64
	Duration    time.Duration
}

type YieldFunc func(catalog.Entry) error
type ProgressFunc func(Progress)
type ErrorFunc func(path string, err error)

func Scan(ctx context.Context, root string, options Options, yield YieldFunc, onProgress ProgressFunc, onError ErrorFunc) (Result, error) {
	if yield == nil {
		return Result{}, errors.New("scanner requires a yield function")
	}
	if options.HashMode == "" {
		options.HashMode = HashNone
	}
	if options.HashMode != HashNone && options.HashMode != HashQuick && options.HashMode != HashFull {
		return Result{}, fmt.Errorf("unsupported hash mode %q", options.HashMode)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return Result{}, fmt.Errorf("resolve scan root: %w", err)
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		return Result{}, fmt.Errorf("open scan root: %w", err)
	}
	if !info.IsDir() {
		return Result{}, errors.New("scan root must be a directory or mounted volume")
	}

	started := time.Now()
	progress := Progress{StartedAt: started, LastReportedAt: started}
	result := Result{Root: absRoot}
	lastReport := started

	err = filepath.WalkDir(absRoot, func(fullPath string, item fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			progress.Errors++
			result.Errors++
			if onError != nil {
				onError(fullPath, walkErr)
			}
			return nil
		}
		if fullPath == absRoot {
			return nil
		}

		relative, err := filepath.Rel(absRoot, fullPath)
		if err != nil {
			return err
		}
		catalogPath := filepath.ToSlash(relative)
		if excluded(catalogPath, item.Name(), options.Exclude) {
			if item.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		entry := catalog.Entry{
			Path:       catalogPath,
			ParentPath: parentCatalogPath(catalogPath),
			Name:       item.Name(),
			Hidden:     strings.HasPrefix(item.Name(), "."),
		}
		if item.Type()&os.ModeSymlink != 0 {
			entry.Kind = "symlink"
		} else if item.IsDir() {
			entry.Kind = "directory"
		} else {
			entry.Kind = "file"
		}

		itemInfo, err := item.Info()
		if err != nil {
			progress.Errors++
			result.Errors++
			if onError != nil {
				onError(fullPath, err)
			}
			return nil
		}
		entry.ModifiedAt = itemInfo.ModTime().UTC()
		if entry.Kind == "file" {
			entry.Size = itemInfo.Size()
			entry.Extension = strings.ToLower(strings.TrimPrefix(filepath.Ext(item.Name()), "."))
			if options.HashMode != HashNone {
				entry.Hash, err = hashFile(ctx, fullPath, itemInfo.Size(), options.HashMode)
				if err != nil {
					progress.Errors++
					result.Errors++
					if onError != nil {
						onError(fullPath, err)
					}
				}
			}
			progress.Files++
			progress.Bytes += entry.Size
			result.Files++
			result.Bytes += entry.Size
		} else if entry.Kind == "directory" {
			progress.Directories++
			result.Directories++
		}
		progress.CurrentPath = catalogPath

		if err := yield(entry); err != nil {
			return err
		}
		now := time.Now()
		if onProgress != nil && (progress.Files+progress.Directories)%250 == 0 && now.Sub(lastReport) >= 100*time.Millisecond {
			progress.LastReportedAt = now
			onProgress(progress)
			lastReport = now
		}
		return nil
	})
	result.Duration = time.Since(started)
	if onProgress != nil {
		progress.LastReportedAt = time.Now()
		onProgress(progress)
	}
	if err != nil {
		return result, fmt.Errorf("scan %s: %w", absRoot, err)
	}
	return result, nil
}

func excluded(catalogPath, name string, patterns []string) bool {
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(filepath.ToSlash(pattern))
		if pattern == "" {
			continue
		}
		matchedPath, _ := path.Match(pattern, catalogPath)
		matchedName, _ := path.Match(pattern, name)
		directoryPattern := strings.TrimSuffix(pattern, "/**")
		matchesDirectoryTree := strings.HasSuffix(pattern, "/**") &&
			(catalogPath == directoryPattern || strings.HasPrefix(catalogPath, directoryPattern+"/"))
		if matchedPath || matchedName || matchesDirectoryTree {
			return true
		}
		if runtime.GOOS == "windows" {
			lowerPattern := strings.ToLower(pattern)
			lowerPath := strings.ToLower(catalogPath)
			lowerName := strings.ToLower(name)
			matchedPath, _ = path.Match(lowerPattern, lowerPath)
			matchedName, _ = path.Match(lowerPattern, lowerName)
			if matchedPath || matchedName {
				return true
			}
		}
	}
	return false
}

func parentCatalogPath(catalogPath string) string {
	parent := path.Dir(catalogPath)
	if parent == "." || parent == "/" {
		return ""
	}
	return parent
}
