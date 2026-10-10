package mcpauth

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

func TestMCPClientSecretRawUTF16PhysicalCredentialsMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	s, home := authFixture(t)
	config := authObject(t, `{"type":"http","url":"http://127.0.0.1:1/mcp","oauth":{"clientId":"private-client"}}`)
	initial := authObject(t, `{"future":{"keep":"\ud800"},"mcpOAuthClientConfig":{"unrelated":{"clientSecret":"keep-private"}}}`)
	if result := s.options.Secure.Update(context.Background(), initial); !result.Success {
		t.Fatal(result)
	}
	steps := []map[string]any{}
	observations := []map[string]any{}
	for _, secret := range []json.RawMessage{json.RawMessage(`"private-"`), json.RawMessage(`"private-\ud83d"`), json.RawMessage(`"private-\ude00"`), json.RawMessage(`"private-\ud83d\ude00"`), json.RawMessage(`"private-你"`), json.RawMessage(`"\u0000 \n"`)} {
		steps = append(steps, map[string]any{"action": "save-secret", "value": secret})
		if err := s.SaveClientSecretJSON(context.Background(), "private-cli", config, secret); err != nil {
			t.Fatal(err)
		}
		physical, err := os.ReadFile(filepath.Join(home, ".claude", ".credentials.json"))
		if err != nil {
			t.Fatal(err)
		}
		observations = append(observations, map[string]any{"value": nil, "storage": json.RawMessage(physical)})
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-mcp-auth.fixture.ts")
	input, _ := json.Marshal(map[string]any{"name": "private-cli", "config": config, "initial": initial, "now": 1700000000000, "operations": steps})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_MCP_AUTH_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	expected, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source []protocol.JSONObject
	if err = json.Unmarshal(expected, &source); err != nil || len(source) != len(observations) {
		t.Fatal(err, string(expected))
	}
	for i, actual := range observations {
		a, _ := protocol.ParseAndStringifyJavaScriptJSON(actual["storage"].(json.RawMessage))
		b, _ := protocol.ParseAndStringifyJavaScriptJSON(source[i].Get("storage"))
		if !bytes.Equal(a, b) {
			t.Errorf("native physical credential UTF16 mismatch case%d\nGo%s\nSource%s", i, a, b)
		}
	}
}
