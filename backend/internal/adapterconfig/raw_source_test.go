package adapterconfig

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

func TestRawAdapterReadActualSourceShapesSecretsUnknownFieldsAndErrors(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	_ = os.MkdirAll(config, 0700)
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	initial := []any{nil, `{"telegram":{"botToken":"fake-sensitive-token","allowedUsers":[123],"future":"keep"},"futureRoot":{"version":99}}`, `null`, `[1,"future"]`, `false`, `42`, `"future"`, "", "\ufeff{}", `{bad protected`}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-scheduler-adapter-config.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SCHEDULER_ADAPTER_CONFIG_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin", "TZ=UTC"}
	cmd.Stdin = bytes.NewReader(mustJSON(map[string]any{"homeDir": home, "rows": initial}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var source struct {
		Rows []struct {
			Value json.RawMessage
			Error *Error
			Logs  []struct{ Message, Error string }
		}
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(data, &source); e != nil || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(e, string(data))
	}
	logs := []error{}
	reader, e := New(Options{ConfigDir: config, ReadFile: os.ReadFile, LogError: func(e error) { logs = append(logs, e) }})
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(config, "adapters.json")
	for n, row := range initial {
		_ = os.Remove(path)
		if row != nil {
			_ = os.WriteFile(path, []byte(row.(string)), 0600)
		}
		logs = nil
		value, e := reader.ReadJSON(context.Background())
		expected := source.Rows[n]
		if expected.Error != nil {
			actual, ok := e.(*Error)
			if !ok || !reflect.DeepEqual(actual, expected.Error) || len(logs) != 1 || len(expected.Logs) != 1 || logs[0].Error() != expected.Logs[0].Error {
				t.Errorf("row%d raw read Go%v logs%v Source%+v logs%+v", n, e, logs, expected.Error, expected.Logs)
			}
		} else {
			var native, want any
			_ = json.Unmarshal(value, &native)
			_ = json.Unmarshal(expected.Value, &want)
			if e != nil || !reflect.DeepEqual(native, want) || len(logs) != 0 {
				t.Errorf("row%d raw shape Go%s Source%s error%v", n, value, expected.Value, e)
			}
		}
		if row != nil {
			after, _ := os.ReadFile(path)
			if string(after) != row.(string) {
				t.Fatal("raw reader rewrote user state", n, string(after))
			}
		}
	}
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
