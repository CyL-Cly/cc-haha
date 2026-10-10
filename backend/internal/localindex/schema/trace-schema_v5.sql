
-- Session overviews are served from locators without re-reading the JSONL, so
-- the locator has to carry the few body facts the trace tree header shows.
ALTER TABLE trace_calls ADD COLUMN request_bytes INTEGER;
ALTER TABLE trace_calls ADD COLUMN response_bytes INTEGER;
ALTER TABLE trace_calls ADD COLUMN response_status INTEGER;
ALTER TABLE trace_events ADD COLUMN title TEXT;
ALTER TABLE trace_events ADD COLUMN message TEXT;

-- Pre-existing locator rows have NULL body facts; a shell built from them
-- would look permanently pending (no response) or sizeless. The projection is
-- disposable: force a one-time rebuild from JSONL on next access instead of
-- serving shells that misreport completed calls.
UPDATE trace_sources
SET state = 'degraded', last_error_code = 'TRACE_INDEX_V5_REBUILD_REQUIRED';
