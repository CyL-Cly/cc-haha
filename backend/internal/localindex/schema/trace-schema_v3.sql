
ALTER TABLE trace_sessions
  ADD COLUMN reset_token TEXT NOT NULL DEFAULT '';

UPDATE trace_sessions
SET reset_token = lower(hex(randomblob(16)))
WHERE reset_token = '';
