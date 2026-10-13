package sdkhost

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

func TestActualQueryEngineLocalDispatchBeforeWorkAndLiteralPeerInput(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual pinned Source Bun required")
	}
	commands := []ControlCommand{{Name: "compact", Kind: "local", Aliases: []string{"short"}}, {Name: "secret", Kind: "local", Sensitive: true}, {Name: "review", Kind: "prompt"}, {Name: "jsx", Kind: "local-jsx"}}
	inputs := []json.RawMessage{quote("/compact retain context"), quote("/compact  retain  context \ufeff"), quote(" /compact"), quote("/compact"), quote("/secret token"), quote("/review"), quote("/jsx"), quote("/unknown"), quote("/"), quote("normal message"), []byte(`"/compact lone\ud800\udc00 and\ud800"`), []byte(`[{"type":"text","text":"preceding"},{"type":"text","text":"/compact 中文"}]`), []byte(`[{"type":"text","text":"/compact"},{"type":"image","source":{}}]`), []byte(`[]`)}
	rows := []map[string]any{}
	inputs = append(inputs, quote("/short original alias args"))
	for _, content := range inputs {
		for _, pending := range []bool{false, true} {
			rows = append(rows, map[string]any{"content": content, "uuid": "owned-command-uuid", "pending": pending})
		}
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("testdata/local-dispatch-source.fixture.ts")
	sandbox, _ := filepath.Abs("../../testdata/loopback-only.sb")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_LOCAL_DISPATCH_FIXTURE=1", "ANTHROPIC_API_KEY=fake-local-dispatch", "ANTHROPIC_BASE_URL=http://127.0.0.1:9", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"commands": []map[string]any{{"name": "compact", "type": "local", "aliases": []string{"short"}}, {"name": "secret", "type": "local", "isSensitive": true}, {"name": "review", "type": "prompt"}, {"name": "jsx", "type": "local-jsx"}}, "rows": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Outputs      []json.RawMessage
		SourceSha256 string
	}
	if err = json.Unmarshal(data, &source); err != nil || len(source.Outputs) != len(rows) || len(source.SourceSha256) != 64 {
		t.Fatal(err, string(data))
	}
	for i, row := range rows {
		content := localDispatchContent(row["content"].(json.RawMessage), commands, row["pending"].(bool))
		var record protocol.JSONObject
		if string(source.Outputs[i]) == "null" {
			if content != nil {
				t.Fatalf("unexpected ACK row%d %s", i, content)
			}
			continue
		}
		if err = record.UnmarshalJSON(source.Outputs[i]); err != nil {
			t.Fatal(err)
		}
		if content == nil || !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(content), protocol.StringifyValidatedJavaScriptJSON(record.Get("content"))) {
			t.Fatalf("row%d Go%s Source%s", i, content, record.Get("content"))
		}
	}
}
