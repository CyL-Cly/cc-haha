package sessionhistory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeSessionBranchActualOriginalHistoryAndPhysicalFiles(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	const sid = "60000000-0000-4000-8000-000000000001"
	const now = "2026-10-07T08:09:10.000Z"
	user := func(id, parent, text, stamp string) string {
		raw, _ := json.Marshal(map[string]any{"type": "user", "uuid": id, "parentUuid": parent, "sessionId": sid, "timestamp": stamp, "cwd": "/fixture/work", "message": map[string]any{"role": "user", "content": text}, "future": map[string]any{"kept": true}})
		return string(raw) + "\n"
	}
	assistant := func(id, parent, text, stamp string) string {
		raw, _ := json.Marshal(map[string]any{"type": "assistant", "uuid": id, "parentUuid": parent, "sessionId": sid, "timestamp": stamp, "message": map[string]any{"id": "api-" + id, "role": "assistant", "content": []any{map[string]any{"type": "text", "text": text}}}})
		return string(raw) + "\n"
	}
	first := user("u1", "", "  first\n\t👋 prompt  ", "2026-10-01T00:00:00Z")
	main := first + assistant("a1", "u1", "before", "2026-10-01T00:00:01Z") + user("u2", "a1", "later", "2026-10-01T00:00:02Z") + assistant("a2", "u2", "done", "2026-10-01T00:00:03Z")
	type sample struct {
		Content string         `json:"content"`
		Options map[string]any `json:"options"`
		Titles  []string       `json:"titles,omitempty"`
		Missing bool           `json:"missing,omitempty"`
	}
	cases := []sample{
		{Content: main, Options: map[string]any{"targetMessageId": "a1"}},
		{Content: main, Options: map[string]any{"title": "custom [name]", "sourceWorkDir": "/source/work", "sourceRepository": map[string]any{"future": 4}, "sourceWorktreeSession": map[string]any{"sessionId": sid, "worktreePath": "/fixture/tree"}}, Titles: []string{"custom [name] (Branch)", "custom [name] (Branch 02)", "custom [name] (Branch 4)"}},
		{Content: main, Options: map[string]any{"targetMessageId": "unknown"}},
		{Content: "", Options: map[string]any{}},
		{Missing: true, Options: map[string]any{}},
		{Content: main + `{"type":"user","uuid":"side","parentUuid":null,"sessionId":"` + sid + `","timestamp":"2027-01-01T00:00:00Z","isSidechain":true,"message":{"role":"user","content":"side"}}` + "\n", Options: map[string]any{"targetMessageId": "side"}},
		{Content: main + assistant("old-leaf", "a1", "fork-old", "2026-09-01T00:00:00Z"), Options: map[string]any{}},
		{Content: first + `{"type":"progress","uuid":"progress","parentUuid":"u1"}` + "\n" + assistant("a1", "progress", "bridged", "2026-10-01T00:00:02Z"), Options: map[string]any{}},
		{Content: user("u1", "", strings.Repeat("界", 99)+"👋tail", "2026-10-01T00:00:00Z") + assistant("a1", "u1", "done", "2026-10-01T00:00:01Z"), Options: map[string]any{}},
		{Content: main + `{"type":"system","uuid":"snip","parentUuid":"a2","timestamp":"2026-10-01T00:00:04Z","snipMetadata":{"removedUuids":["u2"]}}` + "\n", Options: map[string]any{}},
	}
	metadata := `{"type":"session-meta","isMeta":true,"workDir":"/metadata/work","future":{"keep":1}}` + "\n" + `{"type":"worktree-state","sessionId":"` + sid + `","worktreeSession":{"sessionId":"` + sid + `","path":"/tree","future":2}}` + "\n" + `{"type":"mode","sessionId":"` + sid + `","mode":"plan","future":3}` + "\n" + `{"type":"pr-link","sessionId":"` + sid + `","prNumber":4,"future":5}` + "\n" + `{"type":"file-history-snapshot","messageId":"u1","snapshot":{"messageId":"u1","trackedFileBackups":{}},"future":6}` + "\n" + `{"type":"file-history-snapshot","messageId":"u2","snapshot":{"messageId":"u2"}}` + "\n" + `{"type":"attribution-snapshot","messageId":"u1","future":7}` + "\n" + `{"type":"tag","sessionId":"` + sid + `","tag":"do not copy"}` + "\n"
	cases = append(cases, sample{Content: metadata + main, Options: map[string]any{"targetMessageId": "a1", "sourceWorkDir": "/launch/work"}})
	tool := first + `{"type":"assistant","uuid":"tool","parentUuid":"u1","sessionId":"` + sid + `","timestamp":"2026-10-01T00:00:01Z","message":{"id":"api-tool","role":"assistant","content":[{"type":"tool_use","id":"call-keep","name":"Read","input":{}}]}}` + "\n" + `{"type":"user","uuid":"result","parentUuid":"tool","sessionId":"` + sid + `","timestamp":"2026-10-01T00:00:02Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call-keep","content":"read"}]}}` + "\n" + assistant("a2", "result", "done", "2026-10-01T00:00:03Z") + `{"type":"content-replacement","sessionId":"` + sid + `","replacements":[{"toolUseId":"call-keep","content":"kept","future":7},{"toolUseId":"call-drop","content":"dropped"}]}` + "\n"
	cases = append(cases, sample{Content: tool, Options: map[string]any{}}, sample{Content: tool, Options: map[string]any{"targetMessageId": "tool"}})
	sourceHome := t.TempDir()
	if e := os.MkdirAll(filepath.Join(sourceHome, "tmp"), 0700); e != nil {
		t.Fatal(e)
	}
	input, _ := json.Marshal(map[string]any{"home": sourceHome, "sessionId": sid, "now": now, "cases": cases})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-session-branch.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = sourceHome
	cmd.Env = []string{"HOME=" + sourceHome, "USERPROFILE=" + sourceHome, "CLAUDE_CONFIG_DIR=" + filepath.Join(sourceHome, ".claude"), "TMPDIR=" + filepath.Join(sourceHome, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(sourceHome), "CC_HAHA_SESSION_BRANCH_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "DISABLE_TELEMETRY=1", "ANTHROPIC_API_KEY=fake-branch-key", "ANTHROPIC_BASE_URL=http://127.0.0.1:1"}
	cmd.Stdin = bytes.NewReader(input)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("actual branch Source: %v %.2500s", e, out)
	}
	var expected struct {
		Results []struct {
			Result BranchResult `json:"result"`
			Disk   string       `json:"disk"`
			Error  *BranchError `json:"error"`
		} `json:"results"`
	}
	if e = json.Unmarshal(out, &expected); e != nil || len(expected.Results) != len(cases) {
		t.Fatalf("receipt: %v %.2000s", e, out)
	}
	clock, _ := time.Parse(time.RFC3339, now)
	for i, row := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, sid+".jsonl")
			if !row.Missing {
				if e := os.WriteFile(path, []byte(row.Content), 0600); e != nil {
					t.Fatal(e)
				}
			}
			for j, title := range row.Titles {
				data, _ := json.Marshal(map[string]any{"type": "custom-title", "customTitle": title})
				if e := os.WriteFile(filepath.Join(dir, fmt.Sprintf("sibling-%d.jsonl", j)), append(data, '\n'), 0600); e != nil {
					t.Fatal(e)
				}
			}
			loader, _ := nativeLoader(t)
			x := expected.Results[i]
			rawOption := func(name string) json.RawMessage {
				v, ok := row.Options[name]
				if !ok {
					return nil
				}
				raw, _ := json.Marshal(v)
				return raw
			}
			target, _ := row.Options["targetMessageId"].(string)
			got, e := loader.Branch(BranchOptions{SourceSessionID: sid, SourceTranscriptPath: path, TargetMessageID: target, Title: rawOption("title"), SourceWorkDir: rawOption("sourceWorkDir"), SourceRepository: rawOption("sourceRepository"), SourceWorktreeSession: rawOption("sourceWorktreeSession"), UUID: func() (string, error) { return x.Result.SessionID, nil }, Now: func() time.Time { return clock }})
			if x.Error != nil {
				var failure *BranchError
				if !errors.As(e, &failure) || !reflect.DeepEqual(failure, x.Error) {
					t.Fatalf("Source error %#v != %v", x.Error, e)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			data, e := os.ReadFile(got.ForkPath)
			if e != nil {
				t.Fatal(e)
			}
			if string(data) != x.Disk {
				t.Fatalf("physical Source fork drift:\nGo %s\nSource %s", data, x.Disk)
			}
			x.Result.ForkPath = got.ForkPath
			actual, _ := json.Marshal(got)
			wanted, _ := json.Marshal(x.Result)
			if !bytes.Equal(actual, wanted) {
				t.Fatalf("result drift Go %s Source %s", actual, wanted)
			}
			old, e := os.ReadFile(path)
			if e != nil || string(old) != row.Content {
				t.Fatal("source transcript mutated", e)
			}
			stat, e := os.Stat(got.ForkPath)
			if e != nil || stat.Mode().Perm() != 0600 {
				t.Fatal("fork mode", e)
			}
			// No agent work, sidechain or later records may be copied as a shortcut.
			if !bytes.Contains(data, []byte(`"forkedFrom"`)) || !bytes.Contains(data, []byte(`"parentUuid":null`)) {
				t.Fatal("missing original provenance or root")
			}
			if _, e = protocol.ParseAndStringifyJavaScriptJSON(data[:bytes.IndexByte(data, '\n')]); e != nil {
				t.Fatal(e)
			}
		})
	}
}
