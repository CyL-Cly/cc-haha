package localindex

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

func TestSearchableSegmentsAgainstActualSource(t *testing.T) {
	entries := []any{map[string]any{}, map[string]any{"type": "progress", "message": map[string]any{"content": "needle"}}}
	for _, kind := range []string{"user", "assistant"} {
		for _, content := range []any{nil, 42, true, "", "  text\ufeff ", "\ufeff中文\ufeff", []any{}, []any{map[string]any{"type": "thinking", "thinking": "secret"}, map[string]any{"type": "text", "text": "plain"}, map[string]any{"type": "tool_result", "content": "secret"}, map[string]any{"type": "text", "text": " \nsecond\t "}, nil, true}, "<command-name>plan</command-name><command-args>a &amp; b</command-args>", "<command-name>/skill</command-name><command-message>my &lt;skill&gt;</command-message><skill-format>true</skill-format>", "<command-name>/help</command-name> extra", "<local-command-caveat>hidden metadata</local-command-caveat>", "<COMMAND-NAME>/upper</COMMAND-NAME>", "<command-name data-x=\"1\">/attr</command-name>", []any{map[string]any{"type": "text", "text": "<command-name>first</command-name>"}, map[string]any{"type": "text", "text": "<command-name>/second</command-name>"}}, []any{map[string]any{"type": "text", "text": "<command-name>one</command-name>"}, map[string]any{"type": "text", "text": "ordinary"}}, "<command-name>x</command-name>\ufeff", "<command-name> x </command-name><command-args> &amp;lt; </command-args>"} {
			entries = append(entries, map[string]any{"type": kind, "message": map[string]any{"role": "assistant", "content": content}})
		}
	}
	observed := sourceIndex(t, disposableHome(t), map[string]any{"action": "segments", "entries": entries})
	var rows []json.RawMessage
	_ = json.Unmarshal(observed["observations"], &rows)
	for n, v := range entries {
		var entry protocol.JSONObject
		if json.Unmarshal(raw(v), &entry) != nil {
			continue
		}
		actual := ExtractSearchableSegments(entry)
		if !reflect.DeepEqual(decoded(actual), decoded(rows[n])) {
			t.Fatalf("Visible search segments %d %s native=%s Source=%s", n, raw(v), raw(actual), rows[n])
		}
	}
}
func TestContentProjectorActualSourceIncrementalAndBounded(t *testing.T) {
	home := disposableHome(t)
	ctx := context.Background()
	p := filepath.Join(home, "source.jsonl")
	db, err := OpenDatabase(ctx, DatabaseOptions{Path: filepath.Join(home, "native.sqlite"), Kind: Content})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	index, _ := NewContentIndex(db, "fixture", func() time.Time { return time.UnixMilli(42) })
	projector, _ := NewContentProjector(ContentProjectorOptions{Index: index, Budget: sessionstore.NewBudget(), SpoolDir: filepath.Join(home, "tmp"), Now: func() time.Time { return time.UnixMilli(42) }})
	candidate := ContentCandidate{Path: p, ProjectPath: "project", OwnerSessionID: "session", OwnerTranscriptPath: p, ModifiedAtMs: 77}
	user := func(uuid, text string) string {
		return string(raw(map[string]any{"type": "user", "uuid": uuid, "timestamp": "2026-10-06T00:00:00Z", "message": map[string]any{"content": text}})) + "\n"
	}
	steps := []struct {
		name    string
		change  func()
		parser  int64
		maxLine int
	}{
		{"full", func() {
			_ = os.WriteFile(p, []byte(user("u1", "中文 needle")+"bad json\n"+strings.TrimSuffix(user("u2", "partial"), "\n")), 0600)
		}, 1, ContentMaxLineBytes},
		{"unchangedPending", func() {}, 1, ContentMaxLineBytes},
		{"appendCompleteTail", func() {
			f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0600)
			_, _ = f.WriteString("\n" + user("u3", "append needle"))
			_ = f.Close()
		}, 1, ContentMaxLineBytes},
		{"unchangedReady", func() {}, 1, ContentMaxLineBytes},
		{"rewrite", func() { _ = os.WriteFile(p, []byte(user("u4", "replaced original")), 0600) }, 1, ContentMaxLineBytes},
		{"parserVersionRebuild", func() {}, 2, ContentMaxLineBytes},
		{"replaceInode", func() {
			tmp := p + ".replace"
			_ = os.WriteFile(tmp, []byte(user("u5", "new inode")), 0600)
			_ = os.Rename(tmp, p)
		}, 2, ContentMaxLineBytes},
		{"oversized", func() {
			_ = os.WriteFile(p, []byte(user("small", "first")+user("big", strings.Repeat("x", 800))), 0600)
		}, 2, 200},
		{"deleted", func() { _ = os.Remove(p) }, 2, 200},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			step.change()
			projector.o.ParserVersion = step.parser
			projector.o.MaxLineBytes = step.maxLine
			source := sourceIndex(t, home, map[string]any{"action": "project", "candidate": ContentCandidate{Path: "source.jsonl", ProjectPath: candidate.ProjectPath, OwnerSessionID: candidate.OwnerSessionID, OwnerTranscriptPath: "source.jsonl", ModifiedAtMs: candidate.ModifiedAtMs}, "parserVersion": step.parser, "maxLineBytes": step.maxLine})
			result, e := projector.Project(ctx, candidate)
			if e != nil {
				t.Fatal(e)
			}
			sources, e := index.ListSources(ctx)
			if e != nil {
				t.Fatal(e)
			}
			var documents any
			e = db.Read(ctx, func(op *Operation) error {
				rows, e := op.All("SELECT source_path,jsonl_line,byte_start,byte_length,segment_index,role,message_id,timestamp,body,normalized_body FROM search_documents ORDER BY source_path,jsonl_line,segment_index")
				documents = rows
				return e
			})
			if e != nil {
				t.Fatal(e)
			}
			native := map[string]any{"result": result, "sources": sources, "documents": documents}
			if !reflect.DeepEqual(decoded(native), decoded(source)) {
				t.Fatalf("Source projection differs: native=%s Source=%s", raw(native), raw(source))
			}
		})
	}
}
func TestContentProjectionSpoolsLargeTranscriptAndDoesNotCommitChangedSource(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	p := filepath.Join(home, "source.jsonl")
	var buffer bytes.Buffer
	for n := 0; n < 1800; n++ {
		buffer.Write(raw(map[string]any{"type": "assistant", "message": map[string]any{"content": strings.Repeat("needle ", 700)}}))
		buffer.WriteByte('\n')
	}
	if e := os.WriteFile(p, buffer.Bytes(), 0600); e != nil {
		t.Fatal(e)
	}
	db, e := OpenDatabase(ctx, DatabaseOptions{Path: filepath.Join(home, "content.sqlite"), Kind: Content})
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	index, _ := NewContentIndex(db, "scope", nil)
	batches := 0
	projector, _ := NewContentProjector(ContentProjectorOptions{Index: index, Budget: sessionstore.NewBudget(), SpoolDir: home, OnBatch: func(count, bytes int) {
		batches++
		if count > 256 || bytes > ContentBatchBytes {
			t.Fatalf("Unbounded projection batch %d %d", count, bytes)
		}
	}})
	candidate := ContentCandidate{p, "project", "owner", p, 42}
	result, e := projector.Project(ctx, candidate)
	if e != nil || result.DocumentCount != 1800 || batches < 7 {
		t.Fatalf("Large transcript was not projected: %+v %v batches=%d", result, e, batches)
	}
	files, _ := os.ReadDir(home)
	for _, file := range files {
		if strings.HasPrefix(file.Name(), "claude-search-spool-") {
			t.Fatal("Spool was not removed")
		}
	}
	old, _ := index.GetSource(ctx, p)
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0600)
	_, _ = f.WriteString("{}\n")
	_ = f.Close()
	projector.o.Verify = func(context.Context, string, Fingerprint) SourceChange {
		return SourceChange{Kind: "retry", Reason: "changed-during-read"}
	}
	result, e = projector.Project(ctx, candidate)
	if e != nil || result.Kind != "retry" {
		t.Fatalf("Source verification bypassed: %+v %v", result, e)
	}
	current, _ := index.GetSource(ctx, p)
	if !reflect.DeepEqual(current, old) {
		t.Fatal("Changed source committed stale projection")
	}
}
