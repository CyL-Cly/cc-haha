package querypreflight

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func record(t *testing.T, s string) protocol.JSONObject {
	t.Helper()
	var r protocol.JSONObject
	if e := r.UnmarshalJSON([]byte(s)); e != nil {
		t.Fatal(e)
	}
	return r
}
func testServices(t *testing.T) (*Service, *clistartupenv.Service, *clisettings.State, *cliglobalconfig.Service, *[]string) {
	t.Helper()
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	os.MkdirAll(config, 0700)
	global, e := cliglobalconfig.New(cliglobalconfig.Options{Home: home, Watcher: func(string, time.Duration, func(cliglobalconfig.FileStat)) func() { return func() {} }, Stderr: func(v string) { t.Errorf("unexpected config diagnostic %s", v) }})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { global.Close() })
	if e = global.Enable(); e != nil {
		t.Fatal(e)
	}
	state, _ := clisettings.NewState(func() float64 { return 1700000000000 })
	settings, e := clisettings.NewService(clisettings.Options{State: state, ConfigHome: config, OriginalCWD: home, Profile: clisettings.ExternalReleaseProfile, Now: func() float64 { return 1700000000000 }, PID: 1})
	if e != nil {
		t.Fatal(e)
	}
	env := protocol.NewJSONObject()
	env.Set("HOME", quote(home))
	env.Set("CLAUDE_CONFIG_DIR", quote(config))
	environment, _ := clistartupenv.NewEnvironment(env)
	startup, e := clistartupenv.New(clistartupenv.Options{Global: global, Settings: settings, SettingsState: state, Environment: environment, ConfigHome: config, Host: clistartupenv.Host{Profile: clistartupenv.EmptyPolicyReleaseProfile, RemoteCacheAbsent: true, MDMCacheAbsent: true, HKCUCacheAbsent: true, PolicyFilesAbsent: true, PluginBaseAbsent: true}})
	if e != nil {
		t.Fatal(e)
	}
	alloc := []string{}
	n := 1
	factory, _ := messages.NewFactory(func() string { n++; v := fmt.Sprintf("probe-id-%d", n); alloc = append(alloc, "uuid:"+v); return v }, func() time.Time {
		alloc = append(alloc, "date:2023-11-14T22:13:20.000Z")
		return time.UnixMilli(1700000000000)
	})
	service, e := New(Dependencies{Global: global, InitialSettings: startup, Factory: factory})
	if e != nil {
		t.Fatal(e)
	}
	return service, startup, state, global, &alloc
}
func base(t *testing.T) Input {
	return Input{History: []queryloop.Message{{Record: record(t, `{"type":"user","message":{"content":"0123456789abcdef","role":"user"}}`)}}, SystemPrompt: []json.RawMessage{quote("original system")}, UserContext: protocol.NewJSONObject(), SystemContext: protocol.NewJSONObject(), MainLoopModel: "deepseek-flash", QuerySource: "repl_main_thread", PermissionMode: "default", OriginalEnvironment: map[string]string{}}
}
func stringsOf(t *testing.T, history []queryloop.Message) []string {
	t.Helper()
	out := []string{}
	for _, m := range history {
		r, e := m.MarshalJSON()
		if e != nil {
			t.Fatal(e)
		}
		out = append(out, string(r))
	}
	return out
}
func TestOriginalSourceWholeQueryPreflightComponent(t *testing.T) {
	var rows []struct {
		Name  string
		Input struct {
			HistorySources, SystemSources                       []string
			UserContextSource, SystemContextSource, Model, Mode string
			Env                                                 map[string]string
			SuppressWarning                                     bool
		}
		Expected struct {
			ToolContextHistory, ModelHistory, OriginalAfter []string
			Blocked                                         *string
			WarningBefore                                   bool
		}
	}
	raw, e := os.ReadFile("testdata/whole-query-source.json")
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(raw, &rows); e != nil {
		t.Fatal(e)
	}
	for _, row := range rows {
		t.Run(row.Name, func(t *testing.T) {
			service, _, _, _, alloc := testServices(t)
			in := base(t)
			in.History = nil
			for _, s := range row.Input.HistorySources {
				in.History = append(in.History, queryloop.Message{Record: record(t, s)})
			}
			in.SystemPrompt = nil
			for _, s := range row.Input.SystemSources {
				in.SystemPrompt = append(in.SystemPrompt, json.RawMessage(s))
			}
			in.UserContext = record(t, row.Input.UserContextSource)
			in.SystemContext = record(t, row.Input.SystemContextSource)
			in.MainLoopModel = row.Input.Model
			in.OriginalEnvironment = row.Input.Env
			if row.Input.SuppressWarning {
				service.SuppressWarning()
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if row.Input.Mode == "abort-before" {
				cancel()
			}
			out, e := service.Run(ctx, in)
			if e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(stringsOf(t, out.ModelHistory), row.Expected.ModelHistory) || !reflect.DeepEqual(stringsOf(t, out.ToolContextHistory), row.Expected.ToolContextHistory) || !reflect.DeepEqual(stringsOf(t, in.History), row.Expected.OriginalAfter) {
				t.Fatalf("original projection mismatch %+v", out)
			}
			if row.Expected.Blocked != nil {
				if out.Blocked == nil {
					t.Fatal("missing real factory error")
				}
				raw, _ := out.Blocked.MarshalJSON()
				if string(raw) != *row.Expected.Blocked {
					t.Fatalf("factory bytes %s expected %s", raw, *row.Expected.Blocked)
				}
				if !reflect.DeepEqual(*alloc, []string{"uuid:probe-id-2", "date:2023-11-14T22:13:20.000Z", "uuid:probe-id-3"}) {
					t.Fatal(*alloc)
				}
			} else if out.Blocked != nil || len(*alloc) != 0 {
				t.Fatal("unexpected preflight allocation")
			}
			if service.WarningSuppressed() || out.Statistics.WarningBefore != row.Expected.WarningBefore || out.Statistics.SettingsGets != 2 {
				t.Fatalf("warning/settings lifecycle %+v", out.Statistics)
			}
		})
	}
}
func TestLiveEnvelopeIdentityAndLocalProjection(t *testing.T) {
	service, _, _, _, _ := testServices(t)
	in := base(t)
	in.OriginalEnvironment["DISABLE_COMPACT"] = "1"
	env := &streamhistory.Envelope{Record: record(t, `{"type":"assistant","uuid":"live"}`), Message: record(t, `{"model":"old","id":"response","content":[]}`), Content: []*streamhistory.Block{{Object: record(t, `{"type":"thinking","thinking":"secret","signature":"signed"}`)}, {Object: record(t, `{"type":"text","text":"visible"}`)}}, Usage: &streamhistory.Usage{Raw: json.RawMessage(`{"input_tokens":1,"output_tokens":1}`)}}
	in.History = append([]queryloop.Message{{Assistant: env}}, in.History...)
	before, _ := env.MarshalJSON()
	out, e := service.Run(context.Background(), in)
	if e != nil {
		t.Fatal(e)
	}
	after, _ := env.MarshalJSON()
	if string(before) != string(after) || out.ToolContextHistory[0].Assistant != env || out.ModelHistory[0].Assistant != nil || out.ModelHistory[1].Record.String("type") != "user" {
		t.Fatal("projection lost live reference or mutated original")
	}
	env.Content[1].Object.Set("text", quote("later"))
	if !strings.Contains(stringsOf(t, out.ToolContextHistory)[0], "later") || strings.Contains(stringsOf(t, out.ModelHistory)[0], "later") {
		t.Fatal("local clone must freeze changed protected record")
	}
	in.MainLoopModel = "fixture-model"
	env.Message.Set("model", quote("fixture-model"))
	out, e = service.Run(context.Background(), in)
	if e != nil || out.ModelHistory[0].Assistant != env {
		t.Fatalf("unchanged live envelope not retained %v", e)
	}
}
func TestActualSharedSettingsAndGlobalCaches(t *testing.T) {
	service, startup, state, global, _ := testServices(t)
	in := base(t)
	cfg, e := startup.GetInitialSettings()
	if e != nil {
		t.Fatal(e)
	}
	cfg.Set("model", quote("fixture-model"))
	same, _ := startup.GetInitialSettings()
	if same != cfg {
		t.Fatal("real getter pointer cache lost")
	}
	out, e := service.Run(context.Background(), in)
	if e != nil || out.Statistics.SettingsGets != 4 {
		t.Fatalf("runtime setting+allowlist must read twice each: %+v %v", out.Statistics, e)
	}
	cfg.Set("availableModels", json.RawMessage(`[]`))
	_, e = service.Run(context.Background(), in)
	requireCap(t, e)
	state.ResetSettingsCache()
	out, e = service.Run(context.Background(), in)
	if e != nil || out.Statistics.SettingsGets != 2 {
		t.Fatal("reset must use real resolver")
	}
	gc, _ := global.Get()
	gc.Set("autoCompactEnabled", json.RawMessage("false"))
	out, e = service.Run(context.Background(), in)
	if e != nil || out.Statistics.AutoChecked || out.Statistics.GlobalGets != 3 {
		t.Fatalf("real global cache pointer disabled %+v %v", out.Statistics, e)
	}
	in.MainLoopModelOverride = json.RawMessage("null")
	out, e = service.Run(context.Background(), in)
	if e != nil || out.Statistics.SettingsGets != 0 {
		t.Fatal("explicit null override skips settings")
	}
	in.MainLoopModelOverride = quote("fixture-model")
	out, e = service.Run(context.Background(), in)
	if e != nil || out.Statistics.SettingsGets != 2 {
		t.Fatal("override allowlist getters")
	}
}
func requireCap(t *testing.T, e error) {
	t.Helper()
	var cap *CapabilityError
	if !errors.As(e, &cap) {
		t.Fatalf("typed capability required: %v", e)
	}
}
func TestPolicyBranchesAndActiveCompactionCap(t *testing.T) {
	for _, key := range []string{"DISABLE_COMPACT", "DISABLE_AUTO_COMPACT"} {
		t.Run(key, func(t *testing.T) {
			s, _, _, _, _ := testServices(t)
			in := base(t)
			in.OriginalEnvironment[key] = " true "
			in.OriginalEnvironment["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] = "16"
			in.OriginalEnvironment["CLAUDE_CODE_BLOCKING_LIMIT_OVERRIDE"] = "5"
			out, e := s.Run(context.Background(), in)
			if e != nil || out.Blocked != nil || out.Statistics.AutoChecked {
				t.Fatalf("disabled: %+v %v", out, e)
			}
		})
	}
	for _, source := range []string{"compact", "session_memory"} {
		t.Run(source, func(t *testing.T) {
			s, _, _, _, _ := testServices(t)
			in := base(t)
			in.QuerySource = source
			in.OriginalEnvironment["CLAUDE_CODE_BLOCKING_LIMIT_OVERRIDE"] = "1"
			out, e := s.Run(context.Background(), in)
			if e != nil || out.Blocked != nil || out.Statistics.GlobalGets != 0 || out.Statistics.FixedPrefixEstimate == 0 {
				t.Fatal("recursive guard order")
			}
		})
	}
	s, _, _, _, _ := testServices(t)
	in := base(t)
	in.OriginalEnvironment["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] = "16"
	_, e := s.Run(context.Background(), in)
	requireCap(t, e)
	n := 3.
	in.ConsecutiveFailures = &n
	out, e := s.Run(context.Background(), in)
	if e != nil || out.Statistics.FixedPrefixEstimate != 0 {
		t.Fatalf("breaker %+v %v", out, e)
	}
	in = base(t)
	in.History[0].Record = record(t, `{"type":"assistant","message":{"model":"deepseek-flash","id":"anchor","content":[],"usage":{"input_tokens":10,"output_tokens":1}}}`)
	out, e = s.Run(context.Background(), in)
	if e != nil || out.Statistics.AutoEstimate != 11 {
		t.Fatalf("usable anchor excludes fixed prefix %+v %v", out.Statistics, e)
	}
	in.OriginalEnvironment["CLAUDE_AUTOCOMPACT_PCT_OVERRIDE"] = "0.0001"
	_, e = s.Run(context.Background(), in)
	requireCap(t, e)
}
func TestOriginalGuardsAndNoEarlyAbort(t *testing.T) {
	s, _, _, _, _ := testServices(t)
	if _, e := New(Dependencies{}); e == nil {
		t.Fatal("missing dependencies")
	}
	for _, mutate := range []func(*Input){func(i *Input) { i.MainLoopModel = "sonnet" }, func(i *Input) { i.PermissionMode = "plan" }, func(i *Input) { i.QuerySource = "" }, func(i *Input) { i.ToolCount = 1 }, func(i *Input) { i.ContentReplacementStatePresent = true }, func(i *Input) { i.UserContext = protocol.JSONObject{} }, func(i *Input) { i.SystemContext = protocol.JSONObject{} }, func(i *Input) { i.OriginalEnvironment["UNKNOWN_FEATURE"] = "1" }, func(i *Input) { i.OriginalEnvironment["USER_TYPE"] = "ant" }, func(i *Input) { i.History = []queryloop.Message{{}} }, func(i *Input) { i.SystemPrompt = []json.RawMessage{json.RawMessage("true")} }} {
		in := base(t)
		mutate(&in)
		_, e := s.Run(context.Background(), in)
		requireCap(t, e)
	}
	in := base(t)
	in.OriginalEnvironment["DISABLE_COMPACT"] = "1"
	in.OriginalEnvironment["CLAUDE_CODE_BLOCKING_LIMIT_OVERRIDE"] = "4"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, e := s.Run(ctx, in)
	if e != nil || out.Blocked == nil {
		t.Fatal("abort before must still reach real blocking factory")
	}
}
func TestContextUTF16NumericOrderAndEstimation(t *testing.T) {
	ctx := record(t, `{"z":"\ud800","2":"second","1":"first","x":null}`)
	out, e := appendContext([]json.RawMessage{quote(""), json.RawMessage("null"), quote("system")}, ctx)
	if e != nil || string(out[1]) != `"1: first\n2: second\nz: \ud800\nx: null"` {
		t.Fatalf("context UTF16/order: %s %v", out, e)
	}
	_, e = appendContext(nil, record(t, `{"x":{"toString":1}}`))
	var coercion *protocol.ExactJSONStringTypeError
	if !errors.As(e, &coercion) {
		t.Fatal(e)
	}
	for _, v := range []string{"1", "true", " YES ", "on"} {
		if !envTruthy(v) {
			t.Fatal(v)
		}
	}
	for _, v := range []string{"0", "false", "no", ""} {
		if envTruthy(v) {
			t.Fatal(v)
		}
	}
	for _, raw := range []string{"null", "false", "0", `""`} {
		if truthy(json.RawMessage(raw)) {
			t.Fatal(raw)
		}
	}
	for _, raw := range []string{`"x"`, "1", "[]", "{}", "true"} {
		if !truthy(json.RawMessage(raw)) {
			t.Fatal(raw)
		}
	}
}
func TestThresholdParseIntFloatOriginalSemantics(t *testing.T) {
	cases := []struct {
		env                  map[string]string
		effective, threshold float64
	}{{nil, 180000, 167000}, {map[string]string{"CLAUDE_CODE_AUTO_COMPACT_WINDOW": "16tail"}, 12, 8}, {map[string]string{"CLAUDE_CODE_AUTO_COMPACT_WINDOW": "\ufeff+32e2"}, 24, 16}, {map[string]string{"CLAUDE_AUTOCOMPACT_PCT_OVERRIDE": "1e1tail"}, 180000, 18000}, {map[string]string{"CLAUDE_AUTOCOMPACT_PCT_OVERRIDE": "101"}, 180000, 167000}, {map[string]string{"CLAUDE_CODE_AUTO_COMPACT_WINDOW": "0x4000"}, 180000, 167000}}
	for _, c := range cases {
		e, tokens := thresholds(c.env)
		if e != c.effective || tokens != c.threshold {
			t.Fatalf("%v => %v %v", c.env, e, tokens)
		}
	}
}

type callbackGetter func() (*protocol.JSONObject, error)

func (f callbackGetter) GetInitialSettings() (*protocol.JSONObject, error) { return f() }
func TestConfigurationFaultsRemainErrorsBeforeModel(t *testing.T) {
	s, _, _, _, alloc := testServices(t)
	in := base(t)
	boom := errors.New("private settings failure")
	s.dep.InitialSettings = callbackGetter(func() (*protocol.JSONObject, error) { return nil, boom })
	in.OriginalEnvironment["DISABLE_COMPACT"] = "1"
	if _, e := s.Run(context.Background(), in); !errors.Is(e, boom) {
		t.Fatal(e)
	}
	s.dep.InitialSettings = callbackGetter(func() (*protocol.JSONObject, error) { return nil, nil })
	if _, e := s.Run(context.Background(), in); e == nil {
		t.Fatal("nil getter must fail")
	}
	calls := 0
	cfg := protocol.NewJSONObject()
	s.dep.InitialSettings = callbackGetter(func() (*protocol.JSONObject, error) {
		calls++
		if calls >= 2 {
			return nil, boom
		}
		return &cfg, nil
	})
	if _, e := s.Run(context.Background(), in); !errors.Is(e, boom) || calls != 2 {
		t.Fatal("second runtime getter failure")
	}
	calls = 0
	in.OriginalEnvironment["ANTHROPIC_MODEL"] = "fixture-model"
	s.dep.InitialSettings = callbackGetter(func() (*protocol.JSONObject, error) {
		calls++
		if calls == 2 {
			return nil, boom
		}
		return &cfg, nil
	})
	if _, e := s.Run(context.Background(), in); !errors.Is(e, boom) {
		t.Fatal("actual allowlist getter fault", e)
	}
	if len(*alloc) != 0 {
		t.Fatal("settings fault must not consume factory")
	}
	global, e := cliglobalconfig.New(cliglobalconfig.Options{Home: t.TempDir()})
	if e != nil {
		t.Fatal(e)
	}
	defer global.Close()
	s.dep.Global = global
	in = base(t)
	if _, e = s.Run(context.Background(), in); e == nil || !strings.Contains(e.Error(), "before allowed") {
		t.Fatal("real unenabled global access", e)
	}
	if _, e = s.warningState(1, in, &Statistics{}); e == nil {
		t.Fatal("warning global failure")
	}
}
func TestMalformedOriginalErrorsAtTheirActualPhase(t *testing.T) {
	for _, raw := range []string{`{"type":"attachment","attachment":{"type":"invoked_skills"}}`, `{"type":"assistant","message":null}`} {
		s, _, _, _, _ := testServices(t)
		in := base(t)
		in.History = []queryloop.Message{{Record: record(t, raw)}}
		_, e := s.Run(context.Background(), in)
		if e == nil {
			t.Fatal("core original guard missing", raw)
		}
	}
	s, _, _, _, _ := testServices(t)
	in := base(t)
	in.History = []queryloop.Message{{Assistant: &streamhistory.Envelope{Record: record(t, `{"type":"assistant"}`), Message: record(t, `{"content":[]}`), Usage: &streamhistory.Usage{Raw: json.RawMessage("invalid")}}}}
	if _, e := s.Run(context.Background(), in); e == nil {
		t.Fatal("malformed live usage must fail")
	}
	in = base(t)
	in.SystemContext.Set("bad", json.RawMessage("not json"))
	if _, e := s.Run(context.Background(), in); e == nil {
		t.Fatal("actual context coercion error")
	}
	in = base(t)
	in.UserContext.Set("bad", json.RawMessage("invalid"))
	if _, e := s.Run(context.Background(), in); e == nil {
		t.Fatal("fixed prefix must not swallow original malformed JSON")
	}
	in = base(t)
	in.History = []queryloop.Message{{Record: record(t, `{"type":"assistant","message":{"model":"deepseek-flash","id":"null-usage","content":[],"usage":null}}`)}}
	if _, e := s.Run(context.Background(), in); e == nil {
		t.Fatal("hasUsable null error before estimate")
	}
	in = base(t)
	in.History = []queryloop.Message{{Record: record(t, `{"type":"user","message":{"content":[{"type":"tool_use","name":123,"input":{}}]}}`)}}
	if _, e := s.Run(context.Background(), in); e == nil {
		t.Fatal("rough unported malformed tool name")
	}
	in = base(t)
	in.OriginalEnvironment["DISABLE_COMPACT"] = "1"
	in.History = []queryloop.Message{{Record: record(t, `{"type":"assistant","message":{"model":123,"id":"bad-model","content":[{"type":"thinking","thinking":"x"}]}}`)}}
	if _, e := s.Run(context.Background(), in); e == nil {
		t.Fatal("signature model coercion error")
	}
	in = base(t)
	in.OriginalEnvironment["DISABLE_COMPACT"] = "1"
	in.History = []queryloop.Message{{Record: record(t, `{"type":"user","message":{"content":[{"type":"tool_use","name":123,"input":{}}]}}`)}}
	if _, e := s.Run(context.Background(), in); e == nil {
		t.Fatal("blocking estimate malformed tool")
	}
	in = base(t)
	in.OriginalEnvironment["DISABLE_COMPACT"] = "1"
	in.OriginalEnvironment["CLAUDE_CODE_BLOCKING_LIMIT_OVERRIDE"] = "4"
	s.dep.Factory = nil
	if _, e := s.Run(context.Background(), in); e == nil {
		t.Fatal("lost shared allocator must fail")
	}
}
func TestFixedPrefixInvalidRawAndOriginalNumbers(t *testing.T) {
	in := base(t)
	in.SystemPrompt = []json.RawMessage{json.RawMessage("invalid")}
	if _, e := fixedEstimate(in); e == nil {
		t.Fatal("invalid raw prompt")
	}
	in = base(t)
	in.SystemContext.Set("x", json.RawMessage("invalid"))
	if _, e := fixedEstimate(in); e == nil {
		t.Fatal("invalid original system")
	}
	in = base(t)
	in.UserContext.Set("x", json.RawMessage("invalid"))
	if _, e := fixedEstimate(in); e == nil {
		t.Fatal("invalid original user")
	}
	s, _, _, _, _ := testServices(t)
	in = base(t)
	in.OriginalEnvironment["CLAUDE_CODE_DISABLE_1M_CONTEXT"] = "1"
	in.OriginalEnvironment["ANTHROPIC_DEFAULT_SONNET_MODEL"] = "fixture-model"
	if _, e := s.Run(context.Background(), in); e != nil {
		t.Fatal(e)
	}
	if !truthy(json.RawMessage("1e400")) || truthy(json.RawMessage("-0")) {
		t.Fatal("JS number truthiness")
	}
	for _, v := range []string{"Infinity", "1e400", "-Infinity"} {
		if parsePrefix(v, floatPrefix) == 0 {
			t.Fatal(v)
		}
	}
}

func TestSerializedSharedServiceLifecycle(t *testing.T) {
	s, _, _, _, _ := testServices(t)
	in := base(t)
	s.SuppressWarning()
	done := make(chan error, 12)
	for i := 0; i < 12; i++ {
		go func() {
			result, e := s.Run(context.Background(), in)
			if e == nil && (result.Blocked != nil || result.Statistics.SettingsGets != 2) {
				e = errors.New("shared finite preflight state changed")
			}
			done <- e
		}()
	}
	for i := 0; i < 12; i++ {
		if e := <-done; e != nil {
			t.Fatal(e)
		}
	}
	if s.WarningSuppressed() {
		t.Fatal("micro warning must clear once and stay clear")
	}
}
