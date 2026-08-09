package scanner

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

const quickSampleSize int64 = 64 * 1024

func hashFile(ctx context.Context, path string, size int64, mode HashMode) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open for hashing: %w", err)
	}
	defer file.Close()

	hasher := sha256.New()
	if mode == HashFull {
		buffer := make([]byte, 256*1024)
		for {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			n, readErr := file.Read(buffer)
			if n > 0 {
				_, _ = hasher.Write(buffer[:n])
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				return "", fmt.Errorf("hash file: %w", readErr)
			}
		}
		return "sha256:" + hex.EncodeToString(hasher.Sum(nil)), nil
	}

	var sizeBytes [8]byte
	binary.LittleEndian.PutUint64(sizeBytes[:], uint64(size))
	_, _ = hasher.Write(sizeBytes[:])
	firstBytes := quickSampleSize
	if size < firstBytes {
		firstBytes = size
	}
	if _, err := io.CopyN(hasher, file, firstBytes); err != nil && err != io.EOF {
		return "", fmt.Errorf("hash first sample: %w", err)
	}
	if size > quickSampleSize {
		start := size - quickSampleSize
		if start < quickSampleSize {
			start = quickSampleSize
		}
		if _, err := file.Seek(start, io.SeekStart); err != nil {
			return "", fmt.Errorf("seek last sample: %w", err)
		}
		if _, err := io.Copy(hasher, file); err != nil {
			return "", fmt.Errorf("hash last sample: %w", err)
		}
	}
	return "quick:" + hex.EncodeToString(hasher.Sum(nil)), nil
}
