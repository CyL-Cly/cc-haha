package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestNativeOrdinaryDefaultCatalogAgainstActualOriginalGetToolsAndFinalPool(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	profiles := []map[string]string{{"ENABLE_TOOL_SEARCH": "false"}, {"ENABLE_TOOL_SEARCH": "false", "CLAUDE_CODE_DISABLE_WORKFLOWS": "1"}, {"ENABLE_TOOL_SEARCH": "false", "CLAUDE_CODE_ENABLE_TASKS": "1"}}
	sourceHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(sourceHome, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{}
	for _, env := range profiles {
		rows = append(rows, map[string]any{"env": env, "nonInteractive": true, "model": "fixture-model"})
	}
	input, _ := json.Marshal(map[string]any{"homeDir": sourceHome, "profiles": rows})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-tool-pool.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = sourceHome
	command.Env = []string{"HOME=" + sourceHome, "USERPROFILE=" + sourceHome, "CLAUDE_CONFIG_DIR=" + filepath.Join(sourceHome, ".claude"), "TMPDIR=" + filepath.Join(sourceHome, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(sourceHome), "CC_HAHA_NATIVE_TOOL_POOL_FIXTURE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "ANTHROPIC_API_KEY=fake-tool-pool-key", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("original default actual producer: %v %.2000s", err, output)
	}
	var expected struct {
		Profiles     []struct{ Final []string }
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &expected); err != nil || len(expected.Profiles) != len(profiles) || len(expected.Capabilities.Blocked) != 0 {
		t.Fatalf("original receipt: %v %.1800s", err, output)
	}
	for i, env := range profiles {
		t.Run(env["CLAUDE_CODE_DISABLE_WORKFLOWS"]+env["CLAUDE_CODE_ENABLE_TASKS"], func(t *testing.T) {
			var captured []byte
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				captured, _ = io.ReadAll(r.Body)
				response(w, "source-default", "", nil, "SOURCE_DEFAULT_OK")
			}))
			defer peer.Close()
			o := nativeSessionOptions(t, peer.URL)
			o.Arguments.Tools = nil
			for k, v := range env {
				o.Environment[k] = v
				o.Native.Environment[k] = v
			}
			s, err := NewSession(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			if err = s.Submit(context.Background(), tui.Input{Text: "Use the ordinary Source default."}, nil); err != nil {
				t.Fatal(err)
			}
			var body struct{ Tools []struct{ Name string } }
			if err = json.Unmarshal(captured, &body); err != nil {
				t.Fatal(err)
			}
			names := []string{}
			for _, tool := range body.Tools {
				names = append(names, tool.Name)
			}
			if !reflect.DeepEqual(names, expected.Profiles[i].Final) {
				t.Fatal("actual default source pool drift", names, expected.Profiles[i].Final)
			}
		})
	}
}
