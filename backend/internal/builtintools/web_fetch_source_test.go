package builtintools

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestWebFetchDomainPermissionsMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	_ = os.MkdirAll(config, 0700)
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	rows := []map[string]any{}
	add := func(url string, p string) {
		rows = append(rows, map[string]any{"tool": "WebFetch", "input": string(raw(map[string]string{"url": url, "prompt": "details"})), "permission": json.RawMessage(p)})
	}
	add("https://go.dev/doc", `{}`)
	add("https://go.dev/doc", `{"alwaysDenyRules":{"userSettings":["WebFetch(domain:go.dev)"]}}`)
	add("https://one.fixture/a", `{}`)
	add("https://one.fixture/a", `{"alwaysDenyRules":{"userSettings":["WebFetch(domain:one.fixture)"]}}`)
	add("https://one.fixture/a", `{"alwaysAskRules":{"projectSettings":["WebFetch(domain:one.fixture)"]},"alwaysAllowRules":{"userSettings":["WebFetch(domain:one.fixture)"]}}`)
	add("https://one.fixture/a", `{"alwaysAllowRules":{"userSettings":["WebFetch(domain:one.fixture)"],"projectSettings":["WebFetch(domain:one.fixture)"]}}`)
	add("https://github.com/anthropics/a", `{}`)
	add("https://github.com/anthropics2/a", `{}`)
	add("https://vercel.com/docs/a", `{}`)
	add("https://vercel.com/docs2/a", `{}`)
	add("https://ONE.fixture:443/a", `{"alwaysAllowRules":{"cliArg":["WebFetch(domain:one.fixture)"]}}`)
	add("https://[::1]/", `{}`)
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
		t.Fatalf("Source %v %.2400s", e, stderr.String())
	}
	var source struct {
		Rows []struct {
			Permission  json.RawMessage
			Description string
		}
		Capabilities struct{ Blocked []string }
	}
	if json.Unmarshal(out, &source) != nil || len(source.Rows) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source escaped %v %.1800s", source.Capabilities.Blocked, out)
	}
	for i, row := range rows {
		var in, c protocol.JSONObject
		_ = json.Unmarshal([]byte(row["input"].(string)), &in)
		_ = json.Unmarshal(row["permission"].(json.RawMessage), &c)
		decision := webFetchToolDecision(c, &in)
		actual := protocol.NewJSONObject(field("behavior", decision.Behavior))
		if decision.UpdatedInput != nil {
			actual.Set("updatedInput", raw(*decision.UpdatedInput))
		}
		actual.Set("message", decision.Message)
		actual.Set("suggestions", decision.Suggestions)
		actual.Set("decisionReason", decision.DecisionReason)
		var a, b any
		_ = json.Unmarshal(raw(actual), &a)
		_ = json.Unmarshal(source.Rows[i].Permission, &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("case%d Go%s Source%s", i, raw(actual), source.Rows[i].Permission)
		}
		if string(decision.Description) != string(quote(source.Rows[i].Description)) {
			t.Errorf("case%d description Go%s Source%q", i, decision.Description, source.Rows[i].Description)
		}
	}
}
