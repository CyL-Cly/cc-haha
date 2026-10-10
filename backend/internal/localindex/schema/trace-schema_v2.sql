
ALTER TABLE trace_sources ADD COLUMN file_identity TEXT;
ALTER TABLE trace_sources ADD COLUMN fingerprint TEXT;
ALTER TABLE trace_sources ADD COLUMN pending_tail_bytes INTEGER NOT NULL DEFAULT 0;

CREATE TABLE trace_sessions (
  session_id TEXT PRIMARY KEY REFERENCES trace_sources(session_id) ON DELETE CASCADE,
  revision INTEGER NOT NULL,
  last_reset_revision INTEGER NOT NULL,
  next_ordinal INTEGER NOT NULL,
  api_calls INTEGER NOT NULL,
  failed_calls INTEGER NOT NULL,
  total_duration_ms REAL NOT NULL,
  total_input_tokens INTEGER NOT NULL,
  total_output_tokens INTEGER NOT NULL,
  summary_updated_at TEXT
);

CREATE TABLE trace_session_models (
  session_id TEXT NOT NULL REFERENCES trace_sessions(session_id) ON DELETE CASCADE,
  model TEXT NOT NULL,
  call_count INTEGER NOT NULL CHECK (call_count > 0),
  PRIMARY KEY (session_id, model)
);

INSERT INTO trace_sessions (
  session_id, revision, last_reset_revision, next_ordinal,
  api_calls, failed_calls, total_duration_ms,
  total_input_tokens, total_output_tokens, summary_updated_at
)
SELECT
  source.session_id,
  source.revision,
  source.last_reset_revision,
  COALESCE((
    SELECT MAX(ordinal) + 1 FROM (
      SELECT ordinal FROM trace_calls WHERE session_id = source.session_id
      UNION ALL
      SELECT ordinal FROM trace_events WHERE session_id = source.session_id
    )
  ), 0),
  COUNT(call.call_id),
  COALESCE(SUM(call.failed), 0),
  COALESCE(SUM(call.duration_ms), 0),
  COALESCE(SUM(call.input_tokens), 0),
  COALESCE(SUM(call.output_tokens), 0),
  (
    SELECT COALESCE(latest.completed_at, latest.started_at)
    FROM trace_calls AS latest
    WHERE latest.session_id = source.session_id
    ORDER BY latest.started_at DESC, latest.ordinal DESC
    LIMIT 1
  )
FROM trace_sources AS source
LEFT JOIN trace_calls AS call ON call.session_id = source.session_id
GROUP BY source.session_id;

INSERT INTO trace_session_models (session_id, model, call_count)
SELECT session_id, model, COUNT(*)
FROM trace_calls
WHERE model IS NOT NULL
GROUP BY session_id, model;
