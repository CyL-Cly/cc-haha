
CREATE TABLE session_entries (
  transcript_path TEXT NOT NULL REFERENCES source_files(path) ON DELETE CASCADE,
  ordinal INTEGER NOT NULL,
  jsonl_line INTEGER NOT NULL,
  byte_start INTEGER NOT NULL,
  byte_length INTEGER NOT NULL,
  entry_type TEXT NOT NULL,
  message_id TEXT,
  role TEXT,
  timestamp TEXT,
  parent_tool_use_id TEXT,
  PRIMARY KEY (transcript_path, ordinal)
);

CREATE INDEX session_entries_kind_idx
  ON session_entries(transcript_path, entry_type, ordinal);
CREATE INDEX session_entries_message_idx
  ON session_entries(message_id, transcript_path);
