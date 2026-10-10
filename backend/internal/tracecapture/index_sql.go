package tracecapture

const SOURCE_COLUMNS = `
  source.session_id,
  source.file_path,
  source.size_bytes,
  source.mtime_ms,
  source.indexed_bytes,
  source.file_identity,
  source.fingerprint,
  source.pending_tail_bytes,
  session.revision,
  session.last_reset_revision,
  session.reset_token,
  session.next_ordinal,
  source.state,
  source.last_error_code,
  source.oversized_records,
  source.scan_truncated,
  source.window_start_byte
`

const SUMMARY_COLUMNS = `
  
  source.session_id,
  source.file_path,
  source.size_bytes,
  source.mtime_ms,
  source.indexed_bytes,
  source.file_identity,
  source.fingerprint,
  source.pending_tail_bytes,
  session.revision,
  session.last_reset_revision,
  session.reset_token,
  session.next_ordinal,
  source.state,
  source.last_error_code,
  source.oversized_records,
  source.scan_truncated,
  source.window_start_byte
,
  session.api_calls,
  session.failed_calls,
  session.total_duration_ms,
  session.total_input_tokens,
  session.total_output_tokens,
  session.summary_updated_at
`

const UPSERT_SOURCE_SQL = `
INSERT INTO trace_sources (
  session_id, file_path, size_bytes, mtime_ms, indexed_bytes,
  revision, last_reset_revision, state, last_error_code, updated_at_ms,
  file_identity, fingerprint, pending_tail_bytes, oversized_records, scan_truncated, window_start_byte
) VALUES (?, ?, ?, ?, ?, ?, ?, 'ready', ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(session_id) DO UPDATE SET
  file_path = excluded.file_path,
  size_bytes = excluded.size_bytes,
  mtime_ms = excluded.mtime_ms,
  indexed_bytes = excluded.indexed_bytes,
  revision = excluded.revision,
  last_reset_revision = excluded.last_reset_revision,
  state = 'ready',
  last_error_code = excluded.last_error_code,
  updated_at_ms = excluded.updated_at_ms,
  file_identity = excluded.file_identity,
  fingerprint = excluded.fingerprint,
  pending_tail_bytes = excluded.pending_tail_bytes,
  oversized_records = excluded.oversized_records,
  scan_truncated = excluded.scan_truncated,
  window_start_byte = excluded.window_start_byte
`

const UPSERT_SESSION_SQL = `
INSERT INTO trace_sessions (
  session_id, revision, last_reset_revision, reset_token, next_ordinal,
  api_calls, failed_calls, total_duration_ms,
  total_input_tokens, total_output_tokens, summary_updated_at
) VALUES (?, ?, ?, ?, ?, 0, 0, 0, 0, 0, NULL)
ON CONFLICT(session_id) DO UPDATE SET
  revision = excluded.revision,
  last_reset_revision = excluded.last_reset_revision,
  reset_token = excluded.reset_token,
  next_ordinal = excluded.next_ordinal
`

const UPSERT_CALL_SQL = `
INSERT INTO trace_calls (
  session_id, call_id, ordinal, first_ordinal, byte_start, byte_length, revision,
  started_at, completed_at, status, source, model, duration_ms,
  failed, input_tokens, output_tokens,
  request_bytes, response_bytes, response_status
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(session_id, call_id) DO UPDATE SET
  ordinal = excluded.ordinal,
  byte_start = excluded.byte_start,
  byte_length = excluded.byte_length,
  revision = excluded.revision,
  started_at = excluded.started_at,
  completed_at = excluded.completed_at,
  status = excluded.status,
  source = excluded.source,
  model = excluded.model,
  duration_ms = excluded.duration_ms,
  failed = excluded.failed,
  input_tokens = excluded.input_tokens,
  output_tokens = excluded.output_tokens,
  request_bytes = excluded.request_bytes,
  response_bytes = excluded.response_bytes,
  response_status = excluded.response_status
`

const INSERT_EVENT_SQL = `
INSERT INTO trace_events (
  session_id, ordinal, event_id, byte_start, byte_length, revision,
  timestamp, phase, severity, call_id, source, model, title, message
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(session_id, ordinal) DO UPDATE SET
  event_id = excluded.event_id,
  byte_start = excluded.byte_start,
  byte_length = excluded.byte_length,
  revision = excluded.revision,
  timestamp = excluded.timestamp,
  phase = excluded.phase,
  severity = excluded.severity,
  call_id = excluded.call_id,
  source = excluded.source,
  model = excluded.model,
  title = excluded.title,
  message = excluded.message
`
