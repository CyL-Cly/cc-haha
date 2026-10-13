package localindex

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestSyncIndexV6UpgradeAndFastTrueFalseSurviveReprojection(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.sqlite")
	old, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	for v := 1; v <= 6; v++ {
		script, e := schemaFiles.ReadFile("schema/primary-schema_v" + string(rune('0'+v)) + ".sql")
		if e != nil {
			t.Fatal(e)
		}
		if _, e = old.Exec(string(script)); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = old.Exec("PRAGMA user_version = 6"); e != nil {
		t.Fatal(e)
	}
	old.Close()
	db, e := OpenDatabase(ctx, DatabaseOptions{Path: path, Kind: Primary})
	if e != nil {
		t.Fatal(e)
	}
	if e = db.Read(ctx, func(op *Operation) error {
		row, e := op.Get("PRAGMA user_version")
		if e == nil && intValue(row.Get("user_version")) != 7 {
			t.Fatal(row)
		}
		_, e = op.Get("SELECT openai_fast_mode FROM sessions")
		return e
	}); e != nil {
		t.Fatal(e)
	}
	db.Close()
	p, _, home := sessionProjectorFixture(t, 1)
	transcript := filepath.Join(home, "fast.jsonl")
	index := p.o.Index
	content := `{"type":"session-meta","runtimeProviderId":"openai-official","runtimeModelId":"gpt-6-sol","openAIFastMode":true}` + "\n" + `{"type":"user","uuid":"u1","timestamp":"2026-10-12T00:00:00Z","message":{"role":"user","content":"task"}}` + "\n"
	if e = os.WriteFile(transcript, []byte(content), 0600); e != nil {
		t.Fatal(e)
	}
	candidate := SessionCandidate{Path: transcript, SessionID: "fast", ProjectPath: "fixture", FallbackCreatedAt: "2026-10-12T00:00:00Z", FallbackModifiedAt: "2026-10-12T00:00:01Z", ModifiedAtMs: 1}
	for _, want := range []string{"true", "false"} {
		if want == "false" {
			f, e := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0600)
			if e != nil {
				t.Fatal(e)
			}
			_, _ = f.WriteString(`{"type":"session-meta","openAIFastMode":false}` + "\n")
			f.Close()
		}
		if _, e = p.Project(ctx, candidate, nil); e != nil {
			t.Fatal(e)
		}
		row, e := index.Get(ctx, "fast")
		if e != nil || row == nil || string(row.Get("openAIFastMode")) != want {
			t.Fatal(row, e)
		}
		src, e := index.GetSource(ctx, transcript)
		if e != nil || src == nil || src.ParserVersion != 10 {
			t.Fatal(src, e)
		}
	}
	reopened, e := OpenDatabase(ctx, DatabaseOptions{Path: path, Kind: Primary})
	if e != nil {
		t.Fatal("v7 reopen", e)
	}
	reopened.Close()
}
