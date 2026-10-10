package httphook

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

func TestActualSourceHTTPHookPreparationPolicyIntersectionUnknownFieldsAndHeaderOrder(t *testing.T) {
	type row struct {
		Hook        protocol.JSONObject `json:"hook"`
		Settings    protocol.JSONObject `json:"settings"`
		Environment map[string]string   `json:"environment"`
	}
	rows := []row{}
	native := []Prepared{}
	for _, urls := range []json.RawMessage{nil, []byte(`[]`), []byte(`["http://127.0.0.1/*"]`), []byte(`["https://blocked/*"]`)} {
		for _, vars := range []json.RawMessage{nil, []byte(`[]`), []byte(`["HTTP_HOOK_FIXTURE_TOKEN"]`), []byte(`["HTTP_HOOK_FIXTURE_TOKEN","HTTP_HOOK_FIXTURE_OTHER"]`)} {
			var hook protocol.JSONObject
			_ = hook.UnmarshalJSON([]byte(`{"type":"http","url":"http://127.0.0.1/hook","headers":{"X-Own":"$HTTP_HOOK_FIXTURE_TOKEN:${HTTP_HOOK_FIXTURE_OTHER}","Content-Type":"custom\r\n\u0000type","content-type":"case retained","3":"${HTTP_HOOK_FIXTURE_TOKEN}","X-Literal":"$lowercase:${MISSING}"},"allowedEnvVars":["HTTP_HOOK_FIXTURE_TOKEN","HTTP_HOOK_FIXTURE_OTHER","MISSING"],"future":{"unknown":true}}`))
			settings := protocol.NewJSONObject(protocol.JSONField{Name: "allowedHttpHookUrls", Value: urls}, protocol.JSONField{Name: "httpHookAllowedEnvVars", Value: vars}, protocol.JSONField{Name: "unknown", Value: []byte(`{"future":true}`)})
			env := map[string]string{"HTTP_HOOK_FIXTURE_TOKEN": "fake\r\n\x00token", "HTTP_HOOK_FIXTURE_OTHER": "other-authorized"}
			prepared, err := Prepare(hook, settings, env)
			if err != nil {
				t.Fatal(err)
			}
			rows = append(rows, row{hook, settings, env})
			native = append(native, prepared)
		}
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-http-hook-policy.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_HTTP_HOOK_POLICY_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NODE_ENV=production"}
	data, _ := json.Marshal(map[string]any{"homeDir": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var source struct {
		Rows []struct {
			Prepared struct {
				Result  protocol.JSONObject
				Capture protocol.JSONObject
			}
		}
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.Rows) != len(native) {
		t.Fatal(err, string(out))
	}
	for i, prepared := range native {
		result := source.Rows[i].Prepared.Result
		capture := source.Rows[i].Prepared.Capture
		if prepared.Blocked != nil {
			if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(prepared.Blocked), protocol.StringifyValidatedJavaScriptJSON(result.Get("error"))) || capture.IsObject() {
				t.Errorf("Source policy before IO row%d %s %s", i, prepared.Blocked, result.Get("error"))
			}
			continue
		}
		headers := protocol.NewJSONObject()
		for _, header := range prepared.Headers {
			_ = headers.SetPropertyJSON(header.Name, header.Value)
		}
		actual, _ := headers.MarshalJSON()
		want := capture.Get("headers")
		if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(actual), protocol.StringifyValidatedJavaScriptJSON(want)) {
			t.Errorf("Source prepared headers row%d Go%s Source%s", i, actual, want)
		}
		if capture.String("url") != "http://127.0.0.1/hook" || string(capture.Get("maxRedirects")) != "0" || string(capture.Get("proxy")) != "false" || capture.String("responseType") != "text" {
			t.Fatal("original transport options", string(out))
		}
	}
}
