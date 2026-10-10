package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestRestoredHistoryFullscreenAndVisibilityMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Actual Source Bun required")
	}
	profiles := []map[string]any{}
	for _, user := range []string{"", "ant", "external"} {
		for _, flag := range []string{"", "0", "false", " NO ", "off", "1", "TRUE", " yes ", "on", "unknown"} {
			profiles = append(profiles, map[string]any{"environment": map[string]string{"USER_TYPE": user, "CLAUDE_CODE_NO_FLICKER": flag}})
		}
	}
	for _, row := range []map[string]any{
		{"environment": map[string]string{"USER_TYPE": "ant", "TMUX": "owned", "TERM_PROGRAM": "iTerm.app", "TERM": "xterm"}},
		{"environment": map[string]string{"USER_TYPE": "ant", "TMUX": "owned", "TERM_PROGRAM": "iTerm.app", "TERM": "screen-256color"}},
		{"environment": map[string]string{"USER_TYPE": "ant", "TMUX": "owned", "TERM_PROGRAM": "tmux"}},
		{"environment": map[string]string{"USER_TYPE": "ant", "TMUX": "owned"}, "output": " 1\n"},
		{"environment": map[string]string{"USER_TYPE": "ant", "TMUX": "owned"}, "output": "0\n"},
		{"environment": map[string]string{"USER_TYPE": "ant", "TMUX": "owned"}, "output": "1\n", "error": true},
		{"environment": map[string]string{"USER_TYPE": "ant", "TMUX": "owned", "CLAUDE_CODE_NO_FLICKER": "1"}, "output": "1"},
	} {
		profiles = append(profiles, row)
	}
	records := []protocol.JSONObject{}
	for _, source := range []string{
		`{"type":"user","uuid":"10000000-0000-4000-8000-000000000001","message":{"role":"user","content":"old user 中文"}}`,
		`{"type":"assistant","uuid":"10000000-0000-4000-8000-000000000002","message":{"role":"assistant","content":[{"type":"text","text":"old assistant"},{"type":"thinking","thinking":"retained thinking","signature":"original"}]}}`,
		`{"type":"user","uuid":"10000000-0000-4000-8000-000000000003","isMeta":true,"message":{"role":"user","content":"MODEL_ONLY_META"}}`,
		`{"type":"user","uuid":"10000000-0000-4000-8000-000000000004","isVisibleInTranscriptOnly":true,"message":{"role":"user","content":"transcript only"}}`,
		`{"type":"assistant","uuid":"10000000-0000-4000-8000-000000000005","message":{"role":"assistant","content":[{"type":"tool_use","id":"read-original","name":"Read","input":{"file_path":"/private/owned.txt","future":{"keep":true}}}]}}`,
		`{"type":"user","uuid":"10000000-0000-4000-8000-000000000006","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"read-original","content":"1→private\n<system-reminder>MODEL_ONLY_REMINDER</system-reminder>"}]},"toolUseResult":{"type":"text","file":{"filePath":"/private/owned.txt","numLines":1,"content":"private","startLine":1,"totalLines":1}}}`,
		`{"type":"user","uuid":"10000000-0000-4000-8000-000000000007","message":{"role":"user","content":"   "}}`,
		`{"type":"assistant","uuid":"10000000-0000-4000-8000-000000000008","message":{"role":"assistant","content":[{"type":"text","text":"(no content)"}]}}`,
	} {
		var record protocol.JSONObject
		if err := record.UnmarshalJSON([]byte(source)); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	histories := []map[string]any{{"records": records, "fullscreen": false, "transcript": false}, {"records": records, "fullscreen": true, "transcript": true}}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-tui-history.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TUI_HISTORY_FIXTURE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1"}
	input, _ := json.Marshal(map[string]any{"home": home, "fullscreen": profiles, "histories": histories})
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Source history %v %.4000s", err, out)
	}
	var source struct {
		Fullscreen []struct {
			Enabled bool
			Probes  int
		}
		Histories [][]struct {
			Kind, Block, Text, ID  string
			Input, Content, Result json.RawMessage
			IsError                bool
		}
		Blocked []string
	}
	if err := json.Unmarshal(out, &source); err != nil || len(source.Fullscreen) != len(profiles) || len(source.Histories) != len(histories) || len(source.Blocked) != 0 {
		t.Fatalf("Source shape %v %.4000s", err, out)
	}
	for i, row := range profiles {
		probes := 0
		got := FullscreenEnabled(row["environment"].(map[string]string), func() (string, error) {
			probes++
			if row["error"] == true {
				return "", errors.New("private tmux probe unavailable")
			}
			output, _ := row["output"].(string)
			return output, nil
		})
		if got != source.Fullscreen[i].Enabled || probes != source.Fullscreen[i].Probes {
			t.Fatal("Actual Source fullscreen branch", i, got, probes, source.Fullscreen[i])
		}
	}
	before, _ := json.Marshal(records)
	for i, row := range histories {
		s := NewState(nil, 120, 80)
		s.SetInitialHistory(records, row["fullscreen"].(bool))
		s.Transcript = row["transcript"].(bool)
		for _, expected := range source.Histories[i] {
			if expected.Block != "text" && expected.Block != "thinking" {
				continue
			}
			if expected.Block == "thinking" && !s.Transcript {
				continue
			}
			if !strings.Contains(s.View("", row["fullscreen"].(bool)).Content, expected.Text) {
				t.Fatal("Original visible history block absent", i, expected.Text, s.Entries)
			}
		}
		view := s.View("", row["fullscreen"].(bool)).Content
		if strings.Contains(view, "MODEL_ONLY_META") || strings.Contains(view, "MODEL_ONLY_REMINDER") || strings.Contains(view, "(no content)") || !strings.Contains(view, "Read 1 line") || (strings.Contains(view, "transcript only") != s.Transcript) {
			t.Fatal("Source visibility/tool result presentation", i, view)
		}
		tool := s.Entries[len(s.Entries)-1]
		var blocks []protocol.JSONObject
		_ = json.Unmarshal(presentationObject(records[4].Get("message")).Get("content"), &blocks)
		if tool.ToolID != "read-original" || tool.ToolName != "Read" || len(blocks) != 1 || string(tool.Input) != string(blocks[0].Get("input")) || string(tool.Result) != string(records[5].Get("toolUseResult")) {
			t.Fatal("History renderer changed original tool bytes", tool)
		}
	}
	after, _ := json.Marshal(records)
	if !bytes.Equal(before, after) {
		t.Fatal("Rendering mutated original restored envelopes")
	}
}
