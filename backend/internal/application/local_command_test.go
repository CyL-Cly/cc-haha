package application

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

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func equalLocalJSON(a, b json.RawMessage) bool {
	if a == nil {
		a = json.RawMessage("null")
	}
	if b == nil {
		b = json.RawMessage("null")
	}
	if au, err := protocol.DecodeJavaScriptJSONString(a); err == nil {
		bu, err := protocol.DecodeJavaScriptJSONString(b)
		return err == nil && reflect.DeepEqual(au, bu)
	}
	var am, bm map[string]json.RawMessage
	if json.Unmarshal(a, &am) == nil && am != nil {
		if json.Unmarshal(b, &bm) != nil || len(am) != len(bm) {
			return false
		}
		for key, value := range am {
			other, ok := bm[key]
			if !ok || !equalLocalJSON(value, other) {
				return false
			}
		}
		return true
	}
	return bytes.Equal(bytes.TrimSpace(a), bytes.TrimSpace(b))
}

func TestActualSourceLocalCommandUnicodeTagsAndGoalOutput(t *testing.T) {
	bun := os.Getenv("CC_HAHA_PINNED_BUN")
	if bun == "" {
		t.Skip("actual Source comparison requires explicitly supplied Bun 1.3.14")
	}
	contents := []json.RawMessage{
		json.RawMessage(`null`), json.RawMessage(`42`), json.RawMessage(`{}`), json.RawMessage(`[]`), json.RawMessage(`""`),
		json.RawMessage(`[{"type":"thinking","text":"<local-command-stdout> first "},{"text":"second </local-command-stdout>"}]`),
		json.RawMessage(`[{"text":""},null,{"text":3},{"type":"other","text":"<command-name>/goal</command-name>"},{"text":""}]`),
		json.RawMessage(`"<local-command-stdout>\ud800😀\udfff</local-command-stdout>"`),
		json.RawMessage(`"<command-name>\ud800</command-name>"`),
	}
	for _, text := range []string{
		"\ufeff\t plain \u00a0\u3000", "\u0085", "<local-command-stdout> \n </local-command-stdout><local-command-stderr>stderr</local-command-stderr>",
		"<local-command-stderr> error </local-command-stderr>", "<local-command-stdout> first </local-command-stdout><local-command-stdout>second</local-command-stdout>",
		"<local-command-stdout attr='x'>attr</local-command-stdout>", "<LOCAL-COMMAND-STDOUT>case</LOCAL-COMMAND-STDOUT>",
		"<local-command-stdout>unclosed", "<command-name> / </command-name>", "<command-name>//goal</command-name>", "<command-name> </command-name>",
		"<command-name>/cost</command-name><local-command-stdout>Goal set: ordinary text</local-command-stdout>",
		"Goal cleared.", "Goal cleared: test", "Goal marked complete.", "No active goal.", "Goal continuing: test", "Goal set: objective", "Goal set:  ", "goal set: lower", "custom output",
		"<command-name>/goal</command-name><local-command-stdout>custom output</local-command-stdout>",
	} {
		contents = append(contents, raw(text))
	}
	type row struct {
		Content  json.RawMessage `json:"content"`
		Untagged bool            `json:"untagged"`
	}
	var rows []row
	for _, content := range contents {
		for _, untagged := range []bool{false, true} {
			rows = append(rows, row{content, untagged})
		}
	}
	input, _ := json.Marshal(rows)
	fixture, err := filepath.Abs("testdata/local-command.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	for _, leaf := range []string{"tmp", "config", "cache"} {
		if err := os.MkdirAll(filepath.Join(home, leaf), 0700); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, "config"), "TMPDIR=" + filepath.Join(home, "tmp"), "XDG_CONFIG_HOME=" + filepath.Join(home, "config"), "XDG_CACHE_HOME=" + filepath.Join(home, "cache"), "PATH=/usr/bin:/bin", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	command.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil || stderr.Len() != 0 {
		t.Fatalf("actual Source failed: %v %s", err, stderr.String())
	}
	var want []struct{ Command, Output, Goal json.RawMessage }
	if err := json.Unmarshal(output, &want); err != nil || len(want) != len(rows) {
		t.Fatalf("Source results: %v %s", err, output)
	}
	for i, row := range rows {
		parsed := parseLocalCommand(row.Content)
		var name json.RawMessage
		if parsed != nil {
			name = protocol.QuoteJavaScriptUTF16(parsed.name)
		}
		expectedName := obj(want[i].Command).Get("name")
		got := localCommandOutput(row.Content, row.Untagged)
		goal := localGoalEvent(got, parsed)
		orderedGoal := goal
		if orderedGoal == nil {
			orderedGoal = json.RawMessage("null")
		}
		if !equalLocalJSON(name, expectedName) || !equalLocalJSON(got, want[i].Output) || !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(orderedGoal), protocol.StringifyValidatedJavaScriptJSON(want[i].Goal)) {
			t.Fatalf("row %d content=%s untagged=%v\nname=%s Source=%s\noutput=%s Source=%s\ngoal=%s Source=%s", i, row.Content, row.Untagged, name, expectedName, got, want[i].Output, goal, want[i].Goal)
		}
	}
}

func TestLocalCommandProjectionRetainsPendingThroughResultAndUserOutput(t *testing.T) {
	p := NewProjection()
	if got := project(t, p, `{"type":"system","subtype":"local_command","content":"<command-name>/goal</command-name>"}`); len(got) != 0 {
		t.Fatal(got)
	}
	project(t, p, `{"type":"result","usage":{}}`)
	got := project(t, p, `{"type":"user","message":{"content":[{"type":"text","text":"<local-command-stdout> custom goal output </local-command-stdout>"},{"type":"tool_result","tool_use_id":"x","content":"actual result"}]}}`)
	if len(got) != 2 || obj(got[0]).String("subtype") != "goal_event" || obj(obj(got[0]).Get("data")).String("action") != "message" || obj(got[1]).String("type") != "tool_result" {
		t.Fatal(string(raw(got)))
	}
	if p.localCommand != nil {
		t.Fatal("pending command not consumed")
	}
	project(t, p, `{"type":"system","subtype":"local_command","content":null,"message":"<command-name>/</command-name>"}`)
	got = project(t, p, `{"type":"system","subtype":"local_command_output","content":"Goal set: ordinary slash output"}`)
	if len(got) != 2 || obj(got[1]).String("text") != "Goal set: ordinary slash output" {
		t.Fatal("empty raw slash name did not suppress goal detection", string(raw(got)))
	}
	project(t, p, `{"type":"system","subtype":"local_command","content":"<command-name>/goal</command-name>"}`)
	got = project(t, p, `{"type":"user","message":{"content":"<local-command-stdout>Compacted</local-command-stdout>"}}`)
	if len(got) != 0 || p.localCommand != nil {
		t.Fatal("compact output leaked", string(raw(got)))
	}
	got = project(t, p, `{"type":"system","subtype":"local_command_output","content":"<local-command-stdout>\ud800</local-command-stdout>"}`)
	if len(got) != 2 || !equalLocalJSON(obj(got[1]).Get("text"), json.RawMessage(`"\ud800"`)) {
		t.Fatal("UTF16 local output lost", string(raw(got)))
	}
}
