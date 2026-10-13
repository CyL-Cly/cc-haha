ALTER TABLE sessions ADD COLUMN openai_fast_mode INTEGER CHECK (openai_fast_mode IN (0, 1));
