package queryhooks

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
)

func normalizedFixture(t *testing.T, value any) any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var normalized any
	if err := json.Unmarshal(raw, &normalized); err != nil {
		t.Fatal(err)
	}
	return normalized
}

// Expected rows were captured from two equal actual guarded Source runs. Only
// original file/env/history/registration inputs enter the independent service.
func TestActualSourceEmptyHookContract(t *testing.T) {
	raw, err := os.ReadFile("testdata/source_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Input    fixtureInput
		Expected json.RawMessage
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 25 {
		t.Fatalf("expected 25 independent Source profiles, got %d", len(cases))
	}
	for _, test := range cases {
		t.Run(test.Input.Name, func(t *testing.T) {
			test.Input.Home = t.TempDir()
			result, err := observeFixture(test.Input)
			if err != nil {
				t.Fatal(err)
			}
			var expected any
			if err := json.Unmarshal(test.Expected, &expected); err != nil {
				t.Fatal(err)
			}
			if actual := normalizedFixture(t, result); !reflect.DeepEqual(actual, expected) {
				actualRaw, _ := json.MarshalIndent(actual, "", "  ")
				t.Fatalf("actual Source contract mismatch\nactual:%s\nexpected:%s", actualRaw, test.Expected)
			}
		})
	}
}

func newTestService(t *testing.T, files map[string]string) (*Service, string, *atomic.Int64) {
	t.Helper()
	home := t.TempDir()
	for _, dir := range []string{filepath.Join(home, ".claude"), filepath.Join(home, "project/.claude")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for source, raw := range files {
		path := filepath.Join(home, ".claude/settings.json")
		if source == "projectSettings" {
			path = filepath.Join(home, "project/.claude/settings.json")
		}
		if source == "localSettings" {
			path = filepath.Join(home, "project/.claude/settings.local.json")
		}
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
	}
	state, err := clisettings.NewState(func() float64 { return 1700000000000 })
	if err != nil {
		t.Fatal(err)
	}
	settings, err := clisettings.NewService(clisettings.Options{State: state, ConfigHome: filepath.Join(home, ".claude"), OriginalCWD: filepath.Join(home, "project"), Profile: clisettings.ExternalReleaseProfile, PID: 123, Now: func() float64 { return 1700000000000 }})
	if err != nil {
		t.Fatal(err)
	}
	count := &atomic.Int64{}
	service, err := New(Options{Profile: EmptyExternalProfile, Settings: settings, SettingsState: state, OriginalEnv: map[string]string{"CLAUDE_CODE_SIMPLE": "0"}, MainSessionID: "hook-main", PolicyAbsent: true, FlagAbsent: true, PluginBaseAbsent: true, UUID: func() string { count.Add(1); return "own-shared-id" }})
	if err != nil {
		t.Fatal(err)
	}
	return service, home, count
}
func emptyContext() Context {
	return Context{QuerySource: "sdk", ComputerUseInactive: true, GoalInactive: true, TeammateInactive: true}
}
func rejectYield(queryloop.Yield) error { return errors.New("finite empty resolver must never emit") }
func requireCapability(t *testing.T, err error) {
	t.Helper()
	var failure *CapabilityError
	if !errors.As(err, &failure) || failure.Boundary == "" || failure.Error() == "" {
		t.Fatalf("expected explicit capability, got %v", err)
	}
}
func finishedTask(t *testing.T, task *Task, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-task.Done():
	default:
		t.Fatal("empty service did not finish synchronous entry")
	}
}

func TestOriginalEnvironmentAndConstructorCapabilities(t *testing.T) {
	service, _, _ := newTestService(t, nil)
	options := service.options
	for _, change := range []func(*Options){func(o *Options) { o.Profile = "" }, func(o *Options) { o.PolicyAbsent = false }, func(o *Options) { o.FlagAbsent = false }, func(o *Options) { o.PluginBaseAbsent = false }, func(o *Options) { o.OriginalEnv = map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1"} }} {
		copy := options
		change(&copy)
		_, err := New(copy)
		requireCapability(t, err)
	}
	for _, change := range []func(*Options){func(o *Options) { o.Settings = nil }, func(o *Options) { o.SettingsState = nil }, func(o *Options) { o.MainSessionID = "" }, func(o *Options) { o.UUID = nil }} {
		copy := options
		change(&copy)
		if _, err := New(copy); err == nil {
			t.Fatal("missing original host dependency admitted")
		}
	}
	requireCapability(t, service.SetOriginalEnvironment(map[string]string{"CLAUDE_CODE_USE_VERTEX": "1"}))
	for _, input := range []struct {
		raw      string
		expected bool
	}{{"", false}, {"0", false}, {"FALSE", false}, {"1", true}, {"true", true}, {"YES", true}, {"on", true}, {"\u0085yes\u0085", false}} {
		original := map[string]string{"CLAUDE_CODE_SIMPLE": input.raw}
		if err := service.SetOriginalEnvironment(original); err != nil {
			t.Fatal(err)
		}
		original["CLAUDE_CODE_SIMPLE"] = "changed"
		if service.simple() != input.expected {
			t.Fatalf("original SIMPLE %q", input.raw)
		}
	}
}

func TestCacheSafeReferencesAndNonMainRetention(t *testing.T) {
	service, _, _ := newTestService(t, nil)
	if service.CacheSafeParams() != nil {
		t.Fatal("cache should begin undefined")
	}
	object := protocol.NewJSONObject()
	object.Set("type", json.RawMessage(`"user"`))
	assistant := &streamhistory.Envelope{Record: protocol.NewJSONObject()}
	system, host := &struct{}{}, &struct{}{}
	call := emptyContext()
	call.History = []queryloop.Message{{Record: object}}
	call.Assistants = []*streamhistory.Envelope{assistant}
	call.SystemPrompt = system
	call.ToolUseContext = host
	call.UserContext = map[string]string{"before": "value"}
	call.SystemContext = map[string]string{}
	for _, source := range []string{"sdk", "repl_main_thread"} {
		call.QuerySource = source
		decision, err := service.StopHooks(context.Background(), call, rejectYield)
		if err != nil || decision.BlockingMessages == nil || decision.PreventContinuation {
			t.Fatalf("empty normal stop %v %+v", err, decision)
		}
		saved := service.CacheSafeParams()
		if len(saved.Messages) != 2 || saved.Messages[1].Assistant != assistant || saved.Context.SystemPrompt != system || saved.Context.ToolUseContext != host {
			t.Fatal("original references lost")
		}
		saved.Context.UserContext["probe"] = "shared"
		if call.UserContext["probe"] != "shared" {
			t.Fatal("user context was cloned")
		}
		saved.Context.SystemContext["probe"] = "shared"
		if call.SystemContext["probe"] != "shared" {
			t.Fatal("system context was cloned")
		}
		call.History[0] = queryloop.Message{}
		if saved.Messages[0].Record.Get("type") == nil {
			t.Fatal("concatenated array was not copied")
		}
		call.History[0] = queryloop.Message{Record: object}
		call.QuerySource = "agent"
		if _, err := service.StopHooks(context.Background(), call, rejectYield); err != nil {
			t.Fatal(err)
		}
		if saved != service.CacheSafeParams() {
			t.Fatal("nonmain query overwrote process cache")
		}
	}
}

func TestActiveCapabilitiesAndSuppressionGateOrder(t *testing.T) {
	service, _, count := newTestService(t, nil)
	call := emptyContext()
	last := &streamhistory.Envelope{}
	if _, err := service.StartStopFailure(call, nil); err == nil {
		t.Fatal("missing terminal assistant admitted")
	}
	task, err := service.StartPostSampling(call)
	finishedTask(t, task, err)
	service.RegisterProgrammatic()
	_, err = service.StartPostSampling(call)
	requireCapability(t, err)
	service.ClearProgrammatic()
	task, err = service.StartPostSampling(call)
	finishedTask(t, task, err)
	for _, event := range []string{"StopFailure", "Stop"} {
		if err := service.Register(event, []Matcher{{Hooks: []string{"callback"}}}); err != nil {
			t.Fatal(err)
		}
	}
	_, err = service.StartStopFailure(call, last)
	requireCapability(t, err)
	if count.Load() != 0 {
		t.Fatal("StopFailure must not allocate toolUseID")
	}
	_, err = service.StopHooks(context.Background(), call, rejectYield)
	requireCapability(t, err)
	if count.Load() != 1 {
		t.Fatal("Stop existence must allocate before execution capability")
	}
	call.Interactive = true
	task, err = service.StartStopFailure(call, last)
	finishedTask(t, task, err)
	if _, err = service.StopHooks(context.Background(), call, rejectYield); err != nil || count.Load() != 2 {
		t.Fatal("untrusted skip lost pre-gate allocation")
	}
	call.TrustAccepted = true
	_, err = service.StartStopFailure(call, last)
	requireCapability(t, err)
	service.ClearRegistry()
	if err := service.SetSession("hook-main", "Stop", []Matcher{{Hooks: []string{"function"}}}); err != nil {
		t.Fatal(err)
	}
	_, err = service.StopHooks(context.Background(), call, rejectYield)
	requireCapability(t, err)
	if err := service.SetOriginalEnvironment(map[string]string{"CLAUDE_CODE_SIMPLE": "1"}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.StopHooks(context.Background(), call, rejectYield); err != nil {
		t.Fatal(err)
	}
	service.ClearSession("absent")
}

func TestRegistrationsValidateBeforeMutationAndCopyOriginalDescriptors(t *testing.T) {
	service, _, _ := newTestService(t, nil)
	for _, input := range []struct {
		event    string
		matchers []Matcher
	}{{"Other", nil}, {"Stop", []Matcher{{Matcher: "*"}}}, {"Stop", []Matcher{{Hooks: []string{"command"}}}}} {
		requireCapability(t, service.Register(input.event, input.matchers))
		requireCapability(t, service.SetSession("hook-main", input.event, input.matchers))
	}
	requireCapability(t, service.SetSession("", "Stop", nil))
	if service.registry.registered != nil || len(service.registry.sessions) != 0 {
		t.Fatal("validation mutated registry")
	}
	input := []Matcher{{Hooks: []string{"callback"}}}
	if err := service.Register("Stop", input); err != nil {
		t.Fatal(err)
	}
	input[0].Hooks[0] = "changed"
	if service.registry.registered["Stop"][0].Hooks[0] != "callback" {
		t.Fatal("registered descriptor borrowed caller slice")
	}
	input = []Matcher{{Hooks: []string{"function"}}}
	if err := service.SetSession("hook-main", "Stop", input); err != nil {
		t.Fatal(err)
	}
	input[0].Hooks[0] = "changed"
	if service.registry.sessions["hook-main"].events["Stop"][0].Hooks[0] != "function" {
		t.Fatal("session descriptor borrowed caller slice")
	}
}

func TestCapabilityAndSettingsFailuresDoNotBecomeNoopTasks(t *testing.T) {
	for _, raw := range []string{`{"hooks":{"Stop":[{"hooks":[]}]}}`, `{"hooks":{"Other":[]}}`} {
		service, _, _ := newTestService(t, map[string]string{"userSettings": raw})
		if _, err := service.Snapshot(); err == nil {
			t.Fatal("active/unknown configured hooks silently discarded")
		}
		if _, err := service.StartStopFailure(emptyContext(), &streamhistory.Envelope{}); err == nil {
			t.Fatal("failure entry discarded settings capability")
		}
		if _, err := service.StopHooks(context.Background(), emptyContext(), rejectYield); err == nil {
			t.Fatal("normal stop discarded settings capability")
		}
		if err := service.UpdateSnapshot(); err == nil {
			t.Fatal("snapshot update discarded settings capability")
		}
	}
	service, _, _ := newTestService(t, nil)
	call := emptyContext()
	if _, err := service.StopHooks(nil, call, rejectYield); err == nil {
		t.Fatal("nil cancellation input admitted")
	}
	if _, err := service.StopHooks(context.Background(), call, nil); err == nil {
		t.Fatal("nil controlled consumer admitted")
	}
	for _, alter := range []func(*Context){func(c *Context) { c.ComputerUseInactive = false }, func(c *Context) { c.GoalInactive = false }, func(c *Context) { c.TeammateInactive = false }} {
		copy := call
		alter(&copy)
		_, err := service.StopHooks(context.Background(), copy, rejectYield)
		requireCapability(t, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.StopHooks(ctx, call, rejectYield); err != nil {
		t.Fatal("empty actual resolver fabricated hook cancellation output")
	}
}

func TestConcurrentServiceLifecycle(t *testing.T) {
	service, _, _ := newTestService(t, nil)
	var wait sync.WaitGroup
	for index := 0; index < 12; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			for step := 0; step < 15; step++ {
				if err := service.SetOriginalEnvironment(map[string]string{"CLAUDE_CODE_SIMPLE": "1"}); err != nil {
					t.Error(err)
				}
				if err := service.Register("Stop", []Matcher{{Hooks: []string{}}}); err != nil {
					t.Error(err)
				}
				if err := service.SetSession("hook-main", "Stop", []Matcher{}); err != nil {
					t.Error(err)
				}
				if _, err := service.StopHooks(context.Background(), emptyContext(), rejectYield); err != nil {
					t.Error(err)
				}
				if _, err := service.Snapshot(); err != nil {
					t.Error(err)
				}
				service.ResetSnapshot()
				service.CacheSafeParams()
				service.ClearSession("hook-main")
				service.ClearRegistry()
			}
		}(index)
	}
	wait.Wait()
}

func TestDormantAbortCleanupDoesNotInheritCallerCancellation(t *testing.T) {
	service, _, _ := newTestService(t, nil)
	call := emptyContext()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := service.AbortCleanup(ctx, call); err != nil {
		t.Fatal(err)
	}
	requireCapability(t, service.AbortCleanup(nil, call))
	call.ComputerUseInactive = false
	requireCapability(t, service.AbortCleanup(ctx, call))
	call.AgentID = "subagent"
	if err := service.AbortCleanup(ctx, call); err != nil {
		t.Fatal("subagent must not inspect or release main CU resource")
	}
}

func TestMutableSnapshotMustNotTurnConfiguredMatcherIntoNoop(t *testing.T) {
	service, _, _ := newTestService(t, nil)
	snapshot, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Set("Stop", json.RawMessage(`[{"hooks":[]}]`))
	_, err = service.StopHooks(context.Background(), emptyContext(), rejectYield)
	requireCapability(t, err)
	snapshot.Set("StopFailure", json.RawMessage(`{}`))
	if _, err = service.StartStopFailure(emptyContext(), &streamhistory.Envelope{}); err == nil {
		t.Fatal("malformed mutable snapshot must surface")
	}
	service.resolver.snapshot = nil
	if service.active("Stop", "hook-main") {
		t.Fatal("uncaptured empty snapshot fabricated active matcher")
	}
	if err = service.SetSession("hook-main", "Stop", []Matcher{{Hooks: []string{"function"}}}); err != nil {
		t.Fatal(err)
	}
	service.resolver.disabled = true
	if service.active("Stop", "hook-main") {
		t.Fatal("managed-only must suppress session hooks")
	}
}

func TestSnapshotResetKeepsProgrammaticAndSessionStores(t *testing.T) {
	service, _, _ := newTestService(t, nil)
	if err := service.Register("Stop", []Matcher{{Hooks: []string{"callback"}}}); err != nil {
		t.Fatal(err)
	}
	if err := service.SetSession("hook-main", "Stop", []Matcher{}); err != nil {
		t.Fatal(err)
	}
	service.RegisterProgrammatic()
	service.ResetSnapshot()
	if service.registry.registered != nil || len(service.registry.order) != 0 {
		t.Fatal("snapshot reset did not clear SDK registry")
	}
	if service.registry.sessions["hook-main"] == nil || service.registry.programmatic != 1 {
		t.Fatal("SDK reset cleared independent stores")
	}
	_, err := service.StartPostSampling(emptyContext())
	requireCapability(t, err)
}
