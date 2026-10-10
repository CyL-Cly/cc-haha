package errorlog

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestErrorMemoryPrivacyAndQueuedSinkMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	profiles := []map[string]any{}
	environments := []map[string]string{{}, {"DISABLE_TELEMETRY": "1"}, {"DISABLE_ERROR_REPORTING": "0"}, {"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "false"}, {"NODE_ENV": "test"}}
	for _, name := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_AZURE_OPENAI"} {
		for _, value := range []string{"1", "true", " YES ", "0", "false", ""} {
			environments = append(environments, map[string]string{name: value})
		}
	}
	for _, env := range environments {
		profiles = append(profiles, map[string]any{"environment": env, "operations": []map[string]any{{"message": "first"}, {"message": "second", "name": "TypeError", "stack": "private original stack"}, {"attach": true}, {"attach": true}, {"message": "third"}}})
	}
	operations := []map[string]any{}
	for i := 0; i < 105; i++ {
		operations = append(operations, map[string]any{"message": string(rune('A' + i))})
	}
	operations = append(operations, map[string]any{"attach": true})
	profiles = append(profiles, map[string]any{"environment": map[string]string{}, "operations": operations})
	profiles = append(profiles, map[string]any{"environment": map[string]string{}, "operations": []map[string]any{{"attach": true, "throwSink": true}, {"message": "recoverable sink failure"}, {"message": "second retained error"}}})
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-error-log.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_ERROR_LOG_FIXTURE=1", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"profiles": profiles})
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var source struct {
		Rows []struct {
			Memory []Row
			Sink   []Error
		}
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Rows) != len(profiles) {
		t.Fatal(e, string(out))
	}
	for i, p := range profiles {
		s, _ := New(Options{Environment: func() map[string]string { return p["environment"].(map[string]string) }, Now: func() time.Time { return time.UnixMilli(1700000000000) }})
		sink := []Error{}
		for _, op := range p["operations"].([]map[string]any) {
			if op["attach"] == true {
				s.Attach(func(err error) {
					sink = append(sink, Details(err))
					if op["throwSink"] == true {
						panic("private sink failed")
					}
				})
			} else {
				message, _ := op["message"].(string)
				name, _ := op["name"].(string)
				if name == "" {
					name = "Error"
				}
				stack, _ := op["stack"].(string)
				s.Log(Error{Name: name, Message: message, Stack: stack})
			}
		}
		if !reflect.DeepEqual(s.Memory(), source.Rows[i].Memory) || !reflect.DeepEqual(sink, source.Rows[i].Sink) {
			t.Errorf("profile%d Go memory%v sink%v Source%v", i, s.Memory(), sink, source.Rows[i])
		}
	}
}
