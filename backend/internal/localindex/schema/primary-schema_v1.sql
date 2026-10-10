
CREATE TABLE schema_meta (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE source_files (
  path TEXT PRIMARY KEY,
  kind TEXT NOT NULL CHECK (kind IN ('transcript')),
  size_bytes INTEGER NOT NULL,
  mtime_ms INTEGER NOT NULL,
  file_identity TEXT,
  prefix_hash TEXT NOT NULL,
  indexed_bytes INTEGER NOT NULL DEFAULT 0,
  parser_version INTEGER NOT NULL,
  state TEXT NOT NULL CHECK (state IN ('ready', 'pending', 'degraded')),
  last_error_code TEXT,
  updated_at_ms INTEGER NOT NULL
);

CREATE TABLE sessions (
  transcript_path TEXT PRIMARY KEY REFERENCES source_files(path) ON DELETE CASCADE,
  session_id TEXT NOT NULL,
  project_path TEXT NOT NULL,
  title TEXT NOT NULL,
  created_at TEXT NOT NULL,
  modified_at TEXT NOT NULL,
  modified_at_ms INTEGER NOT NULL,
  message_count INTEGER NOT NULL,
  work_dir TEXT,
  repository_json TEXT,
  worktree_session_json TEXT,
  permission_mode TEXT,
  runtime_provider_id TEXT,
  runtime_provider_present INTEGER NOT NULL DEFAULT 0
    CHECK (runtime_provider_present IN (0, 1)),
  runtime_model_id TEXT,
  effort_level TEXT
);

CREATE INDEX sessions_modified_idx
  ON sessions(modified_at_ms DESC, session_id, transcript_path);
CREATE INDEX sessions_project_modified_idx
  ON sessions(project_path, modified_at_ms DESC, session_id, transcript_path);
CREATE INDEX sessions_id_project_idx
  ON sessions(session_id, project_path, transcript_path);

CREATE TABLE backfill_state (
  scope TEXT PRIMARY KEY,
  state TEXT NOT NULL,
  watermark TEXT,
  discovered INTEGER NOT NULL DEFAULT 0,
  indexed INTEGER NOT NULL DEFAULT 0,
  degraded INTEGER NOT NULL DEFAULT 0,
  last_error_code TEXT,
  updated_at_ms INTEGER NOT NULL
);
