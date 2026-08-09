package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/CAOShurong/coldshelf/internal/catalog"
	"github.com/CAOShurong/coldshelf/internal/scanner"
)

type ScanRequest struct {
	DriveID  string   `json:"drive_id"`
	Name     string   `json:"name"`
	Path     string   `json:"path"`
	Location string   `json:"location"`
	Notes    string   `json:"notes"`
	Tags     []string `json:"tags"`
	HashMode string   `json:"hash_mode"`
	Exclude  []string `json:"exclude"`
}

type ScanJob struct {
	ID          string           `json:"id"`
	Status      string           `json:"status"`
	DriveID     string           `json:"drive_id"`
	SnapshotID  int64            `json:"snapshot_id,omitempty"`
	Progress    scanner.Progress `json:"progress"`
	Error       string           `json:"error,omitempty"`
	StartedAt   time.Time        `json:"started_at"`
	CompletedAt time.Time        `json:"completed_at,omitempty"`
}

type jobManager struct {
	catalog *catalog.Catalog
	mu      sync.RWMutex
	jobs    map[string]*ScanJob
}

func newJobManager(c *catalog.Catalog) *jobManager {
	return &jobManager{catalog: c, jobs: make(map[string]*ScanJob)}
}

func (m *jobManager) Start(input ScanRequest) (ScanJob, error) {
	root, err := filepath.Abs(strings.TrimSpace(input.Path))
	if err != nil {
		return ScanJob{}, fmt.Errorf("resolve scan path: %w", err)
	}
	if input.HashMode == "" {
		input.HashMode = string(scanner.HashNone)
	}
	info, err := os.Stat(root)
	if err != nil {
		return ScanJob{}, fmt.Errorf("open scan path: %w", err)
	}
	if !info.IsDir() {
		return ScanJob{}, fmt.Errorf("scan path must be a directory or mounted volume")
	}
	mode := scanner.HashMode(input.HashMode)
	if mode != scanner.HashNone && mode != scanner.HashQuick && mode != scanner.HashFull {
		return ScanJob{}, fmt.Errorf("hash_mode must be none, quick, or full")
	}

	ctx := context.Background()
	driveID := strings.TrimSpace(input.DriveID)
	if driveID == "" {
		name := strings.TrimSpace(input.Name)
		if name == "" {
			name = filepath.Base(root)
		}
		drive, err := m.catalog.CreateDrive(ctx, catalog.NewDrive{
			Name: name, SourcePath: root, Location: input.Location, Notes: input.Notes, Tags: input.Tags,
		})
		if err != nil {
			return ScanJob{}, err
		}
		driveID = drive.ID
	} else {
		drive, err := m.catalog.ResolveDrive(ctx, driveID)
		if err != nil {
			return ScanJob{}, err
		}
		driveID = drive.ID
	}

	jobID, err := randomID("scan_")
	if err != nil {
		return ScanJob{}, err
	}
	job := &ScanJob{ID: jobID, Status: "queued", DriveID: driveID, StartedAt: time.Now().UTC()}
	m.mu.Lock()
	m.jobs[jobID] = job
	m.mu.Unlock()

	go m.run(jobID, driveID, root, mode, input.Exclude)
	return m.Get(jobID)
}

func (m *jobManager) run(jobID, driveID, root string, mode scanner.HashMode, exclude []string) {
	ctx := context.Background()
	m.update(jobID, func(job *ScanJob) { job.Status = "scanning" })

	writer, err := m.catalog.StartSnapshot(ctx, driveID, root, string(mode))
	if err != nil {
		m.fail(jobID, err)
		return
	}
	m.update(jobID, func(job *ScanJob) { job.SnapshotID = writer.ID() })

	_, scanErr := scanner.Scan(ctx, root, scanner.Options{HashMode: mode, Exclude: exclude},
		writer.Add,
		func(progress scanner.Progress) {
			m.update(jobID, func(job *ScanJob) { job.Progress = progress })
		},
		func(_ string, _ error) { writer.AddError() },
	)
	if scanErr != nil {
		_ = writer.Fail(scanErr)
		m.fail(jobID, scanErr)
		return
	}
	if _, err := writer.Complete(); err != nil {
		m.fail(jobID, err)
		return
	}
	m.update(jobID, func(job *ScanJob) {
		job.Status = "complete"
		job.CompletedAt = time.Now().UTC()
	})
}

func (m *jobManager) Get(id string) (ScanJob, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	job := m.jobs[id]
	if job == nil {
		return ScanJob{}, fmt.Errorf("scan job not found")
	}
	return *job, nil
}

func (m *jobManager) update(id string, mutate func(*ScanJob)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if job := m.jobs[id]; job != nil {
		mutate(job)
	}
}

func (m *jobManager) fail(id string, err error) {
	m.update(id, func(job *ScanJob) {
		job.Status = "failed"
		job.Error = err.Error()
		job.CompletedAt = time.Now().UTC()
	})
}

func randomID(prefix string) (string, error) {
	value := make([]byte, 8)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(value), nil
}
