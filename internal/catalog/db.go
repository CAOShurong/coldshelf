package catalog

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const schemaVersion = 1

type Catalog struct {
	db   *sql.DB
	path string
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

	db, err := sql.Open("sqlite", abs)
	if err != nil {
		return nil, fmt.Errorf("open catalog: %w", err)
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
		"PRAGMA synchronous=NORMAL",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("configure catalog (%s): %w", pragma, err)
		}
	}

	c := &Catalog{db: db, path: abs}
	if err := c.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return c, nil
}

func (c *Catalog) Close() error { return c.db.Close() }
func (c *Catalog) Path() string { return c.path }

func (c *Catalog) migrate(ctx context.Context) error {
	statements := []string{
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

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	defer tx.Rollback()
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migrate catalog: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO metadata(key, value) VALUES('schema_version', ?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value`, fmt.Sprint(schemaVersion)); err != nil {
		return fmt.Errorf("record schema version: %w", err)
	}
	now := time.Now().Unix()
	if _, err := tx.ExecContext(ctx,
		`UPDATE snapshots SET status='failed', completed_at=?, failure='scan interrupted before completion'
		 WHERE status='scanning' AND started_at < ?`, now, now-int64((24*time.Hour)/time.Second)); err != nil {
		return fmt.Errorf("recover interrupted scans: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
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
	_ = json.Unmarshal([]byte(tags), &d.Tags)
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
