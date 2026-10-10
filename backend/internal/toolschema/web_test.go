package toolschema

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestWebSchemasPromptAndInputMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	_ = os.MkdirAll(config, 0700)
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	inputs := map[string][]string{"WebFetch": {`{"url":"https://example.org/a","prompt":"extract"}`, `{}`, `{"url":"bad","prompt":4,"unknown":true}`, `{"url":"mailto:x@example.org","prompt":""}`, `{"url":"https://\ud800.example","prompt":"\ud800"}`, `{"url":null,"prompt":true,"0":null,"x":false}`}, "WebSearch": {`{"query":"Go","allowed_domains":["go.dev"],"blocked_domains":[]}`, `{}`, `{"query":"a","allowed_domains":[1,null],"blocked_domains":false,"x":2}`, `{"query":"😀","allowed_domains":[]}`, `{"query":"\ud800","blocked_domains":["\ud800"]}`}}
	rows := []map[string]any{}
	for _, name := range []string{"WebFetch", "WebSearch"} {
		for _, in := range inputs[name] {
			rows = append(rows, map[string]any{"tool": name, "input": in})
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-web-tools.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WEB_TOOLS_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatalf("Source %v %.2000s", e, stderr.String())
	}
	var source struct {
		Rows []struct {
			Success      bool
			Data, Issues string
		}
		Schemas      []json.RawMessage
		MonthYear    string
		Capabilities struct{ Blocked []string }
	}
	if json.Unmarshal(out, &source) != nil || len(source.Rows) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source escaped %v %.1800s", source.Capabilities.Blocked, out)
	}
	for i, row := range rows {
		def, e := NativeBuiltinDefinition(row["tool"].(string))
		if e != nil {
			t.Fatal(e)
		}
		in := []byte(row["input"].(string))
		data, e := ParseInputJSON(def, in)
		if (e == nil) != source.Rows[i].Success {
			t.Errorf("row%d Go%s %v Source%+v", i, in, e, source.Rows[i])
			continue
		}
		if e == nil {
			if !jsonSourcesEqual(raw(data), []byte(source.Rows[i].Data)) {
				t.Errorf("row%d parsedGo%s Source%s", i, raw(data), source.Rows[i].Data)
			}
		} else if ve, ok := e.(*ValidationError); !ok || !jsonSourcesEqual(ve.Issues, []byte(source.Rows[i].Issues)) {
			t.Errorf("row%d issues Go%v Source%s", i, e, source.Rows[i].Issues)
		}
	}
	for i, name := range []string{"WebFetch", "WebSearch"} {
		def, e := NativeBuiltinDefinition(name)
		if e != nil {
			t.Fatal(e)
		}
		schemas, e := NewSession().RenderBatch([]Definition{def}, Options{Host: Host{BaseURL: "http://127.0.0.1:1", WebSearchMonthYear: source.MonthYear}, Model: "deepseek-flash"})
		if e != nil {
			t.Fatal(e)
		}
		var api protocol.JSONObject
		_ = json.Unmarshal(source.Schemas[i], &api)
		if !jsonSourcesEqual(raw(schemas[0]), raw(api)) {
			t.Errorf("%s schema Go%s Source%s", name, raw(schemas[0]), raw(api))
		}
	}
}
