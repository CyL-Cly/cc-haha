package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestMessageSelectorFilterAndResubmitAgainstActualFrozenSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	rows := []protocol.JSONObject{}
	for _, row := range []string{
		`{"type":"user","message":{"content":"  Original Chinese 用户任务 \ufeff"}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"assistant"}]}}`,
		`{"type":"user","message":{"content":[{"type":"text","text":"first"},{"type":"text","text":"last"}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","content":"result"}]}}`,
		`{"type":"user","message":{"content":[{"type":"text","text":"[Request interrupted by user]"}]}}`,
		`{"type":"user","message":{"content":"[Request interrupted by user]"}}`,
		`{"type":"user","message":{"content":[{"type":"text","text":"The user doesn't want to take this action right now. STOP what you are doing and wait for the user to tell you how to proceed."}]}}`,
		`{"type":"user","message":{"content":[{"type":"text","text":"The user doesn't want to proceed with this tool use. The tool use was rejected (eg. if it was a file edit, the new_string was NOT written to the file). STOP what you are doing and wait for the user to tell you how to proceed."}]}}`,
		`{"type":"user","isMeta":true,"message":{"content":"private context"}}`,
		`{"type":"user","isCompactSummary":true,"message":{"content":"summary"}}`,
		`{"type":"user","message":{"content":"<local-command-stdout>output</local-command-stdout>"}}`,
		`{"type":"user","message":{"content":"<bash-input>printf 'hello'\n</bash-input>"}}`,
		`{"type":"user","message":{"content":"<command-name>/compact</command-name><command-args>preserve constraints</command-args>"}}`,
		`{"type":"user","message":{"content":"<ide_selection file=\"private\">injected\ntext</ide_selection>\n<code>keep me</code>\ufeff"}}`,
		`{"type":"user","message":{"content":"\u0085 retain non-JS-whitespace \u0085"}}`,
	} {
		var value protocol.JSONObject
		if e := json.Unmarshal([]byte(row), &value); e != nil {
			t.Fatal(e)
		}
		rows = append(rows, value)
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-message-selector.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	raw, _ := json.Marshal(map[string]any{"homeDir": home, "rows": rows})
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_MESSAGE_SELECTOR_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("actual Source selector %v %.3000s", e, out)
	}
	var source struct {
		Rows []struct {
			Selectable bool
			Resubmit   *struct{ Text, Mode string }
		}
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Rows) != len(rows) {
		t.Fatal(e, string(out))
	}
	for i, row := range rows {
		_, got := SelectableMessageText(row)
		if got != source.Rows[i].Selectable {
			t.Errorf("row%d selectable Go%v Source%v", i, got, source.Rows[i].Selectable)
		}
		if source.Rows[i].Resubmit != nil {
			text, mode, _ := MessageResubmit(row)
			if text != source.Rows[i].Resubmit.Text || mode != source.Rows[i].Resubmit.Mode {
				t.Errorf("row%d resubmit Go%q/%s Source%+v", i, text, mode, source.Rows[i].Resubmit)
			}
		}
	}
}

func TestMessageResubmitRestoresOriginalImagePasteIDs(t *testing.T) {
	var message protocol.JSONObject
	_ = json.Unmarshal([]byte(`{"type":"user","imagePasteIds":[8],"message":{"content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"private-data"}},{"type":"text","text":"inspect [Image #8]"}]}}`), &message)
	text, mode, pastes := MessageResubmit(message)
	if text != "inspect [Image #8]" || mode != "prompt" || len(pastes) != 1 || pastes[8].Content != "private-data" || pastes[8].MediaType != "image/png" {
		t.Fatal(text, mode, pastes)
	}
}
