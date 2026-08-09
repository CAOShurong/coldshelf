package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"
)

type SnapshotWriter struct {
	catalog        *Catalog
	ctx            context.Context
	tx             *sql.Tx
	insert         *sql.Stmt
	snapshotID     int64
	driveID        string
	sourcePath     string
	hashMode       string
	fileCount      int64
	directoryCount int64
	totalBytes     int64
	errorCount     int64
	closed         bool
	mu             sync.Mutex
}

func (c *Catalog) StartSnapshot(ctx context.Context, driveID, sourcePath, hashMode string) (*SnapshotWriter, error) {
	if _, err := c.GetDrive(ctx, driveID); err != nil {
		return nil, err
	}
	result, err := c.db.ExecContext(ctx, `INSERT INTO snapshots
		(drive_id, source_path, status, hash_mode, started_at) VALUES(?,?, 'scanning', ?, ?)`,
		driveID, sourcePath, hashMode, time.Now().Unix())
	if err != nil {
		return nil, fmt.Errorf("start snapshot: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("read snapshot id: %w", err)
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		c.markSnapshotFailed(context.Background(), id, err)
		return nil, fmt.Errorf("begin snapshot write: %w", err)
	}
	statement, err := tx.PrepareContext(ctx, `INSERT INTO entries
		(snapshot_id, path, parent_path, name, extension, kind, size, modified_at, hash, hidden)
		VALUES(?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		tx.Rollback()
		c.markSnapshotFailed(context.Background(), id, err)
		return nil, fmt.Errorf("prepare snapshot writer: %w", err)
	}
	return &SnapshotWriter{
		catalog: c, ctx: ctx, tx: tx, insert: statement, snapshotID: id,
		driveID: driveID, sourcePath: sourcePath, hashMode: hashMode,
	}, nil
}

func (w *SnapshotWriter) ID() int64 { return w.snapshotID }

func (w *SnapshotWriter) Add(entry Entry) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return errors.New("snapshot writer is closed")
	}
	modified := int64(0)
	if !entry.ModifiedAt.IsZero() {
		modified = entry.ModifiedAt.Unix()
	}
	hidden := 0
	if entry.Hidden {
		hidden = 1
	}
	if _, err := w.insert.ExecContext(w.ctx, w.snapshotID, entry.Path, entry.ParentPath,
		entry.Name, entry.Extension, entry.Kind, entry.Size, modified, entry.Hash, hidden); err != nil {
		return fmt.Errorf("store %q: %w", entry.Path, err)
	}
	if entry.Kind == "file" {
		w.fileCount++
		w.totalBytes += entry.Size
	} else if entry.Kind == "directory" {
		w.directoryCount++
	}
	return nil
}

func (w *SnapshotWriter) AddError() {
	w.mu.Lock()
	w.errorCount++
	w.mu.Unlock()
}

func (w *SnapshotWriter) Complete() (Snapshot, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return Snapshot{}, errors.New("snapshot writer is closed")
	}
	w.closed = true
	if err := w.insert.Close(); err != nil {
		w.tx.Rollback()
		w.catalog.markSnapshotFailed(context.Background(), w.snapshotID, err)
		return Snapshot{}, fmt.Errorf("close snapshot writer: %w", err)
	}
	now := time.Now().Unix()
	if _, err := w.tx.ExecContext(w.ctx, `UPDATE snapshots SET status='complete', file_count=?,
		directory_count=?, total_bytes=?, error_count=?, completed_at=? WHERE id=?`,
		w.fileCount, w.directoryCount, w.totalBytes, w.errorCount, now, w.snapshotID); err != nil {
		w.tx.Rollback()
		w.catalog.markSnapshotFailed(context.Background(), w.snapshotID, err)
		return Snapshot{}, fmt.Errorf("finalize snapshot: %w", err)
	}
	if _, err := w.tx.ExecContext(w.ctx, `UPDATE drives SET latest_snapshot_id=?, source_path=?, updated_at=? WHERE id=?`,
		w.snapshotID, w.sourcePath, now, w.driveID); err != nil {
		w.tx.Rollback()
		w.catalog.markSnapshotFailed(context.Background(), w.snapshotID, err)
		return Snapshot{}, fmt.Errorf("update drive snapshot: %w", err)
	}
	if err := w.tx.Commit(); err != nil {
		w.catalog.markSnapshotFailed(context.Background(), w.snapshotID, err)
		return Snapshot{}, fmt.Errorf("commit snapshot: %w", err)
	}
	return w.catalog.GetSnapshot(context.Background(), w.snapshotID)
}

func (w *SnapshotWriter) Fail(cause error) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	_ = w.insert.Close()
	_ = w.tx.Rollback()
	return w.catalog.markSnapshotFailed(context.Background(), w.snapshotID, cause)
}

func (c *Catalog) markSnapshotFailed(ctx context.Context, id int64, cause error) error {
	message := "scan failed"
	if cause != nil {
		message = cause.Error()
	}
	_, err := c.db.ExecContext(ctx, `UPDATE snapshots SET status='failed', completed_at=?, failure=? WHERE id=?`,
		time.Now().Unix(), message, id)
	return err
}
