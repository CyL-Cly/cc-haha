
CREATE TABLE trace_sources (
  session_id TEXT PRIMARY KEY,
  file_path TEXT NOT NULL UNIQUE,
  size_bytes INTEGER NOT NULL,
  mtime_ms REAL NOT NULL,
  indexed_bytes INTEGER NOT NULL,
  revision INTEGER NOT NULL,
  last_reset_revision INTEGER NOT NULL,
  state TEXT NOT NULL CHECK (state IN ('ready', 'degraded')),
  last_error_code TEXT,
  updated_at_ms INTEGER NOT NULL
);

CREATE TABLE trace_calls (
  session_id TEXT NOT NULL REFERENCES trace_sources(session_id) ON DELETE CASCADE,
  call_id TEXT NOT NULL,
  ordinal INTEGER NOT NULL,
  byte_start INTEGER NOT NULL,
  byte_length INTEGER NOT NULL,
  revision INTEGER NOT NULL,
  started_at TEXT NOT NULL,
  completed_at TEXT,
  status TEXT NOT NULL,
  source TEXT NOT NULL,
  model TEXT,
  duration_ms REAL,
  failed INTEGER NOT NULL CHECK (failed IN (0, 1)),
  input_tokens INTEGER NOT NULL,
  output_tokens INTEGER NOT NULL,
  PRIMARY KEY (session_id, call_id)
);

CREATE INDEX trace_calls_order_idx
  ON trace_calls(session_id, started_at, ordinal);
CREATE INDEX trace_calls_revision_idx
  ON trace_calls(session_id, revision);

CREATE TABLE trace_events (
  session_id TEXT NOT NULL REFERENCES trace_sources(session_id) ON DELETE CASCADE,
  ordinal INTEGER NOT NULL,
  event_id TEXT NOT NULL,
  byte_start INTEGER NOT NULL,
  byte_length INTEGER NOT NULL,
  revision INTEGER NOT NULL,
  timestamp TEXT NOT NULL,
  phase TEXT NOT NULL,
  severity TEXT NOT NULL,
  call_id TEXT,
  source TEXT,
  model TEXT,
  PRIMARY KEY (session_id, ordinal)
);

CREATE INDEX trace_events_order_idx
  ON trace_events(session_id, timestamp, ordinal);
CREATE INDEX trace_events_revision_idx
  ON trace_events(session_id, revision);
