package application

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceTerminalConversationCopyCodeExportNamesAndRenameContext(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("pinned actual Source Bun required")
	}
	row := func(messages string, markdown, language string) map[string]any {
		return map[string]any{"messages": json.RawMessage(messages), "markdown": markdown, "language": language}
	}
	rows := []map[string]any{
		row(`[]`, "plain text", "plaintext"),
		row(`[{"type":"user","message":{"content":" Hello World!\nsecond "}},{"type":"assistant","message":{"content":[{"type":"text","text":"first"},{"type":"text","text":"second"}]}},{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Write"}]}},{"type":"assistant","isApiErrorMessage":true,"message":{"content":[{"type":"text","text":"api error"}]}}]`, "before\n```go\nfmt.Println(\"one\")\n```\n\n~~~python\nprint('two')\n~~~", "../../python"),
		row(`[{"type":"user","isMeta":true,"message":{"content":"meta"}},{"type":"user","origin":{"kind":"task"},"message":{"content":"task"}},{"type":"user","message":{"content":[{"type":"image"},{"type":"text","text":"　Camel Case　"},{"type":"text","text":"ignored filename"}]}},{"type":"assistant","message":{"content":[{"type":"text","text":"answer"}]}}]`, "<context>hidden</context>\n\n    indented code\n    second line\n\n> ```quoted\n> skipped\n> ```", "js title"),
	}
	var messages []protocol.JSONObject
	for i := 0; i < 24; i++ {
		messages = append(messages, protocol.NewJSONObject(field("type", "assistant"), field("message", protocol.NewJSONObject(field("content", []any{map[string]string{"type": "text", "text": strings.Repeat("X", 50)}})))))
	}
	rows = append(rows, map[string]any{"messages": messages, "markdown": "```plaintext\nfirst\n\n\n```\n", "language": ""})
	home := t.TempDir()
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-conversation.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_CONVERSATION_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "USER_TYPE=external", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "rows": rows}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal("actual Source conversation helper", err, string(output))
	}
	var source struct {
		Results []struct {
			Recent                                  []string
			Blocks                                  []struct{ Code, Lang string }
			Extension, FirstPrompt, Slug, NameInput string
		}
		Caps     struct{ Blocked, SecureReads []string }
		TagCalls []struct {
			Args    string
			Help    bool
			TagName string
		}
		TagGate []struct {
			User    string
			Enabled bool
		}
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Results) != len(rows) || len(source.Caps.Blocked) != 0 || len(source.Caps.SecureReads) != 0 {
		t.Fatal("Source fixture capability/result", err, string(output))
	}
	if len(source.TagCalls) != 21 || len(source.TagGate) != 2 || source.TagGate[0].Enabled || !source.TagGate[1].Enabled {
		t.Fatal("actual tag Source gate/caller evidence missing", source.TagGate, len(source.TagCalls))
	}
	for _, call := range source.TagCalls {
		arg := protocol.TrimJavaScriptString(call.Args)
		if terminalTagHelpArgument(arg) != call.Help || !call.Help && call.TagName != arg {
			t.Fatal("tag actual Source caller argument branch mismatch", call, arg)
		}
	}
	for index, row := range rows {
		var messages []protocol.JSONObject
		if err = json.Unmarshal(raw(row["messages"]), &messages); err != nil {
			t.Fatal(err)
		}
		want := source.Results[index]
		blocks := terminalCopyCodeBlocks(row["markdown"].(string))
		var expected []terminalCopyCode
		for _, block := range want.Blocks {
			expected = append(expected, terminalCopyCode{block.Code, block.Lang})
		}
		if expected == nil {
			expected = []terminalCopyCode{}
		}
		prompt := terminalFirstExportPrompt(messages)
		nameInput, err := clibootstrap.TerminalConversationNameInput(messages)
		if err != nil {
			t.Fatal(err)
		}
		var nameText string
		if nameInput != nil {
			_ = json.Unmarshal(nameInput, &nameText)
		}
		if !reflect.DeepEqual(terminalAssistantTexts(messages), want.Recent) || !reflect.DeepEqual(blocks, expected) || "copy"+want.Extension != terminalCopyFilename(row["language"].(string)) || prompt != want.FirstPrompt || terminalExportSlug(prompt) != want.Slug || nameText != want.NameInput {
			t.Fatalf("Source row %d mismatch blocks Go=%s Source=%s prompt=%q/%q slug=%q/%q name=%q/%q", index, raw(blocks), raw(expected), prompt, want.FirstPrompt, terminalExportSlug(prompt), want.Slug, nameText, want.NameInput)
		}
	}
}
