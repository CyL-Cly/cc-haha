package queryhooks

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
)

type fixtureOperation struct {
	Kind, Event, Source, Raw, SID, Mode string
	Value                               bool
}
type fixtureInput struct {
	Home, Name, QuerySource, AgentID, AssistantSource string
	Simple                                            *string
	Interactive, Trust                                bool
	Files                                             map[string]string
	Operations                                        []fixtureOperation
	HistorySources                                    []string
	Supported                                         *bool
}
type fixtureRow struct {
	Operation   string   `json:"operation"`
	Value       any      `json:"value"`
	Phases      []string `json:"phases"`
	Allocations []string `json:"allocations"`
	UUIDCalls   int      `json:"uuidCalls"`
	Registry    any      `json:"registry"`
	Session     any      `json:"session"`
	Capability  bool     `json:"capability,omitempty"`
}
type fixtureResult struct {
	Name    string       `json:"name"`
	Results []fixtureRow `json:"results"`
}

func fixtureMatchers(mode string, session bool) []Matcher {
	if mode == "array" {
		return []Matcher{}
	}
	hooks := []string{}
	if mode == "active" {
		if session {
			hooks = append(hooks, "function")
		} else {
			hooks = append(hooks, "callback")
		}
	}
	return []Matcher{{Matcher: "", Hooks: hooks}}
}
func registryView(service *Service) any {
	if service.registry.registered == nil {
		return nil
	}
	out := protocol.NewJSONObject()
	for _, event := range service.registry.order {
		values := []any{}
		for _, matcher := range service.registry.registered[event] {
			values = append(values, map[string]any{"matcher": matcher.Matcher, "hooks": matcher.Hooks})
		}
		raw, _ := json.Marshal(values)
		out.Set(event, raw)
	}
	return out
}
func sessionView(service *Service) any {
	out := []any{}
	for _, sid := range service.registry.sessionOrder {
		store := service.registry.sessions[sid]
		object := protocol.NewJSONObject()
		for _, event := range store.order {
			values := []any{}
			for _, matcher := range store.events[event] {
				values = append(values, map[string]any{"matcher": matcher.Matcher, "hooks": matcher.Hooks})
			}
			raw, _ := json.Marshal(values)
			object.Set(event, raw)
		}
		out = append(out, []any{sid, object})
	}
	return out
}
func observeFixture(input fixtureInput) (fixtureResult, error) {
	project := filepath.Join(input.Home, "project")
	paths := map[string]string{"userSettings": filepath.Join(input.Home, ".claude/settings.json"), "projectSettings": filepath.Join(project, ".claude/settings.json"), "localSettings": filepath.Join(project, ".claude/settings.local.json")}
	if err := os.MkdirAll(filepath.Join(project, ".claude"), 0700); err != nil {
		return fixtureResult{}, err
	}
	if err := os.MkdirAll(filepath.Join(input.Home, ".claude"), 0700); err != nil {
		return fixtureResult{}, err
	}
	for source, raw := range input.Files {
		path, found := paths[source]
		if !found {
			return fixtureResult{}, unsupported("fixture source")
		}
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			return fixtureResult{}, err
		}
	}
	settingsState, err := clisettings.NewState(func() float64 { return 1700000000000 })
	if err != nil {
		return fixtureResult{}, err
	}
	settings, err := clisettings.NewService(clisettings.Options{State: settingsState, ConfigHome: filepath.Join(input.Home, ".claude"), OriginalCWD: project, Profile: clisettings.ExternalReleaseProfile, Now: func() float64 { return 1700000000000 }, PID: 12345})
	if err != nil {
		return fixtureResult{}, err
	}
	count := 0
	allocations := []string{}
	simple := "1"
	if input.Simple != nil {
		simple = *input.Simple
	}
	service, err := New(Options{Profile: EmptyExternalProfile, Settings: settings, SettingsState: settingsState, OriginalEnv: map[string]string{"CLAUDE_CODE_SIMPLE": simple}, MainSessionID: "hook-main", PolicyAbsent: true, FlagAbsent: true, PluginBaseAbsent: true, UUID: func() string {
		count++
		allocations = append(allocations, "uuid")
		return fmt.Sprintf("hook-id-%d", count)
	}})
	if err != nil {
		return fixtureResult{}, err
	}
	history := []queryloop.Message{}
	for _, source := range input.HistorySources {
		var object protocol.JSONObject
		if err := json.Unmarshal([]byte(source), &object); err != nil {
			return fixtureResult{}, err
		}
		history = append(history, queryloop.Message{Record: object})
	}
	var record protocol.JSONObject
	if err := json.Unmarshal([]byte(input.AssistantSource), &record); err != nil {
		return fixtureResult{}, err
	}
	var message protocol.JSONObject
	if err := json.Unmarshal(record.Get("message"), &message); err != nil {
		return fixtureResult{}, err
	}
	assistant := &streamhistory.Envelope{Record: record, Message: message, UUID: record.String("uuid")}
	var content []protocol.JSONObject
	if err := json.Unmarshal(message.Get("content"), &content); err != nil {
		return fixtureResult{}, err
	}
	for _, block := range content {
		assistant.Content = append(assistant.Content, &streamhistory.Block{Object: block})
	}
	system := []string{"original system"}
	user, systemContext := map[string]string{}, map[string]string{}
	hostObject := &struct{}{}
	querySource := input.QuerySource
	if querySource == "" {
		querySource = "sdk"
	}
	call := Context{History: history, Assistants: []*streamhistory.Envelope{assistant}, SystemPrompt: &system, UserContext: user, SystemContext: systemContext, ToolUseContext: hostObject, QuerySource: querySource, AgentID: input.AgentID, Interactive: input.Interactive, TrustAccepted: input.Trust, ComputerUseInactive: true, GoalInactive: true, TeammateInactive: true}
	identities := map[*protocol.JSONObject]int{}
	result := fixtureResult{Name: input.Name, Results: []fixtureRow{}}
	caller, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, op := range input.Operations {
		allocations = []string{}
		phases := []string{}
		var value any
		var err error
		switch op.Kind {
		case "snapshot":
			var snapshot *protocol.JSONObject
			snapshot, err = service.Snapshot()
			if err == nil {
				if _, found := identities[snapshot]; !found {
					identities[snapshot] = len(identities) + 1
				}
				raw, e := snapshot.MarshalJSON()
				err = e
				value = map[string]any{"raw": string(raw), "identity": identities[snapshot]}
			}
		case "reset":
			service.ResetSnapshot()
		case "update":
			err = service.UpdateSnapshot()
		case "write":
			err = os.WriteFile(paths[op.Source], []byte(op.Raw), 0600)
		case "register":
			err = service.Register(op.Event, fixtureMatchers(op.Mode, false))
		case "session":
			err = service.SetSession(op.SID, op.Event, fixtureMatchers(op.Mode, true))
		case "clearRegistry":
			service.ClearRegistry()
		case "clearSession":
			service.ClearSession(op.SID)
		case "programmatic":
			service.RegisterProgrammatic()
		case "post", "failure":
			phases = append(phases, "start")
			var task *Task
			if op.Kind == "post" {
				task, err = service.StartPostSampling(call)
			} else {
				task, err = service.StartStopFailure(call, assistant)
			}
			if err == nil {
				phases = append(phases, "returned-promise")
				<-task.Done()
				phases = append(phases, "completed")
			}
		case "cleanup":
			phases = append(phases, "start")
			if op.Value {
				cancel()
			}
			err = service.AbortCleanup(caller, call)
			if call.AgentID == "" {
				phases = append(phases, "returned-promise")
			}
			if err == nil {
				value = map[string]any{"inactive": call.ComputerUseInactive, "canceled": caller.Err() != nil}
				phases = append(phases, "completed")
			}
		case "normal":
			phases = append(phases, "start")
			var decision queryloop.StopDecision
			decision, err = service.StopHooks(context.Background(), call, func(queryloop.Yield) error { return errorsUnexpectedYield() })
			if err == nil {
				value = map[string]any{"yielded": []string{}, "result": map[string]any{"blockingErrors": decision.BlockingMessages, "preventContinuation": decision.PreventContinuation}}
				phases = append(phases, "completed")
			}
		case "cache":
			if saved := service.CacheSafeParams(); saved != nil {
				var sources []json.RawMessage
				sources, err = queryloop.Snapshot(saved.Messages)
				stringsOut := []string{}
				for _, raw := range sources {
					stringsOut = append(stringsOut, string(raw))
				}
				value = map[string]any{"messages": stringsOut, "systemIdentity": saved.Context.SystemPrompt == &system, "userIdentity": fmt.Sprintf("%p", saved.Context.UserContext) == fmt.Sprintf("%p", user), "contextIdentity": saved.Context.ToolUseContext == hostObject, "assistantIdentity": saved.Messages[len(saved.Messages)-1].Assistant == assistant}
			}
		default:
			err = unsupported("fixture operation")
		}
		row := fixtureRow{Operation: op.Kind, Value: value, Phases: phases, Allocations: allocations, UUIDCalls: count, Registry: registryView(service), Session: sessionView(service)}
		if err != nil {
			if _, ok := err.(*CapabilityError); ok {
				row.Capability = true
				result.Results = append(result.Results, row)
				return result, nil
			}
			return fixtureResult{}, err
		}
		result.Results = append(result.Results, row)
	}
	return result, nil
}
func errorsUnexpectedYield() error {
	return fmt.Errorf("empty hook resolver unexpectedly emitted output")
}
func TestQueryTerminalHooksFixture(t *testing.T) {
	if os.Getenv("CC_HAHA_QUERY_TERMINAL_HOOKS") != "1" {
		t.Skip("subprocess fixture only")
	}
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	var input fixtureInput
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		t.Fatal(err)
	}
	home, err := filepath.EvalSymlinks(input.Home)
	if err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	actual, _ := filepath.EvalSymlinks(cwd)
	envHome, _ := filepath.EvalSymlinks(os.Getenv("HOME"))
	config, _ := filepath.EvalSymlinks(os.Getenv("CLAUDE_CONFIG_DIR"))
	temporary, _ := filepath.EvalSymlinks(os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT"))
	if home != actual || home != envHome || config != filepath.Join(home, ".claude") || !strings.HasPrefix(home, temporary+string(filepath.Separator)) {
		t.Fatal("query hooks require disposable HOME/config")
	}
	result, err := observeFixture(input)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err = protocol.ParseAndStringifyJavaScriptJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("QUERY_HOOKS_RESULT:" + string(encoded))
}
