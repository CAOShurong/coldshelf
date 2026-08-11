-- Exact schema shape used by ColdShelf v0.1.5, with one complete fixture scan.
PRAGMA foreign_keys=OFF;
BEGIN;
CREATE TABLE metadata (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
CREATE TABLE drives (
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
);
CREATE TABLE snapshots (
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
);
CREATE TABLE entries (
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
);
CREATE INDEX idx_entries_snapshot_parent ON entries(snapshot_id, parent_path, kind, name);
CREATE INDEX idx_entries_snapshot_extension ON entries(snapshot_id, extension);
CREATE INDEX idx_entries_hash ON entries(hash, size) WHERE hash <> '';
CREATE INDEX idx_snapshots_drive ON snapshots(drive_id, id DESC);
CREATE VIRTUAL TABLE entries_fts USING fts5(
  name,
  path,
  content='entries',
  content_rowid='id',
  tokenize='unicode61 remove_diacritics 2'
);
CREATE TRIGGER entries_ai AFTER INSERT ON entries BEGIN
  INSERT INTO entries_fts(rowid, name, path) VALUES (new.id, new.name, new.path);
END;
CREATE TRIGGER entries_ad AFTER DELETE ON entries BEGIN
  INSERT INTO entries_fts(entries_fts, rowid, name, path) VALUES ('delete', old.id, old.name, old.path);
END;
CREATE TRIGGER entries_au AFTER UPDATE ON entries BEGIN
  INSERT INTO entries_fts(entries_fts, rowid, name, path) VALUES ('delete', old.id, old.name, old.path);
  INSERT INTO entries_fts(rowid, name, path) VALUES (new.id, new.name, new.path);
END;
INSERT INTO metadata(key, value) VALUES('schema_version', '1');
INSERT INTO drives(id, name, source_path, location, notes, tags, fingerprint, latest_snapshot_id, created_at, updated_at)
VALUES('drv_010203040506', 'Legacy', 'E:/LEGACY', 'Shelf V1', 'Created from the v0.1.5 schema fixture.', '["legacy"]', '', NULL, 100, 110);
INSERT INTO snapshots(id, drive_id, source_path, status, hash_mode, file_count, directory_count, total_bytes, error_count, started_at, completed_at, failure)
VALUES(1, 'drv_010203040506', 'E:/LEGACY', 'complete', 'none', 1, 0, 3, 0, 100, 110, '');
INSERT INTO entries(snapshot_id, path, parent_path, name, extension, kind, size, modified_at, hash, hidden)
VALUES(1, 'legacy.txt', '', 'legacy.txt', 'txt', 'file', 3, 105, '', 0);
UPDATE drives SET latest_snapshot_id=1 WHERE id='drv_010203040506';
COMMIT;
