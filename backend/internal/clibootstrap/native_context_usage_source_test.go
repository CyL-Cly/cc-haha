package clibootstrap

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
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestActualNativeContextInspectionSchemasMatchSourceRealTools(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Source Bun not configured")
	}
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{}`)
	f.n.Environment["CLAUDE_CODE_SIMPLE"] = "1"
	f.n.Environment["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] = "true"
	f.n.Environment["CLAUDE_CODE_OVERRIDE_DATE"] = "2026-10-07"
	f.n.Environment["ANTHROPIC_API_KEY"] = "offline-context-real-tools"
	var counting atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages/count_tokens" {
			t.Errorf("inspection opened inference path %s", r.URL.Path)
		}
		counting.Add(1)
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, `{"input_tokens":37}`)
	}))
	defer peer.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	f.n.Environment["ANTHROPIC_MODEL"] = "deepseek-flash[1m]"
	f.n.Environment["ENABLE_TOOL_SEARCH"] = "false"
	f.n.Environment["CLAUDE_CODE_MODEL_CONTEXT_WINDOWS"] = `{"deepseek-flash":1000000}`
	const prompt = "Actual original tools context inspection."
	m, err := NewMainPrint(MainPrintOptions{Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: runtime.GOOS, Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, Arguments: PrintArguments{Model: "deepseek-flash[1m]", SystemPrompt: prompt, Tools: []string{"Read", "Write"}, PermissionMode: "bypassPermissions", MaxTurns: 1, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	if err = m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := m.ReadContextUsage(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-context-analyser.fixture.ts")
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = f.home
	command.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "PATH=/usr/bin:/bin", "NODE_ENV=test", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_CONTEXT_ANALYSER_FIXTURE=1", "BUN_OPTIONS=--no-env-file"}
	input, _ := json.Marshal(map[string]any{"home": f.home, "environment": f.n.Environment, "estimates": []any{}, "rows": []any{map[string]any{"messages": []any{}, "tools": []string{"Read", "Write"}, "model": "deepseek-flash[1m]", "prompt": prompt}}})
	command.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct{ Results []json.RawMessage }
	if err = json.Unmarshal(output, &source); err != nil || len(source.Results) != 1 {
		t.Fatal(err, string(output))
	}
	encoded, _ := got.MarshalJSON()
	var left, right map[string]any
	_ = json.Unmarshal(encoded, &left)
	_ = json.Unmarshal(source.Results[0], &right)
	if !reflect.DeepEqual(left, right) {
		for key, value := range left {
			if !reflect.DeepEqual(value, right[key]) {
				if key == "gridRows" {
					t.Error("gridRows differ; check category totals")
				} else {
					t.Errorf("%s: Go %v Source %v", key, value, right[key])
				}
			}
		}
	}
	if counting.Load() != 0 {
		t.Fatal("estimate-only context opened a model request")
	}
	exact, err := m.ReadContextUsage(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if counting.Load() == 0 || exact.String("model") != "deepseek-flash[1m]" {
		t.Fatal("exact counter did not reach its actual native client", counting.Load())
	}
	exactCommand := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	exactCommand.Dir, exactCommand.Env = command.Dir, command.Env
	exactInput, _ := json.Marshal(map[string]any{"home": f.home, "countURL": peer.URL, "environment": f.n.Environment, "estimates": []any{}, "rows": []any{map[string]any{"messages": []any{}, "tools": []string{"Read", "Write"}, "model": "deepseek-flash[1m]", "prompt": prompt, "estimateOnly": false}}})
	exactCommand.Stdin = bytes.NewReader(exactInput)
	var exactError bytes.Buffer
	exactCommand.Stderr = &exactError
	exactOutput, err := exactCommand.Output()
	if err != nil {
		t.Fatal(err, exactError.String())
	}
	if err = json.Unmarshal(exactOutput, &source); err != nil || len(source.Results) != 1 {
		t.Fatal(err, string(exactOutput))
	}
	exactEncoded, _ := exact.MarshalJSON()
	_ = json.Unmarshal(exactEncoded, &left)
	_ = json.Unmarshal(source.Results[0], &right)
	if !reflect.DeepEqual(left, right) {
		for key, value := range left {
			if !reflect.DeepEqual(value, right[key]) {
				t.Errorf("exact %s differs: Go %v Source %v", key, value, right[key])
			}
		}
	}
}
