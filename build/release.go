// Command release builds deterministic ColdShelf archives for every supported
// desktop platform. It intentionally uses only the Go standard library so the
// release workflow stays auditable and works on any Go host.
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

type target struct{ OS, Arch string }

var targets = []target{
	{"windows", "amd64"},
	{"windows", "arm64"},
	{"darwin", "amd64"},
	{"darwin", "arm64"},
	{"linux", "amd64"},
	{"linux", "arm64"},
}

var archiveTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func main() {
	version := flag.String("version", "dev", "release version without a leading v")
	output := flag.String("output", "dist", "output directory")
	flag.Parse()
	if err := buildAll(strings.TrimPrefix(*version, "v"), *output); err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}
}

func buildAll(version, output string) error {
	if strings.TrimSpace(version) == "" {
		return fmt.Errorf("version cannot be empty")
	}
	absOutput, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(absOutput, 0o755); err != nil {
		return err
	}
	commit := commandOutput("git", "rev-parse", "--short=12", "HEAD")
	if commit == "" {
		commit = "unknown"
	}
	buildDate, err := resolveBuildDate()
	if err != nil {
		return err
	}

	archives := make([]string, 0, len(targets))
	for _, target := range targets {
		name := fmt.Sprintf("coldshelf_%s_%s_%s", version, target.OS, target.Arch)
		stage, err := os.MkdirTemp(absOutput, ".stage-")
		if err != nil {
			return err
		}
		binary := "coldshelf"
		if target.OS == "windows" {
			binary += ".exe"
		}
		binaryPath := filepath.Join(stage, binary)
		ldflags := fmt.Sprintf("-s -w -X main.version=%s -X main.commit=%s -X main.date=%s", version, commit, buildDate)
		command := exec.Command("go", "build", "-trimpath", "-ldflags", ldflags, "-o", binaryPath, "./cmd/coldshelf")
		command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+target.OS, "GOARCH="+target.Arch)
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		fmt.Printf("Building %s/%s\n", target.OS, target.Arch)
		if err := command.Run(); err != nil {
			os.RemoveAll(stage)
			return fmt.Errorf("build %s/%s: %w", target.OS, target.Arch, err)
		}
		files := map[string]string{
			binary:                   binaryPath,
			"README.md":              "README.md",
			"LICENSE":                "LICENSE",
			"CHANGELOG.md":           "CHANGELOG.md",
			"CONTRIBUTING.md":        "CONTRIBUTING.md",
			"ROADMAP.md":             "ROADMAP.md",
			"SECURITY.md":            "SECURITY.md",
			"THIRD_PARTY_NOTICES.md": "THIRD_PARTY_NOTICES.md",
		}
		if err := addTree(files, "docs", "docs"); err != nil {
			_ = os.RemoveAll(stage)
			return err
		}
		if err := addTree(files, "third_party_licenses", "third_party_licenses"); err != nil {
			_ = os.RemoveAll(stage)
			return err
		}
		var archive string
		if target.OS == "windows" {
			archive = filepath.Join(absOutput, name+".zip")
			err = writeZip(archive, files)
		} else {
			archive = filepath.Join(absOutput, name+".tar.gz")
			err = writeTarGz(archive, files)
		}
		_ = os.RemoveAll(stage)
		if err != nil {
			return err
		}
		archives = append(archives, archive)
	}
	return writeChecksums(filepath.Join(absOutput, "SHA256SUMS"), archives)
}

func resolveBuildDate() (string, error) {
	sourceDate := strings.TrimSpace(os.Getenv("SOURCE_DATE_EPOCH"))
	if sourceDate == "" {
		return time.Now().UTC().Format(time.RFC3339), nil
	}
	parsed, err := parseSourceDate(sourceDate)
	if err != nil {
		return "", fmt.Errorf("invalid SOURCE_DATE_EPOCH %q: expected RFC3339 or Unix seconds: %w", sourceDate, err)
	}
	return parsed.UTC().Format(time.RFC3339), nil
}

func parseSourceDate(value string) (time.Time, error) {
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		return time.Unix(seconds, 0).UTC(), nil
	}
	return time.Parse(time.RFC3339, value)
}

func writeZip(destination string, files map[string]string) error {
	output, err := os.Create(destination)
	if err != nil {
		return err
	}
	archive := zip.NewWriter(output)
	for _, name := range sortedKeys(files) {
		info, err := os.Stat(files[name])
		if err != nil {
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = name
		header.Method = zip.Deflate
		header.SetModTime(archiveTime)
		writer, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}
		if err := copyFile(writer, files[name]); err != nil {
			return err
		}
	}
	if err := archive.Close(); err != nil {
		return err
	}
	return output.Close()
}

func writeTarGz(destination string, files map[string]string) error {
	output, err := os.Create(destination)
	if err != nil {
		return err
	}
	gzipWriter, err := gzip.NewWriterLevel(output, gzip.BestCompression)
	if err != nil {
		return err
	}
	gzipWriter.Header.ModTime = archiveTime
	gzipWriter.Header.OS = 255
	tarWriter := tar.NewWriter(gzipWriter)
	for _, name := range sortedKeys(files) {
		info, err := os.Stat(files[name])
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = name
		header.ModTime = archiveTime
		header.AccessTime = time.Time{}
		header.ChangeTime = time.Time{}
		header.Uid, header.Gid = 0, 0
		header.Uname, header.Gname = "", ""
		if name == "coldshelf" {
			header.Mode = 0o755
		} else {
			header.Mode = 0o644
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		if err := copyFile(tarWriter, files[name]); err != nil {
			return err
		}
	}
	if err := tarWriter.Close(); err != nil {
		return err
	}
	if err := gzipWriter.Close(); err != nil {
		return err
	}
	return output.Close()
}

func copyFile(destination io.Writer, source string) error {
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(destination, file)
	return err
}

func writeChecksums(destination string, files []string) error {
	sort.Strings(files)
	output, err := os.Create(destination)
	if err != nil {
		return err
	}
	defer output.Close()
	for _, name := range files {
		file, err := os.Open(name)
		if err != nil {
			return err
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, file)
		file.Close()
		if copyErr != nil {
			return copyErr
		}
		fmt.Fprintf(output, "%s  %s\n", hex.EncodeToString(hash.Sum(nil)), filepath.Base(name))
	}
	return nil
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func addTree(files map[string]string, archiveRoot, sourceRoot string) error {
	return filepath.WalkDir(sourceRoot, func(sourcePath string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(sourceRoot, sourcePath)
		if err != nil {
			return err
		}
		archivePath := filepath.ToSlash(filepath.Join(archiveRoot, relative))
		files[archivePath] = sourcePath
		return nil
	})
}

func commandOutput(name string, args ...string) string {
	command := exec.Command(name, args...)
	command.Env = append(os.Environ(), "GOOS="+runtime.GOOS)
	value, err := command.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(value))
}
