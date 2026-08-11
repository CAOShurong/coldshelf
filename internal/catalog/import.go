package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"math"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	coldShelfDriveID     = regexp.MustCompile(`^drv_[0-9a-f]{12}$`)
	catalogFingerprintID = regexp.MustCompile(`^(quick|sha256):[0-9a-f]{64}$`)
)

type ImportCatalogOptions struct {
	RenameConflicts bool
	TrustFullHashes bool
	DryRun          bool
}

type ImportCatalogRename struct {
	DriveID string `json:"drive_id"`
	From    string `json:"from"`
	To      string `json:"to"`
}

type ImportCatalogResult struct {
	Source                     string                `json:"source"`
	SourceSchemaVersion        int                   `json:"source_schema_version"`
	DrivesAdded                int64                 `json:"drives_added"`
	DrivesMerged               int64                 `json:"drives_merged"`
	SnapshotsImported          int64                 `json:"snapshots_imported"`
	SnapshotsUpdated           int64                 `json:"snapshots_updated"`
	SnapshotsSkipped           int64                 `json:"snapshots_skipped"`
	IncompleteSnapshotsSkipped int64                 `json:"incomplete_snapshots_skipped"`
	EntriesImported            int64                 `json:"entries_imported"`
	FullHashesStripped         int64                 `json:"full_hashes_stripped"`
	FullHashesPreserved        int64                 `json:"full_hashes_preserved"`
	RenamedDrives              []ImportCatalogRename `json:"renamed_drives"`
	DryRun                     bool                  `json:"dry_run"`
}

// ImportCatalog merges every complete snapshot from another ColdShelf database.
// The source is opened read-only and held in one read transaction while the target
// merge is performed in one write transaction. Snapshot IDs are database-local and
// are therefore remapped; stable drive IDs are preserved.
func (c *Catalog) ImportCatalog(ctx context.Context, sourcePath string, options ImportCatalogOptions) (ImportCatalogResult, error) {
	result := ImportCatalogResult{RenamedDrives: []ImportCatalogRename{}, DryRun: options.DryRun}
	absSource, sourceInfo, err := catalogFile(sourcePath)
	if err != nil {
		return result, err
	}
	result.Source = absSource
	targetIdentity := c.identityPath
	if targetIdentity == "" {
		targetIdentity = c.path
	}
	targetInfo, err := os.Stat(targetIdentity)
	if err == nil && os.SameFile(sourceInfo, targetInfo) {
		return result, errors.New("source and target catalog are the same file")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return result, fmt.Errorf("inspect target catalog: %w", err)
	}
	if err := requireStaticCatalog(absSource, "source"); err != nil {
		return result, err
	}

	source, err := sql.Open("sqlite", readOnlyCatalogURI(absSource, true))
	if err != nil {
		return result, fmt.Errorf("open source catalog read-only: %w", err)
	}
	defer source.Close()
	source.SetMaxOpenConns(1)
	source.SetMaxIdleConns(1)
	for _, pragma := range []string{"PRAGMA query_only=ON", "PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=5000"} {
		if _, err := source.ExecContext(ctx, pragma); err != nil {
			return result, fmt.Errorf("configure source catalog (%s): %w", pragma, err)
		}
	}

	sourceTx, err := source.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, fmt.Errorf("begin source catalog snapshot: %w", err)
	}
	defer sourceTx.Rollback()
	result.SourceSchemaVersion, err = validateImportSource(ctx, sourceTx)
	if err != nil {
		return result, err
	}
	drives, err := sourceDrives(ctx, sourceTx)
	if err != nil {
		return result, err
	}

	targetTx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("begin catalog import: %w", err)
	}
	defer targetTx.Rollback()
	if err := migrateCatalogTx(ctx, targetTx); err != nil {
		return result, err
	}
	entryInsert, err := targetTx.PrepareContext(ctx, `INSERT INTO entries
		(snapshot_id, path, parent_path, name, extension, kind, size, modified_at, hash, hidden)
		VALUES(?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return result, fmt.Errorf("prepare imported entry writer: %w", err)
	}
	defer entryInsert.Close()

	for _, drive := range drives {
		added, rename, latestID, latestCompleted, err := ensureImportedDrive(ctx, targetTx, drive, options.RenameConflicts)
		if err != nil {
			return result, err
		}
		if added {
			result.DrivesAdded++
		} else {
			result.DrivesMerged++
		}
		if rename != nil {
			result.RenamedDrives = append(result.RenamedDrives, *rename)
		}

		snapshots, err := sourceSnapshots(ctx, sourceTx, drive.ID)
		if err != nil {
			return result, err
		}
		for _, snapshot := range snapshots {
			if snapshot.Status != "complete" {
				result.IncompleteSnapshotsSkipped++
				continue
			}
			digest, err := importSnapshotDigest(ctx, sourceTx, snapshot)
			if err != nil {
				return result, fmt.Errorf("validate source snapshot %d: %w", snapshot.ID, err)
			}
			var importedSnapshotID int64
			var importedTrust int64
			err = targetTx.QueryRowContext(ctx, `SELECT target_snapshot_id, trusted_full_hashes FROM catalog_imports WHERE digest=?`, digest).Scan(&importedSnapshotID, &importedTrust)
			if err == nil {
				if (importedTrust != 0) == options.TrustFullHashes {
					result.SnapshotsSkipped++
					continue
				}
				preserved, stripped, err := updateImportedHashPolicy(ctx, sourceTx, targetTx, snapshot, importedSnapshotID, options.TrustFullHashes)
				if err != nil {
					return result, err
				}
				if _, err := targetTx.ExecContext(ctx, `UPDATE catalog_imports SET trusted_full_hashes=?, imported_at=? WHERE digest=?`,
					boolInt(options.TrustFullHashes), time.Now().Unix(), digest); err != nil {
					return result, fmt.Errorf("record imported hash policy: %w", err)
				}
				result.SnapshotsUpdated++
				result.FullHashesPreserved += preserved
				result.FullHashesStripped += stripped
				continue
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return result, fmt.Errorf("check imported snapshot: %w", err)
			}

			inserted, err := targetTx.ExecContext(ctx, `INSERT INTO snapshots
				(drive_id, source_path, status, hash_mode, file_count, directory_count,
				total_bytes, error_count, started_at, completed_at, failure)
				VALUES(?,?,'complete',?,?,?,?,?,?,?,?)`, drive.ID, snapshot.SourcePath,
				snapshot.HashMode, snapshot.FileCount, snapshot.DirectoryCount, snapshot.TotalBytes,
				snapshot.ErrorCount, snapshot.StartedAt.Unix(), unixOrZero(snapshot.CompletedAt), snapshot.Failure)
			if err != nil {
				return result, fmt.Errorf("create imported snapshot: %w", err)
			}
			targetSnapshotID, err := inserted.LastInsertId()
			if err != nil {
				return result, fmt.Errorf("read imported snapshot id: %w", err)
			}
			entries, stripped, preserved, err := copyImportedEntries(ctx, sourceTx, entryInsert, snapshot, targetSnapshotID, options.TrustFullHashes)
			if err != nil {
				return result, err
			}
			if stripped > 0 {
				if _, err := targetTx.ExecContext(ctx, `UPDATE snapshots SET hash_mode='imported' WHERE id=?`, targetSnapshotID); err != nil {
					return result, fmt.Errorf("record stripped imported hashes: %w", err)
				}
			}
			if _, err := targetTx.ExecContext(ctx, `INSERT INTO catalog_imports(digest, target_snapshot_id, trusted_full_hashes, imported_at) VALUES(?,?,?,?)`,
				digest, targetSnapshotID, boolInt(options.TrustFullHashes), time.Now().Unix()); err != nil {
				return result, fmt.Errorf("record imported snapshot: %w", err)
			}
			result.SnapshotsImported++
			result.EntriesImported += entries
			result.FullHashesStripped += stripped
			result.FullHashesPreserved += preserved

			completed := unixOrZero(snapshot.CompletedAt)
			if latestID == 0 || completed >= latestCompleted {
				if _, err := targetTx.ExecContext(ctx, `UPDATE drives SET latest_snapshot_id=? WHERE id=?`, targetSnapshotID, drive.ID); err != nil {
					return result, fmt.Errorf("select latest imported snapshot: %w", err)
				}
				latestID = targetSnapshotID
				latestCompleted = completed
			}
		}
	}

	if options.DryRun {
		if err := targetTx.Rollback(); err != nil {
			return result, fmt.Errorf("finish import preview: %w", err)
		}
		return result, nil
	}
	if err := targetTx.Commit(); err != nil {
		return result, fmt.Errorf("commit catalog import: %w", err)
	}
	return result, nil
}

func catalogFile(value string) (string, os.FileInfo, error) {
	if strings.TrimSpace(value) == "" {
		return "", nil, errors.New("source catalog path cannot be empty")
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", nil, fmt.Errorf("resolve source catalog: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", nil, fmt.Errorf("inspect source catalog: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", nil, errors.New("source catalog must be a regular file")
	}
	return abs, info, nil
}

func readOnlyCatalogURI(filename string, immutable bool) string {
	u := sqliteFileURL(filename)
	query := u.Query()
	query.Set("mode", "ro")
	query.Set("cache", "private")
	if immutable {
		query.Set("immutable", "1")
	}
	u.RawQuery = query.Encode()
	return u.String()
}

func validateImportSource(ctx context.Context, tx *sql.Tx) (int, error) {
	var integrity string
	if err := tx.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&integrity); err != nil {
		return 0, fmt.Errorf("check source catalog: %w", err)
	}
	if integrity != "ok" {
		return 0, fmt.Errorf("source catalog integrity check failed: %s", integrity)
	}
	rows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return 0, fmt.Errorf("check source catalog relationships: %w", err)
	}
	hasForeignKeyFailure := rows.Next()
	if closeErr := rows.Close(); closeErr != nil {
		return 0, fmt.Errorf("finish source relationship check: %w", closeErr)
	}
	if hasForeignKeyFailure {
		return 0, errors.New("source catalog contains broken foreign-key relationships")
	}
	var rawVersion string
	if err := tx.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key='schema_version'`).Scan(&rawVersion); err != nil {
		return 0, fmt.Errorf("read source catalog schema version: %w", err)
	}
	version, err := strconv.Atoi(rawVersion)
	if err != nil || version < 1 {
		return 0, fmt.Errorf("invalid source catalog schema version %q", rawVersion)
	}
	if version > databaseSchemaVersion {
		return 0, fmt.Errorf("source catalog schema version %d is newer than supported version %d", version, databaseSchemaVersion)
	}
	return version, nil
}

func sourceDrives(ctx context.Context, tx *sql.Tx) ([]Drive, error) {
	rows, err := tx.QueryContext(ctx, driveSelect+` ORDER BY d.id`)
	if err != nil {
		return nil, fmt.Errorf("list source drives: %w", err)
	}
	defer rows.Close()
	drives := make([]Drive, 0)
	for rows.Next() {
		drive, err := scanDrive(rows)
		if err != nil {
			return nil, fmt.Errorf("read source drive: %w", err)
		}
		if err := validateImportedDrive(drive); err != nil {
			return nil, err
		}
		drives = append(drives, drive)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list source drives: %w", err)
	}
	return drives, nil
}

func sourceSnapshots(ctx context.Context, tx *sql.Tx, driveID string) ([]Snapshot, error) {
	rows, err := tx.QueryContext(ctx, snapshotSelect+` WHERE drive_id=? ORDER BY id`, driveID)
	if err != nil {
		return nil, fmt.Errorf("list source snapshots for %s: %w", driveID, err)
	}
	defer rows.Close()
	snapshots := make([]Snapshot, 0)
	for rows.Next() {
		snapshot, err := scanSnapshot(rows)
		if err != nil {
			return nil, fmt.Errorf("read source snapshot: %w", err)
		}
		snapshots = append(snapshots, snapshot)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list source snapshots: %w", err)
	}
	return snapshots, nil
}

func validateImportedDrive(drive Drive) error {
	if !coldShelfDriveID.MatchString(drive.ID) {
		return fmt.Errorf("source catalog contains invalid drive id %q", drive.ID)
	}
	if strings.TrimSpace(drive.Name) == "" || strings.ContainsRune(drive.Name, 0) {
		return fmt.Errorf("source drive %s has an invalid name", drive.ID)
	}
	for _, value := range []string{drive.SourcePath, drive.Location, drive.Notes, drive.Fingerprint} {
		if strings.ContainsRune(value, 0) {
			return fmt.Errorf("source drive %s contains NUL metadata", drive.ID)
		}
	}
	for _, tag := range drive.Tags {
		if strings.ContainsRune(tag, 0) {
			return fmt.Errorf("source drive %s contains a NUL tag", drive.ID)
		}
	}
	return nil
}

func ensureImportedDrive(ctx context.Context, tx *sql.Tx, drive Drive, renameConflicts bool) (bool, *ImportCatalogRename, int64, int64, error) {
	var existingName string
	var latest sql.NullInt64
	var completed int64
	err := tx.QueryRowContext(ctx, `SELECT d.name, d.latest_snapshot_id, COALESCE(s.completed_at,0)
		FROM drives d LEFT JOIN snapshots s ON s.id=d.latest_snapshot_id WHERE d.id=?`, drive.ID).Scan(&existingName, &latest, &completed)
	if err == nil {
		return false, nil, latest.Int64, completed, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, nil, 0, 0, fmt.Errorf("check target drive %s: %w", drive.ID, err)
	}

	name := drive.Name
	var conflictingID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM drives WHERE name=? COLLATE NOCASE`, name).Scan(&conflictingID)
	var renamed *ImportCatalogRename
	if err == nil {
		if !renameConflicts {
			return false, nil, 0, 0, fmt.Errorf("drive name %q already belongs to %s; rerun with --rename-conflicts to import %s under a distinct name", name, conflictingID, drive.ID)
		}
		name, err = availableImportedName(ctx, tx, name, drive.ID)
		if err != nil {
			return false, nil, 0, 0, err
		}
		renamed = &ImportCatalogRename{DriveID: drive.ID, From: drive.Name, To: name}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return false, nil, 0, 0, fmt.Errorf("check target drive name: %w", err)
	}
	tags, err := encodeTags(drive.Tags)
	if err != nil {
		return false, nil, 0, 0, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO drives
		(id, name, source_path, location, notes, tags, fingerprint, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?)`, drive.ID, name, drive.SourcePath, drive.Location, drive.Notes,
		tags, drive.Fingerprint, drive.CreatedAt.Unix(), drive.UpdatedAt.Unix())
	if err != nil {
		return false, nil, 0, 0, fmt.Errorf("create imported drive %s: %w", drive.ID, err)
	}
	return true, renamed, 0, 0, nil
}

func availableImportedName(ctx context.Context, tx *sql.Tx, original, driveID string) (string, error) {
	suffix := strings.TrimPrefix(driveID, "drv_")
	if len(suffix) > 6 {
		suffix = suffix[len(suffix)-6:]
	}
	base := fmt.Sprintf("%s (imported %s)", original, suffix)
	for attempt := 1; attempt <= 10_000; attempt++ {
		candidate := base
		if attempt > 1 {
			candidate = fmt.Sprintf("%s %d", base, attempt)
		}
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM drives WHERE name=? COLLATE NOCASE)`, candidate).Scan(&exists); err != nil {
			return "", fmt.Errorf("find an available imported drive name: %w", err)
		}
		if exists == 0 {
			return candidate, nil
		}
	}
	return "", errors.New("could not find an available imported drive name")
}

func importSnapshotDigest(ctx context.Context, tx *sql.Tx, snapshot Snapshot) (string, error) {
	if err := validateImportedSnapshot(snapshot); err != nil {
		return "", err
	}
	hasher := sha256.New()
	digestInt(hasher, snapshot.ID)
	digestString(hasher, snapshot.DriveID)
	digestString(hasher, snapshot.SourcePath)
	digestString(hasher, snapshot.Status)
	digestString(hasher, snapshot.HashMode)
	digestInt(hasher, snapshot.FileCount)
	digestInt(hasher, snapshot.DirectoryCount)
	digestInt(hasher, snapshot.TotalBytes)
	digestInt(hasher, snapshot.ErrorCount)
	digestInt(hasher, snapshot.StartedAt.Unix())
	digestInt(hasher, unixOrZero(snapshot.CompletedAt))
	digestString(hasher, snapshot.Failure)

	rows, err := sourceEntryRows(ctx, tx, snapshot.ID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var files, directories, bytes int64
	for rows.Next() {
		entry, modified, hidden, err := scanSourceEntry(rows)
		if err != nil {
			return "", err
		}
		if err := validateImportedEntry(entry, hidden); err != nil {
			return "", err
		}
		digestString(hasher, entry.Path)
		digestString(hasher, entry.ParentPath)
		digestString(hasher, entry.Name)
		digestString(hasher, entry.Extension)
		digestString(hasher, entry.Kind)
		digestInt(hasher, entry.Size)
		digestInt(hasher, modified)
		digestString(hasher, entry.Hash)
		digestInt(hasher, hidden)
		switch entry.Kind {
		case "file":
			files++
			if entry.Size > math.MaxInt64-bytes {
				return "", fmt.Errorf("source snapshot %d byte total overflows int64", snapshot.ID)
			}
			bytes += entry.Size
		case "directory":
			directories++
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("read source snapshot entries: %w", err)
	}
	if files != snapshot.FileCount || directories != snapshot.DirectoryCount || bytes != snapshot.TotalBytes {
		return "", fmt.Errorf("declared counts do not match entries (files %d/%d, directories %d/%d, bytes %d/%d)",
			snapshot.FileCount, files, snapshot.DirectoryCount, directories, snapshot.TotalBytes, bytes)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func validateImportedSnapshot(snapshot Snapshot) error {
	if snapshot.FileCount < 0 || snapshot.DirectoryCount < 0 || snapshot.TotalBytes < 0 || snapshot.ErrorCount < 0 {
		return fmt.Errorf("source snapshot %d contains negative counts", snapshot.ID)
	}
	if snapshot.StartedAt.IsZero() || snapshot.CompletedAt.IsZero() || snapshot.CompletedAt.Before(snapshot.StartedAt) {
		return fmt.Errorf("source snapshot %d contains invalid timestamps", snapshot.ID)
	}
	if snapshot.HashMode != "none" && snapshot.HashMode != "quick" && snapshot.HashMode != "full" && snapshot.HashMode != "imported" && snapshot.HashMode != "demo" {
		return fmt.Errorf("source snapshot %d has invalid hash mode %q", snapshot.ID, snapshot.HashMode)
	}
	if strings.ContainsRune(snapshot.SourcePath, 0) || strings.ContainsRune(snapshot.Failure, 0) {
		return fmt.Errorf("source snapshot %d contains NUL metadata", snapshot.ID)
	}
	return nil
}

func copyImportedEntries(ctx context.Context, source *sql.Tx, insert *sql.Stmt, snapshot Snapshot, targetSnapshotID int64, trustFullHashes bool) (int64, int64, int64, error) {
	rows, err := sourceEntryRows(ctx, source, snapshot.ID)
	if err != nil {
		return 0, 0, 0, err
	}
	defer rows.Close()
	var entries, stripped, preserved int64
	for rows.Next() {
		entry, modified, hidden, err := scanSourceEntry(rows)
		if err != nil {
			return 0, 0, 0, err
		}
		hashValue := entry.Hash
		if !trustFullHashes && strings.HasPrefix(strings.ToLower(hashValue), "sha256:") {
			hashValue = ""
			stripped++
		} else if trustFullHashes && strings.HasPrefix(hashValue, "sha256:") {
			preserved++
		}
		if _, err := insert.ExecContext(ctx, targetSnapshotID, entry.Path, entry.ParentPath,
			entry.Name, entry.Extension, entry.Kind, entry.Size, modified, hashValue, hidden); err != nil {
			return 0, 0, 0, fmt.Errorf("write imported entry %q: %w", entry.Path, err)
		}
		entries++
	}
	if err := rows.Err(); err != nil {
		return 0, 0, 0, fmt.Errorf("copy source snapshot entries: %w", err)
	}
	return entries, stripped, preserved, nil
}

func updateImportedHashPolicy(ctx context.Context, source, target *sql.Tx, snapshot Snapshot, targetSnapshotID int64, trustFullHashes bool) (int64, int64, error) {
	rows, err := sourceEntryRows(ctx, source, snapshot.ID)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()
	update, err := target.PrepareContext(ctx, `UPDATE entries SET hash=? WHERE snapshot_id=? AND path=?`)
	if err != nil {
		return 0, 0, fmt.Errorf("prepare imported hash policy update: %w", err)
	}
	defer update.Close()
	var entries, preserved, stripped int64
	for rows.Next() {
		entry, _, _, err := scanSourceEntry(rows)
		if err != nil {
			return 0, 0, err
		}
		hashValue := entry.Hash
		if strings.HasPrefix(hashValue, "sha256:") {
			if trustFullHashes {
				preserved++
			} else {
				hashValue = ""
				stripped++
			}
		}
		written, err := update.ExecContext(ctx, hashValue, targetSnapshotID, entry.Path)
		if err != nil {
			return 0, 0, fmt.Errorf("update imported fingerprint for %q: %w", entry.Path, err)
		}
		changed, err := written.RowsAffected()
		if err != nil || changed != 1 {
			return 0, 0, fmt.Errorf("imported snapshot %d no longer matches source path %q", targetSnapshotID, entry.Path)
		}
		entries++
	}
	if err := rows.Err(); err != nil {
		return 0, 0, fmt.Errorf("read source snapshot entries: %w", err)
	}
	var targetEntries int64
	if err := target.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries WHERE snapshot_id=?`, targetSnapshotID).Scan(&targetEntries); err != nil {
		return 0, 0, fmt.Errorf("count imported snapshot entries: %w", err)
	}
	if targetEntries != entries {
		return 0, 0, fmt.Errorf("imported snapshot %d entry count changed from %d to %d", targetSnapshotID, entries, targetEntries)
	}
	hashMode := snapshot.HashMode
	if !trustFullHashes && stripped > 0 {
		hashMode = "imported"
	}
	if _, err := target.ExecContext(ctx, `UPDATE snapshots SET hash_mode=? WHERE id=?`, hashMode, targetSnapshotID); err != nil {
		return 0, 0, fmt.Errorf("update imported snapshot hash mode: %w", err)
	}
	return preserved, stripped, nil
}

func boolInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func sourceEntryRows(ctx context.Context, tx *sql.Tx, snapshotID int64) (*sql.Rows, error) {
	rows, err := tx.QueryContext(ctx, `SELECT path, parent_path, name, extension, kind, size, modified_at, hash, hidden
		FROM entries WHERE snapshot_id=? ORDER BY path`, snapshotID)
	if err != nil {
		return nil, fmt.Errorf("read source snapshot %d entries: %w", snapshotID, err)
	}
	return rows, nil
}

func scanSourceEntry(row rowScanner) (Entry, int64, int64, error) {
	var entry Entry
	var modified, hidden int64
	if err := row.Scan(&entry.Path, &entry.ParentPath, &entry.Name, &entry.Extension,
		&entry.Kind, &entry.Size, &modified, &entry.Hash, &hidden); err != nil {
		return Entry{}, 0, 0, fmt.Errorf("read source entry: %w", err)
	}
	if modified != 0 {
		entry.ModifiedAt = time.Unix(modified, 0).UTC()
	}
	entry.Hidden = hidden != 0
	return entry, modified, hidden, nil
}

func validateImportedEntry(entry Entry, hidden int64) error {
	if entry.Path == "" || strings.ContainsAny(entry.Path, "\\\x00") || strings.HasPrefix(entry.Path, "/") || strings.HasSuffix(entry.Path, "/") {
		return fmt.Errorf("source catalog contains invalid entry path %q", entry.Path)
	}
	for _, segment := range strings.Split(entry.Path, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("source catalog contains unsafe entry path %q", entry.Path)
		}
	}
	if entry.Name == "" || strings.ContainsAny(entry.Name, "/\\\x00") || pathpkg.Base(entry.Path) != entry.Name {
		return fmt.Errorf("source entry %q has inconsistent name %q", entry.Path, entry.Name)
	}
	expectedParent := pathpkg.Dir(entry.Path)
	if expectedParent == "." {
		expectedParent = ""
	}
	if entry.ParentPath != expectedParent {
		return fmt.Errorf("source entry %q has inconsistent parent %q", entry.Path, entry.ParentPath)
	}
	if strings.ContainsAny(entry.Extension, "/\\\x00") || strings.ContainsRune(entry.Hash, 0) || len(entry.Hash) > 256 {
		return fmt.Errorf("source entry %q contains invalid metadata", entry.Path)
	}
	if entry.Hash != "" && !catalogFingerprintID.MatchString(entry.Hash) {
		return fmt.Errorf("source entry %q has invalid fingerprint %q", entry.Path, entry.Hash)
	}
	if entry.Size < 0 {
		return fmt.Errorf("source entry %q has negative size", entry.Path)
	}
	if entry.Kind != "file" && entry.Kind != "directory" && entry.Kind != "symlink" {
		return fmt.Errorf("source entry %q has invalid kind %q", entry.Path, entry.Kind)
	}
	if hidden != 0 && hidden != 1 {
		return fmt.Errorf("source entry %q has invalid hidden flag", entry.Path)
	}
	return nil
}

func digestString(writer hash.Hash, value string) {
	var size [8]byte
	binary.LittleEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = writer.Write(size[:])
	_, _ = writer.Write([]byte(value))
}

func digestInt(writer hash.Hash, value int64) {
	var encoded [8]byte
	binary.LittleEndian.PutUint64(encoded[:], uint64(value))
	_, _ = writer.Write(encoded[:])
}

func unixOrZero(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.Unix()
}
