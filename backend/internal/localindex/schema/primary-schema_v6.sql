
ALTER TABLE sessions ADD COLUMN is_team_worker INTEGER NOT NULL DEFAULT 0 CHECK (is_team_worker IN (0, 1));
