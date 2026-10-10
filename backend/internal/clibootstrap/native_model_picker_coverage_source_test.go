package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualSourceModelPickerSelectPersistenceAndNextHarnessEffort(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual Source requires pinned Bun")
	}
	type row struct {
		Name     string              `json:"name"`
		Settings protocol.JSONObject `json:"settings"`
		Initial  string              `json:"initial"`
		Effort   string              `json:"effort"`
		Toggled  bool                `json:"toggled"`
		Skip     bool                `json:"skip"`
		Model    string              `json:"model"`
	}
	rows := []row{}
	labels := []string{"claude-fable-5-1", "claude-fable-5", "claude-opus-5-5", "claude-opus-5", "claude-opus-4-8", "claude-opus-4-7", "claude-opus-4-5", "claude-opus-4-1", "claude-opus-4", "claude-sonnet-5-5", "claude-sonnet-5", "claude-sonnet-4-6", "claude-sonnet-4-5", "claude-sonnet-4", "claude-3-7-sonnet", "claude-3-5-sonnet", "claude-haiku-4-5", "claude-3-5-haiku", "claude-opus-4-7[1m]", "third-party-unknown", "opusplan"}
	for _, item := range []struct {
		name, prior, picked string
		toggled, skip       bool
	}{{"default-following", "", "high", false, false}, {"explicit-high-stays", "high", "high", false, false}, {"explicit-medium-stays", "medium", "medium", false, false}, {"toggled-low", "", "low", true, false}, {"session-max", "medium", "max", true, false}, {"skip-effort-write", "medium", "low", true, true}} {
		settings := protocol.NewJSONObject(protocol.JSONField{Name: "model", Value: quoteMain("sonnet")}, protocol.JSONField{Name: "future", Value: rawMain(map[string]any{"kept": true})}, protocol.JSONField{Name: "agentTeamsEnabled", Value: rawMain(false)}, protocol.JSONField{Name: "alwaysThinkingEnabled", Value: rawMain(false)})
		if item.prior != "" {
			settings.Set("effortLevel", quoteMain(item.prior))
		}
		rows = append(rows, row{item.name, settings, "sonnet", item.picked, item.toggled, item.skip, "sonnet"})
	}
	home := t.TempDir()
	fixture, e := filepath.Abs("testdata/native-model-picker-apply.fixture.ts")
	if e != nil {
		t.Fatal(e)
	}
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "CC_HAHA_MODEL_PICKER_APPLY_FIXTURE=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "NODE_ENV=test", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_FAST_MODE=1", "ANTHROPIC_API_KEY=private-no-network", "ANTHROPIC_BASE_URL=http://127.0.0.1:43210", "ANTHROPIC_DEFAULT_SONNET_MODEL=fixture-menu-model", "ANTHROPIC_DEFAULT_SONNET_MODEL_SUPPORTED_CAPABILITIES=effort,max_effort"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"home": home, "rows": rows, "labels": labels}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatalf("actual Source select %v %s", e, stderr.String())
	}
	var source struct {
		Labels []string `json:"labels"`
		Values []struct {
			Settings  protocol.JSONObject `json:"settings"`
			AppState  protocol.JSONObject `json:"appState"`
			Selected  []json.RawMessage   `json:"selected"`
			APIEffort json.RawMessage     `json:"apiEffort"`
		}
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &source); e != nil {
		t.Fatalf("Source output: %v %s", e, out)
	}
	if len(source.Capabilities.Blocked) > 0 || len(source.Values) != len(rows) {
		t.Fatalf("Source escaped or missing selection observations: %s", out)
	}
	for index, row := range rows {
		t.Run(row.Name, func(t *testing.T) {
			f := newNativePluginFixture(t)
			f.write(t, filepath.Join(f.cwd, ".claude", "settings.json"), `{}`)
			f.write(t, filepath.Join(f.config, "settings.json"), string(rawMain(row.Settings)))
			var mu sync.Mutex
			posts := []protocol.JSONObject{}
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				var request protocol.JSONObject
				if e := request.UnmarshalJSON(body); e != nil {
					t.Error(e)
					return
				}
				mu.Lock()
				posts = append(posts, request)
				mu.Unlock()
				emitPlanFixture(w, 1, "", "picker physical request")
			}))
			defer peer.Close()
			f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
			f.n.Environment["ANTHROPIC_API_KEY"] = "private-menu-model"
			f.n.Environment["ANTHROPIC_DEFAULT_SONNET_MODEL"] = "fixture-menu-model"
			f.n.Environment["ANTHROPIC_DEFAULT_SONNET_MODEL_SUPPORTED_CAPABILITIES"] = "effort,max_effort"
			f.n.Environment["CLAUDE_CODE_DISABLE_FAST_MODE"] = "1"
			f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
			disabled := false
			m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{Model: "sonnet", ComputerUse: &disabled, SystemPrompt: "actual menu selection", MaxTurns: 5, Tools: []string{"Read"}, PermissionMode: "default", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: runtime.GOOS, Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
			if e = m.StartSession(context.Background()); e != nil {
				t.Fatal(e)
			}
			if index == 0 {
				if e = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
					for i, label := range labels {
						got, e := m.pickerLabel(m.nativeModelSelector, pickerPointer(label))
						if e != nil {
							return e
						}
						if got != source.Labels[i] {
							return fmt.Errorf("original command label %s Go=%s Source=%s", label, got, source.Labels[i])
						}
					}
					return nil
				}); e != nil {
					t.Fatal(e)
				}
			}
			picker, e := m.NativeModelPicker(context.Background(), true)
			if e != nil {
				t.Fatal(e)
			}
			if row.Skip {
				if e = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error { m.modelPicker.pending.SkipSettingsWrite = true; return nil }); e != nil {
					t.Fatal(e)
				}
			}
			_, e = m.ApplyNativeModelPicker(context.Background(), picker.ID, tui.ModelPickerResult{Model: pickerPointer("sonnet"), Effort: pickerPointer(row.Effort), HasToggledEffort: row.Toggled})
			if e != nil {
				t.Fatal(e)
			}
			disk, e := os.ReadFile(filepath.Join(f.config, "settings.json"))
			if e != nil {
				t.Fatal(e)
			}
			var actual protocol.JSONObject
			if e = actual.UnmarshalJSON(disk); e != nil {
				t.Fatal(e)
			}
			want := source.Values[index]
			if !reflect.DeepEqual(pickerCoverageJSON(rawMain(actual)), pickerCoverageJSON(rawMain(want.Settings))) {
				t.Fatalf("real Source persistence differs Go%s Source%s", rawMain(actual), rawMain(want.Settings))
			}
			if _, e = m.ApplyNativeModelPicker(context.Background(), picker.ID, tui.ModelPickerResult{Cancelled: true}); e == nil {
				t.Fatal("consumed UI callback accepted again")
			}
			if e = m.RunTurn(context.Background(), protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "after actual menu selection"})})); e != nil {
				t.Fatal(e)
			}
			mu.Lock()
			requests := append([]protocol.JSONObject{}, posts...)
			mu.Unlock()
			if len(requests) != 1 {
				t.Fatalf("menu unexpectedly queried model %d times", len(requests))
			}
			if requests[0].String("model") != "fixture-menu-model" {
				t.Fatal("raw alias wasn't resolved by actual shared Host", rawMain(requests[0].Get("model")))
			}
			got := pickerCoverageObject(requests[0].Get("output_config")).Get("effort")
			if !bytes.Equal(got, want.APIEffort) {
				t.Fatalf("next physical POST effort %s want original resolveAppliedEffort %s", got, want.APIEffort)
			}
		})
	}
}

func pickerCoverageJSON(value []byte) any { var out any; _ = json.Unmarshal(value, &out); return out }
func pickerCoverageObject(value []byte) protocol.JSONObject {
	var out protocol.JSONObject
	_ = out.UnmarshalJSON(value)
	return out
}
