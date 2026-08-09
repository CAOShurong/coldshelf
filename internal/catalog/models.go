package catalog

import "time"

type Drive struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	SourcePath       string    `json:"source_path"`
	Location         string    `json:"location"`
	Notes            string    `json:"notes"`
	Tags             []string  `json:"tags"`
	Fingerprint      string    `json:"fingerprint"`
	LatestSnapshotID *int64    `json:"latest_snapshot_id,omitempty"`
	FileCount        int64     `json:"file_count"`
	DirectoryCount   int64     `json:"directory_count"`
	TotalBytes       int64     `json:"total_bytes"`
	LastScannedAt    time.Time `json:"last_scanned_at,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type NewDrive struct {
	Name        string
	SourcePath  string
	Location    string
	Notes       string
	Tags        []string
	Fingerprint string
}

type DrivePatch struct {
	Name       *string   `json:"name,omitempty"`
	Location   *string   `json:"location,omitempty"`
	Notes      *string   `json:"notes,omitempty"`
	Tags       *[]string `json:"tags,omitempty"`
	SourcePath *string   `json:"source_path,omitempty"`
}

type Snapshot struct {
	ID             int64     `json:"id"`
	DriveID        string    `json:"drive_id"`
	SourcePath     string    `json:"source_path"`
	Status         string    `json:"status"`
	HashMode       string    `json:"hash_mode"`
	FileCount      int64     `json:"file_count"`
	DirectoryCount int64     `json:"directory_count"`
	TotalBytes     int64     `json:"total_bytes"`
	ErrorCount     int64     `json:"error_count"`
	StartedAt      time.Time `json:"started_at"`
	CompletedAt    time.Time `json:"completed_at,omitempty"`
	Failure        string    `json:"failure,omitempty"`
}

type Entry struct {
	ID         int64     `json:"id"`
	SnapshotID int64     `json:"snapshot_id"`
	Path       string    `json:"path"`
	ParentPath string    `json:"parent_path"`
	Name       string    `json:"name"`
	Extension  string    `json:"extension"`
	Kind       string    `json:"kind"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modified_at,omitempty"`
	Hash       string    `json:"hash,omitempty"`
	Hidden     bool      `json:"hidden"`
}

type SearchHit struct {
	Entry
	DriveID   string `json:"drive_id"`
	DriveName string `json:"drive_name"`
	Location  string `json:"location"`
}

type DiffEntry struct {
	Change      string `json:"change"`
	Path        string `json:"path"`
	Kind        string `json:"kind"`
	OldSize     *int64 `json:"old_size,omitempty"`
	NewSize     *int64 `json:"new_size,omitempty"`
	OldModified *int64 `json:"old_modified,omitempty"`
	NewModified *int64 `json:"new_modified,omitempty"`
}

type DuplicateGroup struct {
	Hash  string          `json:"hash"`
	Size  int64           `json:"size"`
	Files []DuplicateFile `json:"files"`
}

type DuplicateFile struct {
	DriveID   string `json:"drive_id"`
	DriveName string `json:"drive_name"`
	Path      string `json:"path"`
}

type Stats struct {
	DriveCount      int64 `json:"drive_count"`
	SnapshotCount   int64 `json:"snapshot_count"`
	FileCount       int64 `json:"file_count"`
	DirectoryCount  int64 `json:"directory_count"`
	TotalBytes      int64 `json:"total_bytes"`
	HashedFileCount int64 `json:"hashed_file_count"`
}

type ExtensionStat struct {
	Extension string `json:"extension"`
	Count     int64  `json:"count"`
	Bytes     int64  `json:"bytes"`
}
