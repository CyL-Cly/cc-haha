
ALTER TABLE trace_sources ADD COLUMN oversized_records INTEGER NOT NULL DEFAULT 0;
ALTER TABLE trace_sources ADD COLUMN scan_truncated INTEGER NOT NULL DEFAULT 0;
ALTER TABLE trace_sources ADD COLUMN window_start_byte INTEGER NOT NULL DEFAULT 0;
CREATE INDEX trace_calls_page_order_idx ON trace_calls(session_id, started_at, first_ordinal);
CREATE INDEX trace_calls_model_order_idx ON trace_calls(session_id, model, started_at, first_ordinal);
UPDATE trace_sources SET state = 'degraded', last_error_code = 'TRACE_INDEX_V6_REBUILD_REQUIRED';
