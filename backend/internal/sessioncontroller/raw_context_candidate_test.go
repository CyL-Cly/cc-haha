package sessioncontroller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type rawCandidateInput struct {
	MemoryAfterActivation                            bool
	Home, Name, Timezone, CustomSystemPrompt, Memory string
	History                                          []storage.Object
	Steps                                            []struct {
		Kind  string
		Value json.RawMessage
	}
}
type rawCandidateFiles struct {
	clisettings.OSFileSystem
	home string
}

func (f rawCandidateFiles) Stat(path string) (os.FileInfo, error) {
	if !strings.HasPrefix(path, f.home+string(os.PathSeparator)) {
		return nil, os.ErrNotExist
	}
	return f.OSFileSystem.Stat(path)
}
func (f rawCandidateFiles) ReadFile(path string) ([]byte, error) {
	if !strings.HasPrefix(path, f.home+string(os.PathSeparator)) {
		return nil, os.ErrPermission
	}
	return f.OSFileSystem.ReadFile(path)
}

type rawCandidateMemory struct {
	usercontext.OSMemoryFiles
	settled, release chan struct{}
}

func (f *rawCandidateMemory) ReadFile(path string) ([]byte, error) {
	value, err := f.OSMemoryFiles.ReadFile(path)
	close(f.settled)
	<-f.release
	return value, err
}
func rawCandidateRun(t *testing.T, input rawCandidateInput) any {
	t.Helper()
	home := input.Home
	config := filepath.Join(home, ".claude")
	project := filepath.Join(home, "project")
	path := filepath.Join(config, "settings.json")
	if err := os.MkdirAll(filepath.Join(project, ".claude"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	initial := `{}`
	if input.Memory != "" {
		if !input.MemoryAfterActivation {
			initial = `{"pluginConfigs":{"agents-md@builtin":{"options":{"instructionFiles":"claude-md"}}}}`
		}
		if err := os.WriteFile(filepath.Join(config, "CLAUDE.md"), []byte("Private original probe instructions.\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, []byte(initial), 0600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	clock, _ := time.Parse(time.RFC3339, "2026-10-01T23:59:59Z")
	now := func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }
	setClock := func(value string) {
		v, err := time.Parse(time.RFC3339, value)
		if err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		clock = v
		mu.Unlock()
	}
	state, err := clisettings.NewState(func() float64 { return float64(now().UnixMilli()) })
	if err != nil {
		t.Fatal(err)
	}
	settings, err := clisettings.NewService(clisettings.Options{State: state, ConfigHome: config, OriginalCWD: project, Profile: clisettings.ExternalReleaseProfile, Now: func() float64 { return float64(now().UnixMilli()) }, PID: 12345, FS: rawCandidateFiles{home: home}})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := clistartupenv.NewEnvironment(protocol.NewJSONObject(protocol.JSONField{Name: "CLAUDE_CODE_SIMPLE", Value: quote("1")}, protocol.JSONField{Name: "CLAUDE_CODE_DISABLE_CLAUDE_MDS", Value: quote("1")}))
	if err != nil {
		t.Fatal(err)
	}
	ucOptions := usercontext.Options{Environment: environment, Settings: settings, SettingsState: state, Now: now, Profile: usercontext.ExternalProfile}
	var barrier *rawCandidateMemory
	if input.Memory != "" {
		if err = environment.SetProperty(quote("CLAUDE_CODE_DISABLE_CLAUDE_MDS"), quote("0")); err != nil {
			t.Fatal(err)
		}
		barrier = &rawCandidateMemory{settled: make(chan struct{}), release: make(chan struct{})}
		ucOptions.AdditionalDirectories = []string{project}
		ucOptions.Memory = &usercontext.ExplicitUserMemory{ConfigHome: config, Profile: usercontext.ExplicitUserMemoryProfile, Files: barrier}
	}
	service, err := usercontext.New(ucOptions)
	if err != nil {
		t.Fatal(err)
	}
	allocations := []any{}
	serial := 0
	uuid := func() string {
		serial++
		value := fmt.Sprintf("00000000-0000-4000-8000-%012d", serial)
		allocations = append(allocations, map[string]any{"kind": "uuid", "value": value, "millis": now().UnixMilli()})
		return value
	}
	factoryClock := func() time.Time {
		value := now()
		allocations = append(allocations, map[string]any{"kind": "timestamp", "millis": value.UnixMilli(), "value": value.Format("2006-01-02T15:04:05.000Z")})
		return value
	}
	factory, err := messages.NewFactory(uuid, factoryClock)
	if err != nil {
		t.Fatal(err)
	}
	options := hostOptions(t, home).options
	options.Factory = factory
	options.UUID = uuid
	options.Now = now
	options.UserContext = service
	options.CustomSystemPrompt = quote(input.CustomSystemPrompt)
	controller, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	if err = controller.Activate(activation()); err != nil {
		t.Fatal(err)
	}
	defer controller.AwaitBackground()
	for _, args := range input.History {
		message := orderedUser(factory.CreateUser(args))
		controller.messages = append(controller.messages, &message)
	}
	allocations = []any{}
	serial = 0
	originalHistory := marshal(t, controller.History())
	results := []any{}
	var current RawContext
	for _, step := range input.Steps {
		start := len(allocations)
		var result any
		switch step.Kind {
		case "clock":
			setClock(text(step.Value))
		case "override":
			if err = environment.SetProperty(quote("CLAUDE_CODE_OVERRIDE_DATE"), step.Value); err != nil {
				t.Fatal(err)
			}
		case "clearUser":
			service.ClearUserContext()
		case "writeMode":
			if err = os.WriteFile(path, []byte(`{"pluginConfigs":{"agents-md@builtin":{"options":{"instructionFiles":`+string(step.Value)+`}}}}`), 0600); err != nil {
				t.Fatal(err)
			}
		case "resetSettings":
			state.ResetSettingsCache()
		case "emptyUser":
			current.UserContext = protocol.NewJSONObject()
		case "mutateUser":
			current.UserContext.Set("currentDate", step.Value)
		case "raw":
			var pending any
			if barrier != nil {
				done := make(chan struct{})
				var next RawContext
				var nextErr error
				go func() { next, nextErr = controller.ResolveRawContext(requestcontext.Context{}); close(done) }()
				<-barrier.settled
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				isPending := true
				select {
				case <-done:
					isPending = false
				default:
				}
				pending = map[string]any{"callerAborted": ctx.Err() != nil, "rawPendingBeforeRelease": isPending, "prefixAllocationCount": len(allocations) - start}
				setClock("2026-10-02T00:00:01Z")
				close(barrier.release)
				<-done
				current, err = next, nextErr
				barrier = nil
			} else {
				current, err = controller.ResolveRawContext(requestcontext.Context{})
			}
			if err != nil {
				t.Fatal(err)
			}
			result = map[string]any{"userContextJSON": marshal(t, current.UserContext), "historyJSON": marshal(t, current.Context.History), "systemPromptJSON": marshal(t, current.Context.SystemPrompt), "pending": pending}
		case "project":
			projected, projectErr := controller.ProjectUserContext(current)
			if projectErr != nil {
				t.Fatal(projectErr)
			}
			result = map[string]any{"historyJSON": marshal(t, projected.History), "rawHistoryJSON": marshal(t, current.Context.History), "userContextJSON": marshal(t, current.UserContext), "transcriptJSON": marshal(t, controller.History())}
		default:
			t.Fatal("undeclared original operation")
		}
		stepValue := map[string]any{"kind": step.Kind}
		if step.Value != nil {
			stepValue["value"] = step.Value
		}
		results = append(results, map[string]any{"step": stepValue, "result": result, "allocations": append([]any{}, allocations[start:]...)})
	}
	if marshal(t, controller.History()) != originalHistory {
		t.Fatal("prefix polluted actual controller history")
	}
	return map[string]any{"name": input.Name, "results": results}
}
func TestRawContextCandidateFixture(t *testing.T) {
	if os.Getenv("CC_HAHA_RAW_CONTEXT_FIXTURE") != "1" {
		t.Skip("candidate fixture private entry")
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		t.Fatal(err)
	}
	var input rawCandidateInput
	if err = json.Unmarshal(data, &input); err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(input.Home) || input.Home != os.Getenv("HOME") || filepath.Join(input.Home, ".claude") != os.Getenv("CLAUDE_CONFIG_DIR") {
		t.Fatal("private home required")
	}
	encoded, err := json.Marshal(rawCandidateRun(t, input))
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(string(encoded))
}

func TestRawContextCandidateDoesNotAllocatePrefixUntilProjection(t *testing.T) {
	controller := activeController(t)
	uuidCalls, dateCalls := 0, 0
	factory, err := messages.NewFactory(func() string { uuidCalls++; return "original-prefix-id" }, func() time.Time { dateCalls++; return time.UnixMilli(1700000000000) })
	if err != nil {
		t.Fatal(err)
	}
	controller.options.Factory = factory
	rawInput, err := controller.ResolveRawContext(requestcontext.Context{})
	if err != nil {
		t.Fatal(err)
	}
	if uuidCalls != 0 || dateCalls != 0 || len(rawInput.Context.History) != 0 {
		t.Fatal("raw phase allocated prefix", uuidCalls, dateCalls, rawInput.Context.History)
	}
	rawInput.UserContext.Set("currentDate", quote("Local request context."))
	next, err := controller.ResolveRawContext(requestcontext.Context{})
	if err != nil {
		t.Fatal(err)
	}
	if string(next.UserContext.Get("currentDate")) == string(rawInput.UserContext.Get("currentDate")) {
		t.Fatal("raw snapshot mutated shared memo")
	}
	projected, err := controller.ProjectUserContext(rawInput)
	if err != nil {
		t.Fatal(err)
	}
	if uuidCalls != 1 || dateCalls != 1 || len(projected.History) != 1 || len(controller.History()) != 0 || len(rawInput.Context.History) != 0 {
		t.Fatal("late local allocation/history", uuidCalls, dateCalls)
	}
	rawInput.UserContext = protocol.NewJSONObject()
	if _, err = controller.ProjectUserContext(rawInput); err != nil || uuidCalls != 1 || dateCalls != 1 {
		t.Fatal("empty context allocated", err)
	}
}
func TestRawContextCandidateNativeHistoryProjectionBeforePrefix(t *testing.T) {
	controller := activeController(t)
	rawInput, err := controller.ResolveRawContext(requestcontext.Context{})
	if err != nil {
		t.Fatal(err)
	}
	original := orderedUser(controller.options.Factory.CreateUser(storage.Object{"content": quote("Original local history."), "uuid": quote("original-user"), "timestamp": quote("2025-01-01T00:00:00Z")}))
	rawInput.Context.History = []protocol.JSONObject{original}
	calls := 0
	factory, _ := messages.NewFactory(func() string { calls++; return "late-prefix" }, controller.options.Now)
	controller.options.Factory = factory
	if _, err = controller.ProjectUserContextWithHistory(rawInput, nil); err == nil || calls != 0 {
		t.Fatal("missing native policy silently became identity", err)
	}
	projected, err := controller.ProjectUserContextWithHistory(rawInput, func(snapshot []protocol.JSONObject) ([]protocol.JSONObject, error) {
		if calls != 0 || len(snapshot) != 1 {
			t.Fatal("prefix before policy")
		}
		snapshot[0].Set("future", raw(true))
		return []protocol.JSONObject{}, nil
	})
	if err != nil || calls != 1 || len(projected.History) != 1 || len(rawInput.Context.History) != 1 || rawInput.Context.History[0].Get("future") != nil {
		t.Fatal("native projection/prefix snapshot", err)
	}
	sentinel := fmt.Errorf("actual native preflight failure")
	if _, err = controller.ProjectUserContextWithHistory(rawInput, func([]protocol.JSONObject) ([]protocol.JSONObject, error) { return nil, sentinel }); err != sentinel || calls != 1 {
		t.Fatal("projection error consumed prefix identity", err)
	}
	if _, err = controller.ProjectUserContext(RawContext{Context: rawInput.Context, UserContext: rawInput.UserContext}); err == nil || calls != 1 {
		t.Fatal("foreign raw context admitted", err)
	}
	other := activeController(t)
	if _, err = other.ProjectUserContext(rawInput); err == nil {
		t.Fatal("other controller context admitted")
	}
	rawInput.Context.Tools = []protocol.JSONObject{protocol.NewJSONObject()}
	if _, err = controller.ProjectUserContextWithHistory(rawInput, func(h []protocol.JSONObject) ([]protocol.JSONObject, error) {
		t.Fatal("tools guard happened after callback")
		return h, nil
	}); err == nil {
		t.Fatal("unported tools admitted")
	}
	controller.active = false
	if _, err = controller.ResolveRawContext(requestcontext.Context{}); err == nil {
		t.Fatal("inactive raw resolver")
	}
	if _, err = controller.ProjectUserContext(rawInput); err == nil {
		t.Fatal("inactive projection")
	}
}

func TestRawContextCandidateRejectsBeforeProjectionEffects(t *testing.T) {
	controller := activeController(t)
	resolved, err := controller.ResolveRawContext(requestcontext.Context{})
	if err != nil {
		t.Fatal(err)
	}
	allocations := 0
	factory, _ := messages.NewFactory(func() string { allocations++; return "guard-id" }, controller.options.Now)
	controller.options.Factory = factory
	sentinel := errors.New("callback should not run")
	callback := func([]protocol.JSONObject) ([]protocol.JSONObject, error) { return nil, sentinel }
	if _, err = controller.ProjectUserContextWithHistory(RawContext{}, callback); err == nil || err == sentinel {
		t.Fatal("foreign history reached callback", err)
	}
	resolved.Context.Tools = []protocol.JSONObject{protocol.NewJSONObject()}
	if _, err = controller.ProjectUserContext(resolved); err == nil {
		t.Fatal("tools passed late projection")
	}
	resolved.Context.Tools = nil
	resolved.UserContext.Set("bad", json.RawMessage(`{"toString":null}`))
	if _, err = controller.ProjectUserContext(resolved); err == nil || allocations != 0 {
		t.Fatal("coercion failure allocated prefix", err, allocations)
	}
	controller.active = false
	if _, err = controller.ProjectUserContextWithHistory(resolved, callback); err == nil || err == sentinel || allocations != 0 {
		t.Fatal("inactive host reached callback", err, allocations)
	}
}

func TestRawContextCandidateLegacyCancellationIsFiniteWaiterOnly(t *testing.T) {
	controller := activeController(t)
	home, canonicalErr := filepath.EvalSymlinks(t.TempDir())
	if canonicalErr != nil {
		t.Fatal(canonicalErr)
	}
	config := filepath.Join(home, ".claude")
	project := filepath.Join(home, "project")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"pluginConfigs":{"agents-md@builtin":{"options":{"instructionFiles":"claude-md"}}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "CLAUDE.md"), []byte("Private real memory.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	state, _ := clisettings.NewState(func() float64 { return 1 })
	settings, err := clisettings.NewService(clisettings.Options{State: state, ConfigHome: config, OriginalCWD: project, Profile: clisettings.ExternalReleaseProfile, Now: func() float64 { return 1 }, PID: 12345, FS: rawCandidateFiles{home: home}})
	if err != nil {
		t.Fatal(err)
	}
	env, err := clistartupenv.NewEnvironment(protocol.NewJSONObject(protocol.JSONField{Name: "CLAUDE_CODE_SIMPLE", Value: quote("1")}, protocol.JSONField{Name: "CLAUDE_CODE_DISABLE_CLAUDE_MDS", Value: quote("0")}))
	if err != nil {
		t.Fatal(err)
	}
	barrier := &rawCandidateMemory{settled: make(chan struct{}), release: make(chan struct{})}
	service, err := usercontext.New(usercontext.Options{Environment: env, Settings: settings, SettingsState: state, Now: controller.options.Now, Profile: usercontext.ExternalProfile, AdditionalDirectories: []string{project}, Memory: &usercontext.ExplicitUserMemory{ConfigHome: config, Profile: usercontext.ExplicitUserMemoryProfile, Files: barrier}})
	if err != nil {
		t.Fatal(err)
	}
	controller.options.UserContext = service
	controller.legacyEnvironment = nil
	allocations := 0
	factory, _ := messages.NewFactory(func() string { allocations++; return "late-id" }, controller.options.Now)
	controller.options.Factory = factory
	future := service.GetUserContext()
	<-barrier.settled
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = controller.ResolveContext(ctx, requestcontext.Context{}); !errors.Is(err, context.Canceled) || allocations != 0 || !future.Pending() {
		t.Fatal("legacy waiter cancellation changed", err, allocations)
	}
	close(barrier.release)
	if _, err = future.Await(context.Background()); err != nil {
		t.Fatal(err)
	}
	rawInput, err := controller.ResolveRawContext(requestcontext.Context{})
	if err != nil || allocations != 0 {
		t.Fatal("raw resolver consumed envelope", err, allocations)
	}
	if _, err = controller.ProjectUserContext(rawInput); err != nil || allocations != 1 {
		t.Fatal("late prefix failed after canceled finite waiter", err, allocations)
	}
}
