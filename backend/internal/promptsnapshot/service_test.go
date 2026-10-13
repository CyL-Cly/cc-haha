package promptsnapshot

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func lines(t *testing.T, path string) []map[string]any {
	t.Helper()
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	out := []map[string]any{}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var v map[string]any
		if e := json.Unmarshal([]byte(line), &v); e != nil {
			t.Fatal(e)
		}
		out = append(out, v)
	}
	return out
}
func TestSnapshotsBlobBeforeMarkerDedupContextScopeAndClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session", "prompt-snapshots.jsonl")
	s := New(nil, nil)
	body := json.RawMessage(`{"model":"fixture","system":[{"type":"text","text":"system"}],"tools":[{"name":"Read","description":"read file","input_schema":{"type":"object"}}]}`)
	for n := 0; n < 4; n++ {
		s.RecordRequest(path, "", "sdk", body)
		s.RecordContext(path, "", map[string]string{"date": "2026-10-12", "instructions": "fixture"})
	}
	s.RecordRequest(path, "child", "agent:custom", body)
	if e := s.Drain(context.Background()); e != nil {
		t.Fatal(e)
	}
	records := lines(t, path)
	blobs := map[string]bool{}
	req, ctx := 0, 0
	for _, v := range records {
		switch v["t"] {
		case "blob":
			blobs[v["h"].(string)] = true
		case "req":
			req++
			if !blobs[v["sys"].(string)] || !blobs[v["tools"].(string)] {
				t.Fatal("marker before referenced blobs", v)
			}
		case "ctx":
			ctx++
			if !blobs[v["ctx"].(string)] {
				t.Fatal("context marker before referenced blob", v)
			}
		}
	}
	if req != 2 || ctx != 1 || len(records) != 6 {
		t.Fatalf("dedup/scope failure: %+v", records)
	}
	if e := s.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	s.RecordRequest(path, "", "sdk", json.RawMessage(`{"model":"new"}`))
	if len(lines(t, path)) != 6 {
		t.Fatal("closed recorder wrote again")
	}
}
func TestSnapshotBoundedFailureBudgetDoesNotDisableAnotherSession(t *testing.T) {
	root := t.TempDir()
	badDir := filepath.Join(root, "not-directory")
	os.WriteFile(badDir, []byte("file"), 0600)
	bad := filepath.Join(badDir, "snapshot.jsonl")
	failures := new(atomic.Int32)
	s := New(nil, func(error) { failures.Add(1) })
	defer s.Close(context.Background())
	body := json.RawMessage(`{"model":"fixture","system":"system","tools":[]}`)
	for n := 0; n < 3; n++ {
		s.RecordRequest(bad, "", "sdk", body)
		s.Drain(context.Background())
	}
	if failures.Load() != 3 {
		t.Fatal(failures.Load())
	}
	os.Remove(badDir)
	os.Mkdir(badDir, 0700)
	s.RecordRequest(bad, "", "sdk", body)
	good := filepath.Join(root, "good", "snapshot.jsonl")
	s.RecordRequest(good, "", "sdk", body)
	s.Drain(context.Background())
	if _, e := os.Stat(bad); !os.IsNotExist(e) {
		t.Fatal("failure-disabled scope revived", e)
	}
	if len(lines(t, good)) != 3 {
		t.Fatal("another runtime's snapshots disabled")
	}
}
func TestSnapshotLargeBlobsBoundedAndConcurrentShutdown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshots.jsonl")
	s := New(nil, nil)
	body := raw(map[string]any{"model": "fixture", "system": strings.Repeat("s", MaxBlobChars+100), "tools": []any{map[string]any{"name": "large", "description": "description", "input_schema": map[string]any{"enum": []string{strings.Repeat("x", MaxBlobChars+100)}}}}})
	s.RecordRequest(path, "", "sdk", body)
	s.RecordContext(path, "", map[string]string{"large": strings.Repeat("c", MaxBlobChars+10)})
	s.Drain(context.Background())
	for _, v := range lines(t, path) {
		if v["t"] == "blob" && v["truncated"] != true {
			t.Fatal("huge blob did not declare truncation", v["k"])
		}
		if v["k"] == "sys" && length(v["text"].(string)) != MaxBlobChars {
			t.Fatal("system truncation size")
		}
	}
	var wg sync.WaitGroup
	for n := 0; n < 20; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.RecordRequest(path, "", "sdk", json.RawMessage(`{"model":"new","tools":[]}`))
		}()
	}
	s.Close(context.Background())
	wg.Wait()
}
func TestSnapshotEntrypointAndDisableFlag(t *testing.T) {
	for _, env := range []map[string]string{{}, {"CLAUDE_CODE_ENTRYPOINT": "cli"}, {"CLAUDE_CODE_ENTRYPOINT": "claude-desktop", "CC_HAHA_PROMPT_SNAPSHOT": " OFF "}} {
		if Enabled(env) {
			t.Fatal(env)
		}
	}
	if !Enabled(map[string]string{"CLAUDE_CODE_ENTRYPOINT": "claude-desktop"}) {
		t.Fatal("desktop snapshot disabled by default")
	}
}
