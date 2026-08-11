package catalog

import (
	"context"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"modernc.org/sqlite"
)

const (
	databaseSchemaVersion = 2
	exportFormatVersion   = 1
)

type Catalog struct {
	db           *sql.DB
	path         string
	identityPath string
	keepAlive    *sql.Conn
}

func Open(path string) (*Catalog, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("catalog path cannot be empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve catalog path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, fmt.Errorf("create catalog directory: %w", err)
	}
	if err := preflightCatalogVersion(abs); err != nil {
		return nil, err
	}
	c, err := openCatalogDatabase(catalogDSN(abs), abs, abs)
	if err != nil {
		return nil, err
	}
	if _, err := c.db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		c.db.Close()
		return nil, fmt.Errorf("configure catalog journal mode: %w", err)
	}
	if err := c.migrate(context.Background()); err != nil {
		c.db.Close()
		return nil, err
	}
	return c, nil
}

// OpenImportTarget opens a target without migrating it. ImportCatalog performs
// the migration and merge in the same transaction, so a rejected source cannot
// leave a v1 target partly upgraded.
func OpenImportTarget(path string) (*Catalog, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("catalog path cannot be empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve catalog path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, fmt.Errorf("create catalog directory: %w", err)
	}
	if err := preflightCatalogVersion(abs); err != nil {
		return nil, err
	}
	return openCatalogDatabase(catalogDSN(abs), abs, abs)
}

// OpenImportPreview creates a private, disposable copy of an existing target
// catalog. If the target does not exist, it creates only an empty disposable
// catalog. The returned Catalog can run the exact import transaction without
// creating, migrating, or otherwise changing the requested target path.
func OpenImportPreview(path string) (*Catalog, func() error, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil, errors.New("catalog path cannot be empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve catalog path: %w", err)
	}
	if err := preflightCatalogVersion(abs); err != nil {
		return nil, nil, err
	}
	memoryURI, err := previewMemoryURI()
	if err != nil {
		return nil, nil, err
	}
	info, statErr := os.Stat(abs)
	var backupConnection driver.Conn
	switch {
	case statErr == nil:
		if !info.Mode().IsRegular() {
			return nil, nil, errors.New("target catalog must be a regular file")
		}
		if err := requireStaticCatalog(abs, "target"); err != nil {
			return nil, nil, err
		}
		backupConnection, err = backupCatalogReadOnly(abs, memoryURI)
		if err != nil {
			return nil, nil, err
		}
	case errors.Is(statErr, os.ErrNotExist):
		// The shared in-memory catalog starts empty.
	default:
		return nil, nil, fmt.Errorf("inspect target catalog: %w", statErr)
	}
	if backupConnection != nil {
		defer backupConnection.Close()
	}
	preview, err := openCatalogDatabase(memoryURI, ":memory:", abs)
	if err != nil {
		return nil, nil, fmt.Errorf("open import preview: %w", err)
	}
	preview.keepAlive, err = preview.db.Conn(context.Background())
	if err != nil {
		preview.db.Close()
		return nil, nil, fmt.Errorf("keep import preview open: %w", err)
	}
	return preview, func() error { return nil }, nil
}

func backupCatalogReadOnly(sourcePath, targetURI string) (driver.Conn, error) {
	source, err := sql.Open("sqlite", readOnlyCatalogURI(sourcePath, true))
	if err != nil {
		return nil, fmt.Errorf("open target catalog read-only for preview: %w", err)
	}
	defer source.Close()
	source.SetMaxOpenConns(1)
	source.SetMaxIdleConns(1)
	connection, err := source.Conn(context.Background())
	if err != nil {
		return nil, fmt.Errorf("connect to target catalog for preview: %w", err)
	}
	defer connection.Close()
	type backuper interface {
		NewBackup(string) (*sqlite.Backup, error)
	}
	var destination driver.Conn
	if err := connection.Raw(func(driverConnection any) error {
		provider, ok := driverConnection.(backuper)
		if !ok {
			return errors.New("SQLite driver does not support online backup")
		}
		backup, err := provider.NewBackup(targetURI)
		if err != nil {
			return err
		}
		finished := false
		defer func() {
			if !finished {
				_ = backup.Finish()
			}
		}()
		for more := true; more; {
			more, err = backup.Step(-1)
			if err != nil {
				return err
			}
		}
		destination, err = backup.Commit()
		finished = true
		return err
	}); err != nil {
		return nil, fmt.Errorf("copy target catalog for preview: %w", err)
	}
	return destination, nil
}

func (c *Catalog) Close() error {
	var keepAliveErr error
	if c.keepAlive != nil {
		keepAliveErr = c.keepAlive.Close()
	}
	return errors.Join(keepAliveErr, c.db.Close())
}
func (c *Catalog) Path() string { return c.path }

func openCatalogDatabase(dsn, path, identityPath string) (*Catalog, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open catalog: %w", err)
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	return &Catalog{db: db, path: path, identityPath: identityPath}, nil
}

func catalogDSN(filename string) string {
	u := sqliteFileURL(filename)
	query := u.Query()
	query.Set("_busy_timeout", "5000")
	query.Set("_foreign_keys", "on")
	query.Set("_synchronous", "normal")
	u.RawQuery = query.Encode()
	return u.String()
}

func previewMemoryURI() (string, error) {
	value := make([]byte, 12)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("name import preview: %w", err)
	}
	u := &url.URL{Scheme: "file", Opaque: "coldshelf-import-preview-" + hex.EncodeToString(value)}
	query := u.Query()
	query.Set("mode", "memory")
	query.Set("cache", "shared")
	query.Set("_busy_timeout", "5000")
	query.Set("_foreign_keys", "on")
	query.Set("_synchronous", "normal")
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func sqliteFileURL(filename string) *url.URL {
	path := filepath.ToSlash(filename)
	if filepath.VolumeName(filename) != "" && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return &url.URL{Scheme: "file", Path: path}
}

func requireStaticCatalog(filename, role string) error {
	for _, suffix := range []string{"-wal", "-shm"} {
		_, err := os.Stat(filename + suffix)
		if err == nil {
			return fmt.Errorf("%s catalog has an active SQLite %s sidecar; close its ColdShelf process and checkpoint it before importing", role, suffix)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect %s catalog sidecar: %w", role, err)
		}
	}
	return nil
}

func preflightCatalogVersion(filename string) error {
	info, err := os.Stat(filename)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect catalog: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("catalog must be a regular file")
	}
	immutable := true
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, sidecarErr := os.Stat(filename + suffix); sidecarErr == nil {
			immutable = false
		} else if !errors.Is(sidecarErr, os.ErrNotExist) {
			return fmt.Errorf("inspect catalog sidecar: %w", sidecarErr)
		}
	}
	check, err := sql.Open("sqlite", readOnlyCatalogURI(filename, immutable))
	if err != nil {
		return fmt.Errorf("inspect catalog schema: %w", err)
	}
	defer check.Close()
	var hasMetadata int
	if err := check.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='metadata')`).Scan(&hasMetadata); err != nil {
		return fmt.Errorf("inspect catalog schema: %w", err)
	}
	if hasMetadata == 0 {
		return nil
	}
	var recorded string
	err = check.QueryRow(`SELECT value FROM metadata WHERE key='schema_version'`).Scan(&recorded)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read catalog schema version: %w", err)
	}
	version, err := strconv.Atoi(recorded)
	if err != nil || version < 1 {
		return fmt.Errorf("invalid catalog schema version %q", recorded)
	}
	if version > databaseSchemaVersion {
		return fmt.Errorf("catalog schema version %d is newer than supported version %d", version, databaseSchemaVersion)
	}
	return nil
}

var catalogSchemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS metadata (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
	`CREATE TABLE IF NOT EXISTS drives (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL COLLATE NOCASE,
			source_path TEXT NOT NULL DEFAULT '',
			location TEXT NOT NULL DEFAULT '',
			notes TEXT NOT NULL DEFAULT '',
			tags TEXT NOT NULL DEFAULT '[]',
			fingerprint TEXT NOT NULL DEFAULT '',
			latest_snapshot_id INTEGER,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE(name),
			FOREIGN KEY(latest_snapshot_id) REFERENCES snapshots(id)
		)`,
	`CREATE TABLE IF NOT EXISTS snapshots (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			drive_id TEXT NOT NULL,
			source_path TEXT NOT NULL,
			status TEXT NOT NULL CHECK(status IN ('scanning','complete','failed')),
			hash_mode TEXT NOT NULL DEFAULT 'none',
			file_count INTEGER NOT NULL DEFAULT 0,
			directory_count INTEGER NOT NULL DEFAULT 0,
			total_bytes INTEGER NOT NULL DEFAULT 0,
			error_count INTEGER NOT NULL DEFAULT 0,
			started_at INTEGER NOT NULL,
			completed_at INTEGER,
			failure TEXT NOT NULL DEFAULT '',
			FOREIGN KEY(drive_id) REFERENCES drives(id) ON DELETE CASCADE
		)`,
	`CREATE TABLE IF NOT EXISTS entries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			snapshot_id INTEGER NOT NULL,
			path TEXT NOT NULL,
			parent_path TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL,
			extension TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL CHECK(kind IN ('file','directory','symlink')),
			size INTEGER NOT NULL DEFAULT 0,
			modified_at INTEGER NOT NULL DEFAULT 0,
			hash TEXT NOT NULL DEFAULT '',
			hidden INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY(snapshot_id) REFERENCES snapshots(id) ON DELETE CASCADE,
			UNIQUE(snapshot_id, path)
		)`,
	`CREATE INDEX IF NOT EXISTS idx_entries_snapshot_parent ON entries(snapshot_id, parent_path, kind, name)`,
	`CREATE INDEX IF NOT EXISTS idx_entries_snapshot_extension ON entries(snapshot_id, extension)`,
	`CREATE INDEX IF NOT EXISTS idx_entries_hash ON entries(hash, size) WHERE hash <> ''`,
	`CREATE INDEX IF NOT EXISTS idx_snapshots_drive ON snapshots(drive_id, id DESC)`,
	`CREATE TABLE IF NOT EXISTS catalog_imports (
			digest TEXT PRIMARY KEY,
			target_snapshot_id INTEGER NOT NULL UNIQUE,
			trusted_full_hashes INTEGER NOT NULL DEFAULT 0 CHECK(trusted_full_hashes IN (0,1)),
			imported_at INTEGER NOT NULL,
			FOREIGN KEY(target_snapshot_id) REFERENCES snapshots(id) ON DELETE CASCADE
		)`,
	`CREATE VIRTUAL TABLE IF NOT EXISTS entries_fts USING fts5(
			name,
			path,
			content='entries',
			content_rowid='id',
			tokenize='unicode61 remove_diacritics 2'
		)`,
	`CREATE TRIGGER IF NOT EXISTS entries_ai AFTER INSERT ON entries BEGIN
			INSERT INTO entries_fts(rowid, name, path) VALUES (new.id, new.name, new.path);
		END`,
	`CREATE TRIGGER IF NOT EXISTS entries_ad AFTER DELETE ON entries BEGIN
			INSERT INTO entries_fts(entries_fts, rowid, name, path) VALUES ('delete', old.id, old.name, old.path);
		END`,
	`CREATE TRIGGER IF NOT EXISTS entries_au AFTER UPDATE ON entries BEGIN
			INSERT INTO entries_fts(entries_fts, rowid, name, path) VALUES ('delete', old.id, old.name, old.path);
			INSERT INTO entries_fts(rowid, name, path) VALUES (new.id, new.name, new.path);
		END`,
}

func (c *Catalog) migrate(ctx context.Context) error {

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	defer tx.Rollback()
	if err := migrateCatalogTx(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	return nil
}

func migrateCatalogTx(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, catalogSchemaStatements[0]); err != nil {
		return fmt.Errorf("create metadata table: %w", err)
	}
	var recorded string
	err := tx.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key='schema_version'`).Scan(&recorded)
	currentVersion := 0
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read schema version: %w", err)
	}
	if err == nil {
		currentVersion, err = strconv.Atoi(recorded)
		if err != nil || currentVersion < 1 {
			return fmt.Errorf("invalid catalog schema version %q", recorded)
		}
		if currentVersion > databaseSchemaVersion {
			return fmt.Errorf("catalog schema version %d is newer than supported version %d", currentVersion, databaseSchemaVersion)
		}
	}
	for _, statement := range catalogSchemaStatements[1:] {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migrate catalog: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO metadata(key, value) VALUES('schema_version', ?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value`, fmt.Sprint(databaseSchemaVersion)); err != nil {
		return fmt.Errorf("record schema version: %w", err)
	}
	now := time.Now().Unix()
	if _, err := tx.ExecContext(ctx,
		`UPDATE snapshots SET status='failed', completed_at=?, failure='scan interrupted before completion'
		 WHERE status='scanning' AND started_at < ?`, now, now-int64((24*time.Hour)/time.Second)); err != nil {
		return fmt.Errorf("recover interrupted scans: %w", err)
	}
	return nil
}

func (c *Catalog) CreateDrive(ctx context.Context, input NewDrive) (Drive, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return Drive{}, errors.New("drive name cannot be empty")
	}
	id, err := newDriveID()
	if err != nil {
		return Drive{}, err
	}
	tags, err := encodeTags(input.Tags)
	if err != nil {
		return Drive{}, err
	}
	now := time.Now().Unix()
	_, err = c.db.ExecContext(ctx, `INSERT INTO drives
		(id, name, source_path, location, notes, tags, fingerprint, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?)`, id, name, input.SourcePath, strings.TrimSpace(input.Location),
		strings.TrimSpace(input.Notes), tags, input.Fingerprint, now, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return Drive{}, fmt.Errorf("a drive named %q already exists", name)
		}
		return Drive{}, fmt.Errorf("create drive: %w", err)
	}
	return c.GetDrive(ctx, id)
}

func (c *Catalog) GetDrive(ctx context.Context, id string) (Drive, error) {
	row := c.db.QueryRowContext(ctx, driveSelect+` WHERE d.id = ?`, id)
	return scanDrive(row)
}

func (c *Catalog) ResolveDrive(ctx context.Context, idOrName string) (Drive, error) {
	row := c.db.QueryRowContext(ctx, driveSelect+` WHERE d.id = ? OR d.name = ? COLLATE NOCASE LIMIT 1`, idOrName, idOrName)
	return scanDrive(row)
}

func (c *Catalog) UpdateDrive(ctx context.Context, id string, patch DrivePatch) (Drive, error) {
	current, err := c.GetDrive(ctx, id)
	if err != nil {
		return Drive{}, err
	}
	if patch.Name != nil {
		current.Name = strings.TrimSpace(*patch.Name)
		if current.Name == "" {
			return Drive{}, errors.New("drive name cannot be empty")
		}
	}
	if patch.Location != nil {
		current.Location = strings.TrimSpace(*patch.Location)
	}
	if patch.Notes != nil {
		current.Notes = strings.TrimSpace(*patch.Notes)
	}
	if patch.SourcePath != nil {
		current.SourcePath = strings.TrimSpace(*patch.SourcePath)
	}
	if patch.Tags != nil {
		current.Tags = normalizeTags(*patch.Tags)
	}
	tags, _ := json.Marshal(current.Tags)
	_, err = c.db.ExecContext(ctx, `UPDATE drives SET name=?, source_path=?, location=?, notes=?, tags=?, updated_at=? WHERE id=?`,
		current.Name, current.SourcePath, current.Location, current.Notes, string(tags), time.Now().Unix(), id)
	if err != nil {
		return Drive{}, fmt.Errorf("update drive: %w", err)
	}
	return c.GetDrive(ctx, id)
}

func newDriveID() (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate drive id: %w", err)
	}
	return "drv_" + hex.EncodeToString(b), nil
}

func encodeTags(tags []string) (string, error) {
	b, err := json.Marshal(normalizeTags(tags))
	if err != nil {
		return "", fmt.Errorf("encode tags: %w", err)
	}
	return string(b), nil
}

func normalizeTags(tags []string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		key := strings.ToLower(tag)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, tag)
	}
	return out
}

type rowScanner interface {
	Scan(dest ...any) error
}

const driveSelect = `SELECT d.id, d.name, d.source_path, d.location, d.notes, d.tags,
	d.fingerprint, d.latest_snapshot_id, d.created_at, d.updated_at,
	COALESCE(s.file_count, 0), COALESCE(s.directory_count, 0), COALESCE(s.total_bytes, 0),
	COALESCE(s.completed_at, 0)
	FROM drives d LEFT JOIN snapshots s ON s.id = d.latest_snapshot_id`

func scanDrive(row rowScanner) (Drive, error) {
	var d Drive
	var tags string
	var latest sql.NullInt64
	var created, updated, scanned int64
	err := row.Scan(&d.ID, &d.Name, &d.SourcePath, &d.Location, &d.Notes, &tags,
		&d.Fingerprint, &latest, &created, &updated, &d.FileCount, &d.DirectoryCount,
		&d.TotalBytes, &scanned)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Drive{}, errors.New("drive not found")
		}
		return Drive{}, fmt.Errorf("read drive: %w", err)
	}
	if err := json.Unmarshal([]byte(tags), &d.Tags); err != nil {
		return Drive{}, fmt.Errorf("read drive tags: %w", err)
	}
	if d.Tags == nil {
		d.Tags = []string{}
	}
	if latest.Valid {
		d.LatestSnapshotID = &latest.Int64
	}
	d.CreatedAt = time.Unix(created, 0).UTC()
	d.UpdatedAt = time.Unix(updated, 0).UTC()
	if scanned > 0 {
		d.LastScannedAt = time.Unix(scanned, 0).UTC()
	}
	return d, nil
}
