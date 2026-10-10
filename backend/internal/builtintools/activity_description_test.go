package builtintools

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeActivityDescriptionsMatchActualSourceBuiltins(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	cwd := filepath.Join(home, "project")
	temp := filepath.Join(home, "tmp")
	_ = os.MkdirAll(cwd, 0700)
	_ = os.MkdirAll(temp, 0700)
	rows := []map[string]string{}
	for _, name := range []string{"Agent", "Bash", "Read", "Write", "Edit", "NotebookEdit", "Glob", "Grep", "WebFetch", "WebSearch", "UnknownSourceTool"} {
		rows = append(rows, map[string]string{"name": name, "input": `{}`})
		for _, value := range []string{"short source value", strings.Repeat("x", 65), strings.Repeat("界👨‍👩‍👧é", 15)} {
			input, _ := json.Marshal(map[string]any{"file_path": filepath.Join(cwd, value), "notebook_path": filepath.Join(home, "outside", value), "pattern": value, "query": value, "url": value, "command": value, "description": value})
			rows = append(rows, map[string]string{"name": name, "input": string(input)})
		}
	}
	rows = append(rows, map[string]string{"name": "Bash", "input": `{"command":"` + strings.Repeat("界👨‍👩‍👧é", 15) + `"}`}, map[string]string{"name": "Bash", "input": `{"command":"actual","description":""}`}, map[string]string{"name": "Read", "input": `{"file_path":"../outside/file.txt"}`}, map[string]string{"name": "Read", "input": `{"file_path":"."}`}, map[string]string{"name": "Read", "input": `{"file_path":"` + home + `/other/file.txt"}`})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-background-tasks.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + temp, "CLAUDE_CODE_TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_BACKGROUND_TASK_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "cwd": cwd, "activities": rows})
	cmd.Stdin = bytes.NewReader(payload)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.1800s", e, out)
	}
	var source struct {
		Activities   []json.RawMessage
		OutputDir    string
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Activities) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source shape %v %.1800s", e, out)
	}
	for i, row := range rows {
		var in protocol.JSONObject
		_ = in.UnmarshalJSON([]byte(row["input"]))
		got, known, e := NativeActivityDescription(row["name"], in, home, cwd, source.OutputDir)
		if !known {
			got = []byte("null")
		}
		if e != nil || string(protocol.StringifyValidatedJavaScriptJSON(got)) != string(protocol.StringifyValidatedJavaScriptJSON(source.Activities[i])) {
			t.Errorf("%s[%d] Go%s/%v Source%s", row["name"], i, got, e, source.Activities[i])
		}
	}
}
