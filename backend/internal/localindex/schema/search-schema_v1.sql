
CREATE TABLE search_sources (
  path TEXT PRIMARY KEY,
  project_path TEXT NOT NULL,
  owner_session_id TEXT NOT NULL,
  owner_transcript_path TEXT NOT NULL,
  modified_at_ms REAL NOT NULL,
  size_bytes INTEGER NOT NULL,
  mtime_ms REAL NOT NULL,
  file_identity TEXT,
  fingerprint TEXT NOT NULL,
  indexed_bytes INTEGER NOT NULL DEFAULT 0,
  indexed_lines INTEGER NOT NULL DEFAULT 0,
  parser_version INTEGER NOT NULL,
  state TEXT NOT NULL CHECK (state IN ('ready', 'pending', 'degraded')),
  last_error_code TEXT,
  updated_at_ms INTEGER NOT NULL
);

CREATE INDEX search_sources_owner_idx
  ON search_sources(owner_transcript_path, path);
CREATE INDEX search_sources_project_modified_idx
  ON search_sources(project_path, modified_at_ms DESC, owner_transcript_path, path);

CREATE TABLE search_documents (
  id INTEGER PRIMARY KEY,
  source_path TEXT NOT NULL REFERENCES search_sources(path) ON DELETE CASCADE,
  jsonl_line INTEGER NOT NULL CHECK (jsonl_line > 0),
  byte_start INTEGER NOT NULL CHECK (byte_start >= 0),
  byte_length INTEGER NOT NULL CHECK (byte_length > 0),
  segment_index INTEGER NOT NULL CHECK (segment_index >= 0),
  role TEXT NOT NULL CHECK (role IN ('user', 'assistant')),
  message_id TEXT,
  timestamp TEXT,
  body TEXT NOT NULL,
  normalized_body TEXT NOT NULL,
  UNIQUE (source_path, jsonl_line, segment_index)
);

CREATE INDEX search_documents_source_line_idx
  ON search_documents(source_path, jsonl_line, segment_index);

CREATE VIRTUAL TABLE search_documents_fts USING fts5(
  normalized_body,
  content='search_documents',
  content_rowid='id',
  tokenize='trigram case_sensitive 0'
);

CREATE TRIGGER search_documents_after_insert AFTER INSERT ON search_documents BEGIN
  INSERT INTO search_documents_fts(rowid, normalized_body)
  VALUES (new.id, new.normalized_body);
END;

CREATE TRIGGER search_documents_after_delete AFTER DELETE ON search_documents BEGIN
  INSERT INTO search_documents_fts(search_documents_fts, rowid, normalized_body)
  VALUES ('delete', old.id, old.normalized_body);
END;

CREATE TRIGGER search_documents_after_update AFTER UPDATE ON search_documents BEGIN
  INSERT INTO search_documents_fts(search_documents_fts, rowid, normalized_body)
  VALUES ('delete', old.id, old.normalized_body);
  INSERT INTO search_documents_fts(rowid, normalized_body)
  VALUES (new.id, new.normalized_body);
END;

CREATE TABLE search_backfill_state (
  scope TEXT PRIMARY KEY,
  state TEXT NOT NULL CHECK (state IN ('building', 'ready', 'degraded')),
  generation INTEGER NOT NULL DEFAULT 0,
  discovered INTEGER NOT NULL DEFAULT 0,
  indexed INTEGER NOT NULL DEFAULT 0,
  degraded INTEGER NOT NULL DEFAULT 0,
  last_error_code TEXT,
  updated_at_ms INTEGER NOT NULL
);
