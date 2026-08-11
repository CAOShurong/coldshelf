package catalog

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (c *Catalog) ListDrives(ctx context.Context) ([]Drive, error) {
	rows, err := c.db.QueryContext(ctx, driveSelect+` ORDER BY d.name COLLATE NOCASE`)
	if err != nil {
		return nil, fmt.Errorf("list drives: %w", err)
	}
	defer rows.Close()
	drives := make([]Drive, 0)
	for rows.Next() {
		d, err := scanDrive(rows)
		if err != nil {
			return nil, err
		}
		drives = append(drives, d)
	}
	return drives, rows.Err()
}

func (c *Catalog) GetSnapshot(ctx context.Context, id int64) (Snapshot, error) {
	row := c.db.QueryRowContext(ctx, snapshotSelect+` WHERE id=?`, id)
	return scanSnapshot(row)
}

func (c *Catalog) ListSnapshots(ctx context.Context, driveID string) ([]Snapshot, error) {
	rows, err := c.db.QueryContext(ctx, snapshotSelect+` WHERE drive_id=?
		ORDER BY COALESCE(completed_at, started_at) DESC, id DESC`, driveID)
	if err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}
	defer rows.Close()
	out := make([]Snapshot, 0)
	for rows.Next() {
		s, err := scanSnapshot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

const snapshotSelect = `SELECT id, drive_id, source_path, status, hash_mode, file_count,
	directory_count, total_bytes, error_count, started_at, COALESCE(completed_at,0), failure
	FROM snapshots`

func scanSnapshot(row rowScanner) (Snapshot, error) {
	var s Snapshot
	var started, completed int64
	if err := row.Scan(&s.ID, &s.DriveID, &s.SourcePath, &s.Status, &s.HashMode,
		&s.FileCount, &s.DirectoryCount, &s.TotalBytes, &s.ErrorCount, &started, &completed, &s.Failure); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Snapshot{}, errors.New("snapshot not found")
		}
		return Snapshot{}, fmt.Errorf("read snapshot: %w", err)
	}
	s.StartedAt = time.Unix(started, 0).UTC()
	if completed > 0 {
		s.CompletedAt = time.Unix(completed, 0).UTC()
	}
	return s, nil
}

func (c *Catalog) ListEntries(ctx context.Context, snapshotID int64, parent string, limit, offset int) ([]Entry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 250
	}
	rows, err := c.db.QueryContext(ctx, `SELECT id, snapshot_id, path, parent_path, name, extension,
		kind, size, modified_at, hash, hidden FROM entries
		WHERE snapshot_id=? AND parent_path=?
		ORDER BY CASE kind WHEN 'directory' THEN 0 ELSE 1 END, name COLLATE NOCASE LIMIT ? OFFSET ?`,
		snapshotID, normalizeCatalogPath(parent), limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list entries: %w", err)
	}
	defer rows.Close()
	out := make([]Entry, 0)
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func scanEntry(row rowScanner) (Entry, error) {
	var e Entry
	var modified int64
	var hidden int
	if err := row.Scan(&e.ID, &e.SnapshotID, &e.Path, &e.ParentPath, &e.Name,
		&e.Extension, &e.Kind, &e.Size, &modified, &e.Hash, &hidden); err != nil {
		return Entry{}, fmt.Errorf("read entry: %w", err)
	}
	if modified > 0 {
		e.ModifiedAt = time.Unix(modified, 0).UTC()
	}
	e.Hidden = hidden != 0
	return e, nil
}

func (c *Catalog) Search(ctx context.Context, query, driveID string, limit int) ([]SearchHit, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	fts := makeFTSQuery(query)
	if fts == "" {
		return []SearchHit{}, nil
	}
	statement := `SELECT e.id, e.snapshot_id, e.path, e.parent_path, e.name, e.extension,
		e.kind, e.size, e.modified_at, e.hash, e.hidden, d.id, d.name, d.location
		FROM entries_fts
		JOIN entries e ON e.id=entries_fts.rowid
		JOIN drives d ON d.latest_snapshot_id=e.snapshot_id
		WHERE entries_fts MATCH ?`
	args := []any{fts}
	if driveID != "" {
		statement += ` AND d.id=?`
		args = append(args, driveID)
	}
	statement += ` ORDER BY bm25(entries_fts, 8.0, 1.0), e.name COLLATE NOCASE LIMIT ?`
	args = append(args, limit)
	rows, err := c.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("search catalog: %w", err)
	}
	defer rows.Close()
	out := make([]SearchHit, 0)
	for rows.Next() {
		var h SearchHit
		var modified int64
		var hidden int
		if err := rows.Scan(&h.ID, &h.SnapshotID, &h.Path, &h.ParentPath, &h.Name,
			&h.Extension, &h.Kind, &h.Size, &modified, &h.Hash, &hidden,
			&h.DriveID, &h.DriveName, &h.Location); err != nil {
			return nil, fmt.Errorf("read search result: %w", err)
		}
		if modified > 0 {
			h.ModifiedAt = time.Unix(modified, 0).UTC()
		}
		h.Hidden = hidden != 0
		out = append(out, h)
	}
	return out, rows.Err()
}

func makeFTSQuery(query string) string {
	parts := strings.Fields(strings.TrimSpace(query))
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Trim(part, `"'`)
		part = strings.ReplaceAll(part, `"`, `""`)
		if part != "" {
			out = append(out, `"`+part+`"*`)
		}
	}
	return strings.Join(out, " AND ")
}

func (c *Catalog) Stats(ctx context.Context) (Stats, error) {
	var stats Stats
	row := c.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM drives),
		(SELECT COUNT(*) FROM snapshots WHERE status='complete'),
		COALESCE(SUM(s.file_count),0), COALESCE(SUM(s.directory_count),0),
		COALESCE(SUM(s.total_bytes),0),
		COALESCE((SELECT COUNT(*) FROM entries e JOIN drives d ON d.latest_snapshot_id=e.snapshot_id
			WHERE e.kind='file' AND e.hash<>''),0)
		FROM drives d LEFT JOIN snapshots s ON s.id=d.latest_snapshot_id`)
	if err := row.Scan(&stats.DriveCount, &stats.SnapshotCount, &stats.FileCount,
		&stats.DirectoryCount, &stats.TotalBytes, &stats.HashedFileCount); err != nil {
		return Stats{}, fmt.Errorf("read stats: %w", err)
	}
	return stats, nil
}

func (c *Catalog) ExtensionStats(ctx context.Context, driveID string, limit int) ([]ExtensionStat, error) {
	if limit <= 0 || limit > 100 {
		limit = 12
	}
	statement := `SELECT CASE WHEN e.extension='' THEN '(none)' ELSE e.extension END,
		COUNT(*), COALESCE(SUM(e.size),0)
		FROM entries e JOIN drives d ON d.latest_snapshot_id=e.snapshot_id
		WHERE e.kind='file'`
	args := []any{}
	if driveID != "" {
		statement += ` AND d.id=?`
		args = append(args, driveID)
	}
	statement += ` GROUP BY e.extension ORDER BY SUM(e.size) DESC LIMIT ?`
	args = append(args, limit)
	rows, err := c.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("extension stats: %w", err)
	}
	defer rows.Close()
	out := make([]ExtensionStat, 0)
	for rows.Next() {
		var item ExtensionStat
		if err := rows.Scan(&item.Extension, &item.Count, &item.Bytes); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (c *Catalog) Diff(ctx context.Context, driveID string, fromID, toID int64, limit int) ([]DiffEntry, error) {
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	for _, id := range []int64{fromID, toID} {
		s, err := c.GetSnapshot(ctx, id)
		if err != nil {
			return nil, err
		}
		if s.DriveID != driveID || s.Status != "complete" {
			return nil, errors.New("snapshots must be complete and belong to the requested drive")
		}
	}
	rows, err := c.db.QueryContext(ctx, `WITH old AS (
		SELECT path, kind, size, modified_at, hash FROM entries WHERE snapshot_id=?
	), new AS (
		SELECT path, kind, size, modified_at, hash FROM entries WHERE snapshot_id=?
	), changes AS (
		SELECT 'removed' AS change, old.path, old.kind, old.size AS old_size, NULL AS new_size,
			old.modified_at AS old_modified, NULL AS new_modified
		FROM old LEFT JOIN new ON new.path=old.path WHERE new.path IS NULL
		UNION ALL
		SELECT 'added', new.path, new.kind, NULL, new.size, NULL, new.modified_at
		FROM new LEFT JOIN old ON old.path=new.path WHERE old.path IS NULL
		UNION ALL
		SELECT 'changed', new.path, new.kind, old.size, new.size, old.modified_at, new.modified_at
		FROM new JOIN old ON old.path=new.path
		WHERE old.kind<>new.kind OR old.size<>new.size OR old.modified_at<>new.modified_at
			OR (old.hash LIKE 'sha256:%' AND new.hash LIKE 'sha256:%' AND old.hash<>new.hash)
			OR (old.hash LIKE 'quick:%' AND new.hash LIKE 'quick:%' AND old.hash<>new.hash)
	) SELECT change, path, kind, old_size, new_size, old_modified, new_modified
	FROM changes ORDER BY path COLLATE NOCASE LIMIT ?`, fromID, toID, limit)
	if err != nil {
		return nil, fmt.Errorf("compare snapshots: %w", err)
	}
	defer rows.Close()
	out := make([]DiffEntry, 0)
	for rows.Next() {
		var item DiffEntry
		var oldSize, newSize, oldMod, newMod sql.NullInt64
		if err := rows.Scan(&item.Change, &item.Path, &item.Kind, &oldSize, &newSize, &oldMod, &newMod); err != nil {
			return nil, err
		}
		item.OldSize = nullIntPointer(oldSize)
		item.NewSize = nullIntPointer(newSize)
		item.OldModified = nullIntPointer(oldMod)
		item.NewModified = nullIntPointer(newMod)
		out = append(out, item)
	}
	return out, rows.Err()
}

func nullIntPointer(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	v := value.Int64
	return &v
}

func (c *Catalog) Duplicates(ctx context.Context, driveID string, limit int) ([]DuplicateGroup, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	statement := `SELECT e.hash, e.size, d.id, d.name, e.path
		FROM entries e JOIN drives d ON d.latest_snapshot_id=e.snapshot_id
		JOIN (SELECT e2.hash, e2.size FROM entries e2 JOIN drives d2 ON d2.latest_snapshot_id=e2.snapshot_id
			WHERE e2.kind='file' AND e2.hash LIKE 'sha256:%'`
	args := []any{}
	if driveID != "" {
		statement += ` AND d2.id=?`
		args = append(args, driveID)
	}
	statement += ` GROUP BY e2.hash, e2.size HAVING COUNT(*)>1 ORDER BY e2.size*COUNT(*) DESC LIMIT ?) dup
		ON dup.hash=e.hash AND dup.size=e.size WHERE e.kind='file'`
	args = append(args, limit)
	if driveID != "" {
		statement += ` AND d.id=?`
		args = append(args, driveID)
	}
	statement += ` ORDER BY e.size DESC, e.hash, d.name, e.path`
	rows, err := c.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("find duplicates: %w", err)
	}
	defer rows.Close()
	groups := make(map[string]*DuplicateGroup)
	order := make([]string, 0)
	for rows.Next() {
		var hash, driveIDValue, driveName, path string
		var size int64
		if err := rows.Scan(&hash, &size, &driveIDValue, &driveName, &path); err != nil {
			return nil, err
		}
		key := hash + ":" + strconv.FormatInt(size, 10)
		group := groups[key]
		if group == nil {
			group = &DuplicateGroup{Hash: hash, Size: size, Files: []DuplicateFile{}}
			groups[key] = group
			order = append(order, key)
		}
		group.Files = append(group.Files, DuplicateFile{DriveID: driveIDValue, DriveName: driveName, Path: path})
	}
	out := make([]DuplicateGroup, 0, len(order))
	for _, key := range order {
		out = append(out, *groups[key])
	}
	return out, rows.Err()
}

func (c *Catalog) ExportJSON(ctx context.Context, w io.Writer) error {
	drives, err := c.ListDrives(ctx)
	if err != nil {
		return err
	}
	type exportDrive struct {
		Drive
		Snapshots []Snapshot `json:"snapshots"`
		Entries   []Entry    `json:"latest_entries"`
	}
	export := struct {
		Version    int           `json:"version"`
		ExportedAt time.Time     `json:"exported_at"`
		Drives     []exportDrive `json:"drives"`
	}{Version: exportFormatVersion, ExportedAt: time.Now().UTC(), Drives: []exportDrive{}}
	for _, drive := range drives {
		item := exportDrive{Drive: drive, Entries: []Entry{}}
		item.Snapshots, err = c.ListSnapshots(ctx, drive.ID)
		if err != nil {
			return err
		}
		if drive.LatestSnapshotID != nil {
			item.Entries, err = c.allEntries(ctx, *drive.LatestSnapshotID)
			if err != nil {
				return err
			}
		}
		export.Drives = append(export.Drives, item)
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(export)
}

func (c *Catalog) ExportCSV(ctx context.Context, w io.Writer) error {
	writer := csv.NewWriter(w)
	if err := writer.Write([]string{"drive_id", "drive_name", "location", "path", "kind", "size", "modified_at", "hash"}); err != nil {
		return err
	}
	rows, err := c.db.QueryContext(ctx, `SELECT d.id, d.name, d.location, e.path, e.kind, e.size,
		e.modified_at, e.hash FROM entries e JOIN drives d ON d.latest_snapshot_id=e.snapshot_id
		ORDER BY d.name COLLATE NOCASE, e.path COLLATE NOCASE`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var driveID, driveName, location, path, kind, hash string
		var size, modified int64
		if err := rows.Scan(&driveID, &driveName, &location, &path, &kind, &size, &modified, &hash); err != nil {
			return err
		}
		if err := writer.Write([]string{driveID, driveName, location, path, kind,
			strconv.FormatInt(size, 10), strconv.FormatInt(modified, 10), hash}); err != nil {
			return err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return err
	}
	return rows.Err()
}

func (c *Catalog) allEntries(ctx context.Context, snapshotID int64) ([]Entry, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT id, snapshot_id, path, parent_path, name, extension,
		kind, size, modified_at, hash, hidden FROM entries WHERE snapshot_id=? ORDER BY path`, snapshotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Entry, 0)
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func normalizeCatalogPath(path string) string {
	path = strings.ReplaceAll(strings.TrimSpace(path), `\`, "/")
	path = strings.Trim(path, "/")
	return path
}

func SortDrivesByRecent(drives []Drive) {
	sort.SliceStable(drives, func(i, j int) bool {
		return drives[i].LastScannedAt.After(drives[j].LastScannedAt)
	})
}
