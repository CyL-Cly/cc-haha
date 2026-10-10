package mcpruntime

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestMCPTextStructuredContentAndResourcesAgainstActualSource(t *testing.T) {
	cases := []json.RawMessage{}
	observations := []any{}
	for _, raw := range []string{`{}`, `{"toolResult":null,"content":[{"type":"text","text":"ignored"}]}`, `{"toolResult":{"n":3}}`, `{"toolResult":["x",null,3]}`, `{"content":[]}`, `{"content":[{"type":"text","text":"hello\ud800"}]}`, `{"structuredContent":{"2":"x","one":{"x":true}}}`, `{"content":[{"type":"text","text":"pretty text"}],"structuredContent":{"ok":true}}`, `{"content":[{"type":"text","text":" {\"n\": 1, \"a\": [true, 2]} "}],"structuredContent":{"a":[true,2],"n":1}}`, `{"content":[{"type":"text","text":"\ufeff{\"n\":1}\ufeff"}],"structuredContent":{"n":1}}`, `{"content":[{"type":"text","text":"\u0085{\"n\":1}\u0085"}],"structuredContent":{"n":1}}`, `{"content":[{"type":"text","text":"{ \"x\": \"\\ud800\" }"}],"structuredContent":{"x":"\ud801"}}`, `{"content":[{"type":"text","text":"{ \"x\": null }"}],"structuredContent":{"x":1e400}}`, `{"content":[{"type":"resource","resource":{"uri":"owned:///file","text":"body\ud801"}},{"type":"resource_link","name":"File","uri":"owned:///next","description":"more"},{"type":"resource","resource":{"uri":"empty:///"}},{"type":"unknown"}]}`, `{"content":[{"type":"text","text":"[1]"}],"structuredContent":[1]}`, `{"content":[{"type":"text","text":"{\"a\":1}"}],"structuredContent":[1]}`, `{"content":[{"type":"text","text":"{ \"x\": 9007199254740992 }"}],"structuredContent":{"x":9007199254740993}}`, `{"content":[],"structuredContent":null}`} {
		cases = append(cases, json.RawMessage(raw))
		value, err := TransformResult(parse(t, raw), "contract-tool", "contract-server")
		var failure any
		if err != nil {
			failure = err.Error()
			value = nil
		}
		observations = append(observations, struct {
			Content json.RawMessage `json:"content"`
			Error   any             `json:"error"`
		}{value, failure})
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source MCP result comparison requires pinned Bun")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../../.."))
	cmd := exec.Command(bun, "run", filepath.Join(root, "scripts/quality-gate/go-parity/native-mcp-result.fixture.ts"))
	cmd.Dir = home
	cmd.Env = []string{"PATH=" + filepath.Dir(bun) + ":/usr/bin:/bin", "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + os.TempDir(), "CC_HAHA_MCP_RESULT_FIXTURE=1"}
	input, _ := json.Marshal(map[string]any{"cases": cases})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	actual, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source MCP results %v\n%s", err, stderr.String())
	}
	expected, _ := json.Marshal(observations)
	if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(actual), protocol.StringifyValidatedJavaScriptJSON(expected)) {
		var a, b []json.RawMessage
		_ = json.Unmarshal(actual, &a)
		_ = json.Unmarshal(expected, &b)
		for i := range a {
			if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(a[i]), protocol.StringifyValidatedJavaScriptJSON(b[i])) {
				t.Fatalf("case %d\nSource %s\nGo %s", i, a[i], b[i])
			}
		}
		t.Fatal("result mismatch")
	}
}
func TestMCPMediaRequiresBoundNativeTransformation(t *testing.T) {
	for _, raw := range []string{`{"content":[{"type":"image","data":"AAAA","mimeType":"image/png"}]}`, `{"content":[{"type":"audio","data":"AAAA","mimeType":"audio/wav"}]}`, `{"content":[{"type":"resource","resource":{"uri":"owned:///file","blob":"AAAA"}}]}`} {
		_, err := TransformResult(parse(t, raw), "t", "s")
		if _, ok := err.(*ResultCapabilityError); !ok {
			t.Fatalf("unbound media was omitted: %v", err)
		}
	}
}
