package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
)

func TestActualSourceTmuxAsynchronousCommandRewakeAndJSONHeaderPhysicalLifecycle(t *testing.T) {
	for _, kind := range []string{"config", "rewake", "header"} {
		for _, outside := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/outside-%t", kind, outside), func(t *testing.T) {
				f := newNativePluginFixture(t)
				f.n.CWD = f.home
				f.n.Environment["PATH"] = "/usr/bin:/bin"
				f.n.Environment["CLAUDE_CODE_TMPDIR"] = f.n.Temp
				sourceHome := filepath.Join(f.home, "source")
				if e := os.MkdirAll(filepath.Join(sourceHome, ".claude"), 0700); e != nil {
					t.Fatal(e)
				}
				scriptText := func(home string) string {
					header := ""
					code := ""
					if kind == "header" {
						header = "printf '{\"async\":true,\"asyncTimeout\":3000}\\n'\n"
					}
					if kind == "rewake" {
						code = "exit 2\n"
					}
					return "printf '%s' \"$$\" > " + shellTmuxTest(filepath.Join(home, "pid")) + "\n" + header + "while [ ! -f " + shellTmuxTest(filepath.Join(home, "release")) + " ]; do /bin/sleep 0.01; done\nprintf 'owned completed\\n'\n" + code
				}
				f.write(t, filepath.Join(sourceHome, "hook.sh"), scriptText(sourceHome))
				f.write(t, filepath.Join(f.home, "hook.sh"), scriptText(f.home))
				fixture, _ := filepath.Abs("testdata/tmux-async-hook.fixture.ts")
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
				cmd.Dir = sourceHome
				cmd.Env = []string{"HOME=" + sourceHome, "USERPROFILE=" + sourceHome, "CLAUDE_CONFIG_DIR=" + filepath.Join(sourceHome, ".claude"), "TMPDIR=" + filepath.Join(sourceHome, "tmp"), "CLAUDE_CODE_TMPDIR=" + filepath.Join(sourceHome, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + f.home, "CC_HAHA_TMUX_ASYNC_SOURCE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
				input, _ := json.Marshal(map[string]any{"home": sourceHome, "kind": kind, "script": filepath.Join(sourceHome, "hook.sh"), "release": filepath.Join(sourceHome, "release"), "result": filepath.Join(sourceHome, "result.json"), "outside": outside})
				cmd.Stdin = bytes.NewReader(input)
				var stdout, stderr bytes.Buffer
				cmd.Stdout = &stdout
				cmd.Stderr = &stderr
				if e := cmd.Start(); e != nil {
					t.Fatal(e)
				}
				sourceDone := make(chan struct{})
				var sourceError error
				go func() { sourceError = cmd.Wait(); close(sourceDone) }()
				t.Cleanup(func() {
					_ = os.WriteFile(filepath.Join(sourceHome, "release"), []byte("owned"), 0600)
					select {
					case <-sourceDone:
					case <-time.After(5 * time.Second):
						_ = cmd.Process.Kill()
						<-sourceDone
					}
				})

				deadline := time.Now().Add(8 * time.Second)
				var early struct {
					Result  json.RawMessage
					Pending []protocol.JSONObject
				}
				for {
					data, e := os.ReadFile(filepath.Join(sourceHome, "result.json"))
					if e == nil {
						e = json.Unmarshal(data, &early)
						if e != nil {
							t.Fatal(e, string(data))
						}
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("actual Source never backgrounded: %v", e)
					}
					time.Sleep(5 * time.Millisecond)
				}
				owner := cooperative.New()
				defer owner.Close()
				var work sync.WaitGroup
				h, e := newNativeTmuxHookRuntime(owner, f.n, f.n.Environment, "tmux-source-session", runtime.GOOS, &work)
				if e != nil {
					t.Fatal(e)
				}
				defer h.Close(context.Background())
				hook := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("command")}, protocol.JSONField{Name: "command", Value: quoteMain("exec /bin/sh " + string(quoteMain(filepath.Join(f.home, "hook.sh"))))}, protocol.JSONField{Name: "timeout", Value: json.RawMessage("10")})
				if kind == "config" {
					hook.Set("async", json.RawMessage("true"))
				}
				if kind == "rewake" {
					hook.Set("asyncRewake", json.RawMessage("true"))
				}
				var output queryhooks.CommandOutput
				var pending []queryhooks.AsyncHook
				var outsideResult []queryhooks.OutsideResult
				e = owner.Run(ctx, func(scope *cooperative.Scope) error {
					run := func(ctx context.Context, call queryhooks.Context, hook, input protocol.JSONObject) (queryhooks.CommandOutput, error) {
						var err error
						output, err = h.Run(ctx, call, hook, input)
						return output, err
					}
					if outside {
						settings, e := NewNativeProcessSettings(f.n, runtime.GOOS, State{})
						if e != nil {
							return e
						}
						defer settings.Close(context.Background())
						// Physical old-format settings, parsed by the same actual
						// pre-CLI producer used by RunNativeTmuxWorktree.
						matchers, _ := json.Marshal(map[string]any{"hooks": map[string]any{"WorktreeCreate": []any{map[string]any{"hooks": []any{hook}}}}})
						if e = os.WriteFile(filepath.Join(f.config, "settings.json"), matchers, 0600); e != nil {
							return e
						}
						if e = settings.ensureSettings(); e != nil {
							return e
						}
						factory, e := messages.NewFactory(uuidMain, f.n.Now)
						if e != nil {
							return e
						}
						service, e := queryhooks.New(queryhooks.Options{Profile: queryhooks.ResolvedSettingsProfile, Settings: settings.settings, SettingsState: settings.settingsState, PluginBaseAbsent: true, MainSessionID: "tmux-source-session", UUID: func() string { return "owned-source-hook" }, MergedSettings: settings.startup.GetInitialSettings, Commands: &queryhooks.CommandBindings{Owner: owner, Factory: factory, Timestamp: func() string { return "original-time" }, NowMillis: f.n.NowMillis, Store: &queryhooks.CommandStore{}, Run: run, Input: func(_ context.Context, c queryhooks.Context) (protocol.JSONObject, error) {
							return protocol.NewJSONObject(protocol.JSONField{Name: "session_id", Value: quoteMain("tmux-source-session")}, protocol.JSONField{Name: "hook_event_name", Value: quoteMain(c.HookEvent)}, protocol.JSONField{Name: "cwd", Value: quoteMain(f.home)}, protocol.JSONField{Name: "name", Value: quoteMain("private")}), nil
						}}})
						if e != nil {
							return e
						}
						outsideResult, e = service.ExecuteOutside(ctx, queryhooks.OutsideInput{Event: "WorktreeCreate", SessionID: "tmux-source-session", Timeout: time.Minute * 10, Fields: protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: quoteMain("private")}), Call: queryhooks.Context{Scope: scope}})
						if e != nil {
							return e
						}
					} else {
						_, e = run(ctx, queryhooks.Context{Scope: scope, HookID: "owned-source-hook", HookName: "WorktreeCreate", HookEvent: "WorktreeCreate"}, hook, protocol.NewJSONObject())
						if e != nil {
							return e
						}
					}
					pending, e = h.registry.Pending(scope)
					return e
				})
				if e != nil {
					t.Fatal(e)
				}
				var actual any
				if outside {
					raw, _ := json.Marshal(outsideResult)
					_ = json.Unmarshal(raw, &actual)
				} else {
					dto := map[string]any{"stdout": output.Stdout, "stderr": output.Stderr, "output": output.Output, "status": float64(output.Code)}
					if kind != "header" {
						dto["backgrounded"] = output.Backgrounded
					}
					actual = dto
				}
				var original any
				_ = json.Unmarshal(early.Result, &original)
				if outside {
					for _, pair := range []struct {
						value any
						home  string
					}{{actual, f.home}, {original, sourceHome}} {
						for _, row := range pair.value.([]any) {
							object := row.(map[string]any)
							object["command"] = strings.ReplaceAll(object["command"].(string), pair.home, "/OWNED")
						}
					}
				}
				if !reflect.DeepEqual(actual, original) {
					t.Fatalf("original command process %s source=%#v go=%#v", kind, original, actual)
				}

				if !output.Backgrounded || output.AsyncProcess == nil {
					t.Fatal("actual Go producer did not transfer real process")
				}
				wantPending := 1
				if kind == "rewake" {
					wantPending = 0
				}
				if len(pending) != wantPending || len(early.Pending) != wantPending {
					t.Fatal("native vs Source registry admission", len(pending), early.Pending)
				}
				if wantPending == 1 {
					if pending[0].HookEvent != "WorktreeCreate" || pending[0].HookName != "WorktreeCreate" {
						t.Fatal(pending[0])
					}
					var timeout float64
					_ = json.Unmarshal(early.Pending[0].Get("timeout"), &timeout)
					if pending[0].Timeout != timeout {
						t.Fatal(pending[0].Timeout, timeout)
					}
				}
				// Both native processes are still physically waiting at this point.
				if e = os.WriteFile(filepath.Join(sourceHome, "release"), []byte("owned"), 0600); e != nil {
					t.Fatal(e)
				}
				f.write(t, filepath.Join(f.home, "release"), "owned")
				<-sourceDone
				if e = sourceError; e != nil {
					t.Fatalf("actual Source process %v %.4000s", e, stderr.String())
				}
				select {
				case <-output.AsyncProcess.Done():
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				var final struct {
					Responses     []protocol.JSONObject
					Notifications []struct{ Value string }
					PIDs          []int
					Result        json.RawMessage
				}
				if e = json.Unmarshal(stdout.Bytes(), &final); e != nil {
					t.Fatal(e, stdout.String(), stderr.String())
				}
				var responses []protocol.JSONObject
				e = owner.Run(ctx, func(scope *cooperative.Scope) error {
					var e error
					responses, e = h.registry.Responses(scope)
					return e
				})
				if e != nil {
					t.Fatal(e)
				}
				if kind == "rewake" {
					// Delivery is the real queued owner callback, not one assumed scheduler tick.
					for {
						ready := false
						e = owner.Run(ctx, func(scope *cooperative.Scope) error { ready = len(h.notifications) > 0; return nil })
						if e != nil {
							t.Fatal(e)
						}
						if ready {
							break
						}
						if ctx.Err() != nil {
							t.Fatal(ctx.Err())
						}
						time.Sleep(5 * time.Millisecond)
					}
					e = owner.Run(ctx, func(scope *cooperative.Scope) error {
						if len(h.notifications) != len(final.Notifications) {
							return fmt.Errorf("Source notification count %d Go %d", len(final.Notifications), len(h.notifications))
						}
						for i, row := range final.Notifications {
							if h.notifications[i] != row.Value {
								return fmt.Errorf("Source notification %q Go %q", row.Value, h.notifications[i])
							}
						}
						return nil
					})
					if e != nil {
						t.Fatal(e)
					}
				} else {
					if len(responses) != 1 || len(final.Responses) != 1 {
						t.Fatal(responses, final.Responses)
					}
					// Process IDs and root paths are physical and intentionally different;
					// compare all other actual producer fields, normalizing only those IDs.
					original := final.Responses[0].Clone()
					actual := responses[0].Clone()
					for _, value := range []*protocol.JSONObject{&original, &actual} {
						value.Set("processId", quoteMain("actual-process"))
						text := value.String("stdout")
						if strings.Contains(text, "Full output saved to: ") {
							text = text[:strings.Index(text, "Full output saved to: ")] + "Full output saved to: /OWNED/OUTPUT"
							value.Set("stdout", quoteMain(text))
						}
					}
					a, _ := actual.MarshalJSON()
					b, _ := original.MarshalJSON()
					var aa, bb any
					_ = json.Unmarshal(a, &aa)
					_ = json.Unmarshal(b, &bb)
					if !reflect.DeepEqual(aa, bb) {
						t.Fatal(string(a), string(b))
					}
				}
				if e = h.Close(ctx); e != nil {
					t.Fatal(e)
				}
				work.Wait()
			})
		}
	}
}
