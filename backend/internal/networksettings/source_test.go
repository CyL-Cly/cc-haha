package networksettings

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
)

func TestNativeNetworkNormalizationAndEnvironmentMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	for _, p := range []string{filepath.Join(home, ".claude"), filepath.Join(home, "tmp")} {
		if e := os.MkdirAll(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	type row struct {
		Settings    string            `json:"settings"`
		Environment map[string]string `json:"environment"`
	}
	rows := []row{}
	for _, settings := range []string{`null`, `[]`, `false`, `{}`, `{"network":null}`, `{"network":{"aiRequestTimeoutMs":"50000"}}`, `{"network":{"aiRequestTimeoutMs":49999.5}}`, `{"network":{"aiRequestTimeoutMs":-5}}`, `{"network":{"aiRequestTimeoutMs":1e400}}`, `{"network":{"aiRequestTimeoutMs":9e9}}`, `{"network":{"proxy":{"mode":"unknown","url":"ignored"}}}`, `{"network":{"proxy":{"mode":"direct"}}}`, `{"network":{"proxy":{"mode":"manual","url":"\ufeff http://127.0.0.1:8888 \u00a0"}}}`, `{"network":{"proxy":{"mode":"manual","url":" "}}}`} {
		for _, env := range []map[string]string{{}, {"https_proxy": "http://lower.example", "HTTPS_PROXY": "http://upper.example", "NO_PROXY": "EXAMPLE,LOCALHOST localhost"}, {"CC_HAHA_SYSTEM_PROXY_URL": "\ufeff http://bridge.example \u00a0", "CC_HAHA_SYSTEM_PROXY_ERROR": "ignored", "no_proxy": "custom\u00a0,::1"}, {"CC_HAHA_SYSTEM_PROXY_ERROR": "\ufeff explicit resolver failure \u00a0"}, {"ALL_PROXY": "http://ignored.example", "HTTP_PROXY": " http://retained-space.example "}} {
			rows = append(rows, row{settings, env})
		}
	}
	data, _ := json.Marshal(map[string]any{"homeDir": home, "rows": rows})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-network-settings.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Stdin = bytes.NewReader(data)
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_NETWORK_SETTINGS_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatalf("Source%v %.2400s", e, stderr.String())
	}
	var expected struct {
		Rows []struct {
			Settings    Settings
			Environment map[string]string
			Error       string
		}
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &expected); e != nil || len(expected.Rows) != len(rows) || len(expected.Capabilities.Blocked) > 0 {
		t.Fatalf("Source escaped%v %.2400s", e, out)
	}
	for i, row := range rows {
		settings := Normalize([]byte(row.Settings))
		env, e := BuildEnvironment(settings, row.Environment)
		errText := ""
		if e != nil {
			errText = e.Error()
		}
		want := expected.Rows[i]
		if !reflect.DeepEqual(settings, want.Settings) || !reflect.DeepEqual(env, want.Environment) || errText != want.Error {
			t.Errorf("case%d settings%+v/%+v env%v/%v error%q/%q", i, settings, want.Settings, env, want.Environment, errText, want.Error)
		}
	}
}
