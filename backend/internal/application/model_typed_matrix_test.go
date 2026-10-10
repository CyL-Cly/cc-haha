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
	"strings"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

type typedModelSourceRow struct {
	Name           string            `json:"name"`
	Args           string            `json:"args"`
	Settings       map[string]any    `json:"settings"`
	State          map[string]any    `json:"state"`
	Env            map[string]string `json:"env,omitempty"`
	Global         map[string]any    `json:"global,omitempty"`
	InitializeFast bool              `json:"initializeFast,omitempty"`
	Ask            bool              `json:"ask,omitempty"`
	ContinueStore  bool              `json:"continueStore,omitempty"`
	SelectModel    *string           `json:"selectModel,omitempty"`
	Notifications  bool              `json:"notifications,omitempty"`
}

type typedModelSourceResult struct {
	Name, Notice    string
	Settings, State map[string]any
	Messages        []map[string]any
	ShouldQuery     bool
	TransportErrors []map[string]any
}

func runTypedModelSource(t *testing.T, rows []typedModelSourceRow, origin string, certificate ...string) []typedModelSourceResult {
	return runTypedModelSourceCredentials(t, rows, origin, nil, certificate...)
}
func runTypedModelSourceCredentials(t *testing.T, rows []typedModelSourceRow, origin string, secure any, certificate ...string) []typedModelSourceResult {
	t.Helper()
	bun := os.Getenv("CC_HAHA_PINNED_BUN")
	if bun == "" {
		t.Skip("actual Source requires explicitly pinned Bun")
	}
	home := t.TempDir()
	for _, leaf := range []string{"tmp", "config", "cache"} {
		if err := os.Mkdir(filepath.Join(home, leaf), 0700); err != nil {
			t.Fatal(err)
		}
	}
	fixture, err := filepath.Abs("../../testdata/native-model-command.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := filepath.Abs("../../testdata/loopback-only.sb")
	if err != nil {
		t.Fatal(err)
	}
	cert := ""
	if len(certificate) > 0 {
		cert = certificate[0]
	}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": rows, "origin": origin, "certificate": cert, "secure": secure})
	cmd := exec.Command("/usr/bin/sandbox-exec", "-f", profile, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Stdin = bytes.NewReader(input)
	base := origin
	if base == "" {
		base = "http://127.0.0.1:43210"
	}
	cmd.Env = []string{"BUN_OPTIONS=--no-env-file", "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "XDG_CONFIG_HOME=" + filepath.Join(home, "config"), "XDG_CACHE_HOME=" + filepath.Join(home, "cache"), "PATH=/usr/bin:/bin", "ANTHROPIC_API_KEY=fixture-model-key", "ANTHROPIC_CUSTOM_HEADERS=X-M680-Client: Source", "ANTHROPIC_BASE_URL=" + base, "CC_HAHA_MODEL_COMMAND_FIXTURE=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "NODE_ENV=production"}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil || stderr.Len() > 0 {
		t.Fatalf("actual Source command %v: %s", err, stderr.String())
	}
	var result struct {
		Rows            []typedModelSourceResult
		TransportErrors []map[string]any
	}
	if err = json.Unmarshal(out, &result); err != nil || len(result.Rows) != len(rows) {
		t.Fatal(err, string(out))
	}
	if len(result.TransportErrors) > 0 {
		t.Logf("original actual transport errors: %v", result.TransportErrors)
	}
	return result.Rows
}

func TestTypedModelArgumentsActualLocalJSXStorePersistenceAndNextASK(t *testing.T) {
	rows := []typedModelSourceRow{}
	add := func(name, arg string, settings map[string]any, env map[string]string) {
		if settings == nil {
			settings = map[string]any{"model": "sonnet", "effortLevel": "low", "future": map[string]any{"keep": true}}
		}
		rows = append(rows, typedModelSourceRow{Name: name, Args: arg, Settings: settings, Env: env, State: map[string]any{"mainLoopModel": "sonnet", "mainLoopModelForSession": nil, "fastMode": false, "effortValue": "low"}})
	}
	for _, arg := range []string{"help", "-h", "--help", "list", "show", "display", "current", "view", "get", "check", "describe", "print", "version", "about", "status", "?"} {
		add("command-"+arg, arg, nil, nil)
	}
	for _, arg := range []string{"default", "sonnet", "OPUS", "haiku", "best", "fable", "opusplan", "fable[1m]", "sonnet[1m]", "opus[1m]"} {
		add("select-"+arg, arg, nil, nil)
	}
	add("allowlist-blocks-opus", "opus", map[string]any{"model": "sonnet", "availableModels": []string{"haiku"}, "future": true}, nil)
	add("allowlist-default-bypasses-empty", "default", map[string]any{"model": "sonnet", "availableModels": []string{}, "future": true}, nil)
	add("allowlist-version-boundary", "opus", map[string]any{"model": "sonnet", "availableModels": []string{"opus", "opus-4-5"}, "future": true}, nil)
	add("disabled-sonnet1m", "sonnet[1m]", nil, map[string]string{"CLAUDE_CODE_DISABLE_1M_CONTEXT": "1"})
	add("disabled-opus1m", "opus[1m]", nil, map[string]string{"CLAUDE_CODE_DISABLE_1M_CONTEXT": "1"})
	add("source-sonnet5-exempts1m", "sonnet[1m]", nil, map[string]string{"CLAUDE_CODE_DISABLE_1M_CONTEXT": "1", "ANTHROPIC_DEFAULT_SONNET_MODEL": "claude-sonnet-5"})
	add("prevalidated-custom-preserves-case", "  MiXeD-Custom  ", nil, map[string]string{"ANTHROPIC_CUSTOM_MODEL_OPTION": "MiXeD-Custom"})
	want := runTypedModelSource(t, rows, "")
	for i, row := range rows {
		t.Run(row.Name, func(t *testing.T) {
			var mu sync.Mutex
			var requests []json.RawMessage
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				mu.Lock()
				requests = append(requests, b)
				mu.Unlock()
				response(w, "typed-next", "", nil, "ACTUAL_TYPED_NEXT")
			}))
			defer peer.Close()
			o := nativeSessionOptions(t, peer.URL)
			o.Arguments.Model = "sonnet"
			for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
				for k, v := range row.Env {
					env[k] = v
				}
			}
			config := o.Environment["CLAUDE_CONFIG_DIR"]
			if err := os.MkdirAll(config, 0700); err != nil {
				t.Fatal(err)
			}
			settingsPath := filepath.Join(config, "settings.json")
			b, _ := json.Marshal(row.Settings)
			if err := os.WriteFile(settingsPath, b, 0600); err != nil {
				t.Fatal(err)
			}
			s, err := NewSession(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			var notices []string
			if err = s.Submit(context.Background(), tui.Input{Mode: "prompt", Text: "/model " + row.Args}, func(event tui.Event) error {
				if event.Kind == tui.Notice {
					notices = append(notices, event.Text)
				}
				return nil
			}); err != nil {
				t.Fatalf("original local JSX notice %q; Go %v", want[i].Notice, err)
			}
			if len(notices) != 1 || notices[0] != want[i].Notice {
				t.Fatal("Source notice", notices, want[i].Notice)
			}
			var history []protocol.JSONObject
			if err = s.process.Owner.Run(context.Background(), func(*cooperative.Scope) error { history = s.process.Controller.History(); return nil }); err != nil {
				t.Fatal(err)
			}
			projected := []map[string]any{}
			for _, record := range history {
				entry := map[string]any{}
				for _, key := range []string{"type", "subtype", "message", "content", "isMeta"} {
					if raw := record.Get(key); raw != nil {
						var v any
						if e := json.Unmarshal(raw, &v); e != nil {
							t.Fatal(e)
						}
						entry[key] = v
					}
				}
				projected = append(projected, entry)
			}
			if !reflect.DeepEqual(projected, want[i].Messages) || want[i].ShouldQuery {
				t.Fatalf("actual generic local JSX completion Source %v Go %v shouldQuery=%v", want[i].Messages, projected, want[i].ShouldQuery)
			}
			var saved map[string]any
			b, err = os.ReadFile(settingsPath)
			if err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(b, &saved); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(saved, want[i].Settings) {
				t.Fatal("actual Source onChange persisted state", saved, want[i].Settings)
			}
			mu.Lock()
			n := len(requests)
			mu.Unlock()
			if n != 0 {
				t.Fatal("alias/default/help/info queried a model", n)
			}
			if strings.HasPrefix(row.Name, "select-") || row.Name == "prevalidated-custom-preserves-case" {
				if err = s.Submit(context.Background(), tui.Input{Text: "actual next typed request"}, nil); err != nil {
					t.Fatal(err)
				}
				mu.Lock()
				last := requests[len(requests)-1]
				mu.Unlock()
				var body map[string]any
				_ = json.Unmarshal(last, &body)
				if row.Name == "prevalidated-custom-preserves-case" && body["model"] != "MiXeD-Custom" {
					t.Fatal("case-sensitive custom model altered", string(last))
				}

			}
		})
	}
}
