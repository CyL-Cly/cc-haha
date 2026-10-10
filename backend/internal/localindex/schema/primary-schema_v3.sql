
CREATE TABLE activity_sources (
  path TEXT PRIMARY KEY,
  parent_session_id TEXT NOT NULL,
  project_path TEXT NOT NULL,
  is_subagent INTEGER NOT NULL CHECK (is_subagent IN (0, 1)),
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

CREATE TABLE activity_sessions (
  transcript_path TEXT PRIMARY KEY REFERENCES activity_sources(path) ON DELETE CASCADE,
  session_id TEXT NOT NULL,
  first_timestamp TEXT,
  last_timestamp TEXT,
  duration_ms INTEGER NOT NULL DEFAULT 0,
  message_count INTEGER NOT NULL DEFAULT 0,
  start_hour INTEGER,
  speculation_time_saved_ms INTEGER NOT NULL DEFAULT 0,
  shot_count INTEGER
);

CREATE TABLE activity_daily (
  transcript_path TEXT NOT NULL REFERENCES activity_sources(path) ON DELETE CASCADE,
  date TEXT NOT NULL,
  message_count INTEGER NOT NULL DEFAULT 0,
  tool_call_count INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (transcript_path, date)
);

CREATE TABLE activity_daily_models (
  transcript_path TEXT NOT NULL REFERENCES activity_sources(path) ON DELETE CASCADE,
  date TEXT NOT NULL,
  model TEXT NOT NULL,
  input_tokens INTEGER NOT NULL DEFAULT 0,
  output_tokens INTEGER NOT NULL DEFAULT 0,
  cache_read_input_tokens INTEGER NOT NULL DEFAULT 0,
  cache_creation_input_tokens INTEGER NOT NULL DEFAULT 0,
  web_search_requests INTEGER NOT NULL DEFAULT 0,
  cost_usd REAL NOT NULL DEFAULT 0,
  context_window INTEGER NOT NULL DEFAULT 0,
  max_output_tokens INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (transcript_path, date, model)
);

CREATE TABLE activity_daily_tools (
  transcript_path TEXT NOT NULL REFERENCES activity_sources(path) ON DELETE CASCADE,
  date TEXT NOT NULL,
  tool_name TEXT NOT NULL,
  call_count INTEGER NOT NULL,
  PRIMARY KEY (transcript_path, date, tool_name)
);

CREATE TABLE activity_daily_skills (
  transcript_path TEXT NOT NULL REFERENCES activity_sources(path) ON DELETE CASCADE,
  date TEXT NOT NULL,
  skill_name TEXT NOT NULL,
  call_count INTEGER NOT NULL,
  PRIMARY KEY (transcript_path, date, skill_name)
);

CREATE TABLE activity_backfill_state (
  scope TEXT PRIMARY KEY,
  state TEXT NOT NULL,
  watermark TEXT,
  discovered INTEGER NOT NULL DEFAULT 0,
  indexed INTEGER NOT NULL DEFAULT 0,
  degraded INTEGER NOT NULL DEFAULT 0,
  last_error_code TEXT,
  updated_at_ms INTEGER NOT NULL
);

CREATE INDEX activity_daily_date_idx
  ON activity_daily(date, transcript_path);
CREATE INDEX activity_models_date_idx
  ON activity_daily_models(date, model, transcript_path);
CREATE INDEX activity_tools_date_idx
  ON activity_daily_tools(date, tool_name, transcript_path);
CREATE INDEX activity_skills_date_idx
  ON activity_daily_skills(date, skill_name, transcript_path);
CREATE INDEX activity_sources_parent_idx
  ON activity_sources(parent_session_id, path);
