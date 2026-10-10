package builtintools

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestTaskFormattingNotificationAndPromptMatchOriginalSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	s, _, home, _ := fixture(t)
	temp := filepath.Join(home, "tmp")
	_ = os.MkdirAll(temp, 0700)
	maps := []map[string]any{}
	for _, limit := range []string{"", "96", "1", "0", "12garbage", " +120 ", "-2", "0x100", "99999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999"} {
		maps = append(maps, map[string]any{"name": "TaskOutput", "limit": limit, "data": `{"retrieval_status":"success","task":{"task_id":"bfixture","task_type":"local_bash","status":"failed","exitCode":7,"output":"  \ud800😀abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz\ufeff ","error":"actual error"}}`})
	}
	for _, canRead := range []bool{false, true} {
		for _, skipped := range []string{"", "Source isolation unavailable"} {
			data, _ := json.Marshal(map[string]any{"isAsync": true, "status": "async_launched", "agentId": "actual-source-agent", "description": "actual source task", "prompt": "original source prompt", "outputFile": "/private/source-owned.output", "canReadOutputFile": canRead, "worktreeIsolationSkipped": skipped})
			maps = append(maps, map[string]any{"name": "Agent", "limit": "", "data": string(data)})
		}
	}
	shells := []map[string]any{}
	for _, code := range []int{0, 7} {
		shells = append(shells, map[string]any{"id": "bfixture", "description": "<actual & \"quoted\">", "command": "printf actual", "toolUseId": "original-shell-use", "code": code})
	}
	shells = append(shells, map[string]any{"id": "bfixture", "description": "owned shell", "command": "printf scoped", "toolUseId": "original-shell-use", "code": 0, "agentId": "aowner"})
	prompts := []string{"Continue? ", "undo you want?", "Do you want?", "Ready to continue?", "hello\nnot a prompt", "Do you\nnot question?", "press ENTER", "y/n", "\ufeff(Y/n)\ufeff", "Would your task?"}
	path, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-background-tasks.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", path)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + temp, "CLAUDE_CODE_TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_BACKGROUND_TASK_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "maps": maps, "shells": shells, "prompts": prompts})
	cmd.Stdin = bytes.NewReader(payload)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.2000s", e, out)
	}
	var source struct {
		OutputDir  string
		Mapped     []json.RawMessage
		ShellCases []struct {
			Started       []json.RawMessage
			Notifications []struct{ Value, Mode, Priority, AgentID string }
			Status        string
			Notified      bool
		}
		Prompts      []bool
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Mapped) != len(maps) || len(source.ShellCases) != len(shells) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source shape %v %.2000s", e, out)
	}
	s.options.BackgroundTasks = &BackgroundTaskBindings{OutputDir: source.OutputDir}
	for i, row := range maps {
		s.options.Environment["TASK_MAX_OUTPUT_LENGTH"] = row["limit"].(string)
		var got protocol.JSONObject
		if row["name"] == "Agent" {
			var data protocol.JSONObject
			_ = data.UnmarshalJSON([]byte(row["data"].(string)))
			got, e = mapAgent(data, quote("actual-task-use"))
		} else {
			got, e = s.mapBackgroundTask("TaskOutput", []byte(row["data"].(string)), quote("actual-task-use"))
		}
		if e != nil || string(protocol.StringifyValidatedJavaScriptJSON(raw(got))) != string(protocol.StringifyValidatedJavaScriptJSON(source.Mapped[i])) {
			t.Errorf("map%d Go%s %v Source%s", i, raw(got), e, source.Mapped[i])
		}
	}
	for i, row := range shells {
		want := source.ShellCases[i]
		if len(want.Notifications) != 1 {
			t.Fatalf("original notification%d missing %+v", i, want)
		}
		code := row["code"].(int)
		status := "completed"
		if code != 0 {
			status = "failed"
		}
		got := shellNotification("bfixture", row["description"].(string), status, &code, quote("original-shell-use"), filepath.Join(source.OutputDir, "bfixture.output"))
		if got != want.Notifications[0].Value || want.Notifications[0].Mode != "task-notification" || want.Notifications[0].Priority != "later" {
			t.Errorf("notification%d Go%s Source%+v", i, got, want.Notifications[0])
		}
	}
	for i, prompt := range prompts {
		if got := looksLikeTaskPrompt(prompt); got != source.Prompts[i] {
			t.Errorf("prompt %q Go%v Source%v", prompt, got, source.Prompts[i])
		}
	}
}
func TestTaskOutputCannotFollowFinalSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("original O_NOFOLLOW is Unix-only")
	}
	home := t.TempDir()
	target := filepath.Join(home, "protected")
	if e := os.WriteFile(target, []byte("retained"), 0600); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(home, "task.output")
	if e := os.Symlink(target, path); e != nil {
		t.Fatal(e)
	}
	f, e := openTaskOutput(path)
	if e == nil {
		_ = f.Close()
		t.Fatal("followed output symlink")
	}
	bytes, e := os.ReadFile(target)
	if e != nil || string(bytes) != "retained" {
		t.Fatal("protected target changed", e)
	}
}
