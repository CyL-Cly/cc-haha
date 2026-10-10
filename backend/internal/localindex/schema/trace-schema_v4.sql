
ALTER TABLE trace_calls
  ADD COLUMN first_ordinal INTEGER NOT NULL DEFAULT 0;

UPDATE trace_calls
SET first_ordinal = ordinal;

ALTER TABLE trace_session_models
  ADD COLUMN first_started_at TEXT NOT NULL DEFAULT '';

ALTER TABLE trace_session_models
  ADD COLUMN first_ordinal INTEGER NOT NULL DEFAULT 0;

UPDATE trace_session_models
SET
  first_started_at = COALESCE((
    SELECT call.started_at
    FROM trace_calls AS call
    WHERE call.session_id = trace_session_models.session_id
      AND call.model = trace_session_models.model
    ORDER BY call.started_at, call.first_ordinal
    LIMIT 1
  ), ''),
  first_ordinal = COALESCE((
    SELECT call.first_ordinal
    FROM trace_calls AS call
    WHERE call.session_id = trace_session_models.session_id
      AND call.model = trace_session_models.model
    ORDER BY call.started_at, call.first_ordinal
    LIMIT 1
  ), 0);

CREATE INDEX trace_session_models_order_idx
  ON trace_session_models(session_id, first_started_at, first_ordinal);

-- Earlier schemas retained only the latest locator ordinal for each call ID,
-- so they cannot reconstruct canonical Map insertion order for LWW records.
-- The projection is disposable: force the next source access to rebuild from
-- JSONL instead of serving a plausibly ordered but incorrect migrated summary.
UPDATE trace_sources
SET state = 'degraded', last_error_code = 'TRACE_INDEX_V4_REBUILD_REQUIRED';
