package desktopattachments

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestRawTerminalTextAttachmentMaterializationMatchesActualSourceUTF16(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual Source Bun required")
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{}
	for _, content := range []string{`" \ufeffa\ud83db\udc00c\ud83d\ude42\u00a0 "`, `"\ufeff\u00a0 "`, `"\u0085\ud83d\u0085"`, `"\udc00"`} {
		for _, attachments := range []json.RawMessage{json.RawMessage(`[]`), json.RawMessage(fmt.Sprintf(`[{"type":"file","path":%s}]`, quoteText(filepath.Join(home, "retained file.txt"))))} {
			rows = append(rows, map[string]any{"content": json.RawMessage(content), "sessionId": fmt.Sprintf("utf16-%d", len(rows)), "attachments": attachments})
		}
	}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "cases": rows})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-desktop-attachments.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CC_HAHA_DESKTOP_ATTACHMENTS_FIXTURE=1", "PATH=/usr/bin:/bin"}
	command.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatal("actual Source attachment text", err, stderr.String())
	}
	var source struct {
		Results []struct {
			Blocks []protocol.JSONObject
			Files  []json.RawMessage
		}
	}
	if err := json.Unmarshal(output, &source); err != nil || len(source.Results) != len(rows) {
		t.Fatal(err, string(output))
	}
	for i, row := range rows {
		encoded, err := BuildJSON(row["content"].(json.RawMessage), row["sessionId"].(string), row["attachments"].(json.RawMessage), Options{HomeDir: home, NewID: func() string { t.Fatal("path-only input staged a file"); return "" }})
		if err != nil {
			t.Fatal(err)
		}
		var actual []protocol.JSONObject
		if err := json.Unmarshal(encoded, &actual); err != nil || len(actual) != len(source.Results[i].Blocks) || len(source.Results[i].Files) != 0 {
			t.Fatal("original attachment blocks or files differ", err, string(encoded), source.Results[i])
		}
		for j, block := range actual {
			want := source.Results[i].Blocks[j]
			gotUnits, err := protocol.DecodeJavaScriptJSONString(block.Get("text"))
			wantUnits, sourceErr := protocol.DecodeJavaScriptJSONString(want.Get("text"))
			if err != nil || sourceErr != nil || block.String("type") != want.String("type") || !reflect.DeepEqual(gotUnits, wantUnits) {
				t.Fatal("Source trim/prefix changed original UTF16", i, gotUnits, wantUnits, err, sourceErr)
			}
		}
	}
}

func quoteText(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func TestRawAttachmentTextRejectsInvalidJSONBeforeStaging(t *testing.T) {
	home := t.TempDir()
	for _, value := range []string{"", `null`, `17`, `{}`, `[]`, `"unterminated`, `"one" "two"`} {
		_, err := BuildJSON(json.RawMessage(value), "must-not-write", json.RawMessage(`[{"type":"file","name":"data.txt","data":"YQ=="}]`), Options{HomeDir: home, NewID: func() string { t.Fatal("invalid text staged an attachment"); return "" }})
		if err == nil {
			t.Fatal("invalid text accepted", value)
		}
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatal("invalid text changed isolated HOME", entries, err)
	}
}
