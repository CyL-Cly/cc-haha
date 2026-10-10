package queryloop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

type fixtureInput struct {
	Home, Origin, Name, Backfill, Permission, ResultContentSource string
	NonInteractive                                                *bool
	Wires                                                         []string
	HistorySources, SystemSources                                 []string
	Definition                                                    protocol.JSONObject
	MaxOutputTokens                                               float64
	IdentityTrace                                                 bool
	DeprecatedCatalog                                             []struct {
		Name    string
		Aliases []string
	}
	Runtime             provider.SourceRuntime
	DeviceID, SessionID string
}
type fixtureRequest struct {
	Path    string      `json:"path"`
	Method  string      `json:"method"`
	Headers [][2]string `json:"headers"`
	Body    string      `json:"body"`
}
type effect struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}
type fixtureObservation struct {
	Profile      map[string]any    `json:"profile"`
	ModelHistory [][]string        `json:"modelHistory"`
	History      []string          `json:"history"`
	Requests     []fixtureRequest  `json:"requests"`
	Yielded      []string          `json:"yielded"`
	YieldedAfter []string          `json:"yieldedAfter"`
	Effects      []effect          `json:"effects"`
	Allocations  []effect          `json:"allocations"`
	Terminal     map[string]string `json:"terminal"`
	Error        any               `json:"error"`
	Executions   int               `json:"executions"`
	File         *string           `json:"file"`
}

func fixtureBytes(object protocol.JSONObject) string {
	raw, err := object.MarshalJSON()
	if err != nil {
		panic(err)
	}
	raw, err = protocol.ParseAndStringifyJavaScriptJSON(raw)
	if err != nil {
		panic(err)
	}
	return string(raw)
}
func fixtureObject(raw string) protocol.JSONObject {
	var object protocol.JSONObject
	if err := json.Unmarshal([]byte(raw), &object); err != nil {
		panic(err)
	}
	return object
}
func fixtureGuard(input fixtureInput) error {
	if os.Getenv("CC_HAHA_QUERY_LOOP_FIXTURE") != "1" || os.Getenv("NODE_ENV") != "production" || os.Getenv("BUN_OPTIONS") != "--no-env-file" {
		return errors.New("explicit query loop production fixture required")
	}
	if input.Home == "" || !filepath.IsAbs(input.Home) || input.Home != os.Getenv("HOME") || input.Home != os.Getenv("USERPROFILE") || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(input.Home, ".claude") {
		return errors.New("explicit disposable query loop HOME/config required")
	}
	home, err := filepath.EvalSymlinks(input.Home)
	if err != nil {
		return err
	}
	root := os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT")
	if root == "" || !filepath.IsAbs(root) {
		return errors.New("trusted query loop allocation root required")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil || !strings.HasPrefix(home, root+string(filepath.Separator)) {
		return errors.New("trusted query loop allocation root required")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	cwd, err = filepath.EvalSymlinks(cwd)
	if err != nil || cwd != home {
		return errors.New("query loop fixture cwd must be private HOME")
	}
	for _, path := range []string{input.Home, filepath.Join(input.Home, ".claude")} {
		stat, err := os.Lstat(path)
		if err != nil || !stat.IsDir() || stat.Mode()&os.ModeSymlink != 0 {
			return errors.New("query loop fixture directories must be private regular directories")
		}
	}
	origin, err := url.Parse(input.Origin)
	if err != nil || origin.Scheme != "http" || origin.Hostname() != "127.0.0.1" || origin.Port() == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" || input.Origin != os.Getenv("ANTHROPIC_BASE_URL") {
		return errors.New("query loop fixture requires exact HTTP loopback origin")
	}
	if input.Backfill != "add" && input.Backfill != "overwrite" && input.Backfill != "none" {
		return errors.New("query loop fixture requires registered tool profile")
	}
	if input.NonInteractive == nil || !json.Valid([]byte(input.ResultContentSource)) || len(input.Wires) != 2 || input.HistorySources == nil || input.SystemSources == nil || input.MaxOutputTokens != 32000 || input.DeviceID != "fixture-query-device" || input.SessionID != "00000000-0000-4000-8000-000000000000" || input.Runtime.Version == "" || input.Runtime.Arch == "" || input.Runtime.Platform == "" || (input.Permission != "allow" && input.Permission != "replace-path" && input.Permission != "replace-content") {
		return errors.New("query loop fixture requires original history/tool definition/runtime and two responses")
	}
	definition, err := toolschema.ParseDefinition(input.Definition)
	if err != nil {
		return err
	}
	if definition.Kind != "registered" || definition.Name != "FixtureWrite" && definition.Name != "�" || definition.InputSchema == nil || definition.InputSchema.Kind != "object" || definition.InputSchema.UnknownKeys != "strip" || len(definition.InputSchema.Fields) != 2 || string(input.Definition.Get("concurrencySafe")) != "false" || string(input.Definition.Get("readOnly")) != "false" {
		return errors.New("query loop fixture requires original registered string/object tool definition")
	}
	for index, field := range definition.InputSchema.Fields {
		if field.Name != []string{"file_path", "content"}[index] || field.Schema.Kind != "string" {
			return errors.New("query loop fixture requires original registered string/object tool definition")
		}
	}
	if _, err := protocol.DecodeJavaScriptJSONString(input.Definition.Get("description")); err != nil {
		return err
	}

	return nil
}

// The fixture supplies an original registered tool and explicit empty-policy
// capabilities; parsing/orchestration/result preparation run production Go code.
func fixtureRun(input fixtureInput) (fixtureObservation, error) {
	querySource := "sdk"
	if !*input.NonInteractive {
		querySource = "repl_main_thread"
	}
	observation := fixtureObservation{Profile: map[string]any{"nonInteractive": *input.NonInteractive, "querySource": querySource, "streamingToolExecution": false}, ModelHistory: [][]string{}, History: []string{}, Requests: []fixtureRequest{}, Yielded: []string{}, YieldedAfter: []string{}, Effects: []effect{}, Allocations: []effect{}}
	record := func(kind, value string) { observation.Effects = append(observation.Effects, effect{kind, value}) }
	id := 0
	uuid := func() string {
		id++
		value := fmt.Sprintf("fixture-id-%d", id)
		observation.Allocations = append(observation.Allocations, effect{"uuid", value})
		return value
	}
	now := func() time.Time {
		observation.Allocations = append(observation.Allocations, effect{"time", "2023-11-14T22:13:20.000Z"})
		return time.UnixMilli(1700000000000)
	}
	factory, _ := messages.NewFactory(uuid, now)
	backfill := func(object *protocol.JSONObject) error {
		record("backfill", fixtureBytes(*object))
		if input.Backfill == "add" {
			object.Set("legacy_view", quoted("shown"))
		} else {
			object.Set("file_path", quoted("effect-overwritten.txt"))
		}
		return nil
	}
	definition, err := toolschema.ParseDefinition(input.Definition)
	if err != nil {
		return observation, err
	}
	tool := RegisteredTool{Name: definition.Name, Aliases: definition.Aliases,
		ParseInput: func(value protocol.JSONObject) (protocol.JSONObject, error) {
			return toolschema.ParseInput(definition, value)
		},
		IsConcurrencySafe: func(protocol.JSONObject) bool { return false }, PersistenceThreshold: 1000,
		CallJSON: func(_ context.Context, value protocol.JSONObject, call JSONToolCallContext) (ToolCallResult, error) {
			if input.IdentityTrace {
				record("context-ID", string(call.ToolUseID))
			}
			record("tool", fixtureBytes(value))
			observation.Executions++
			wantPath := "effect.txt"
			if input.Permission == "replace-path" {
				wantPath = "effect-permission.txt"
			}
			if value.String("file_path") != wantPath || !input.IdentityTrace && observation.Executions != 1 {
				return ToolCallResult{}, errors.New("unexpected private write or duplicate execution")
			}
			content := []byte(value.String("content"))
			if input.IdentityTrace {
				ids := []json.RawMessage{}
				for _, effect := range observation.Effects {
					if effect.Kind == "context-ID" {
						ids = append(ids, json.RawMessage(effect.Value))
					}
				}
				content, _ = json.Marshal(ids)
			}
			if err := os.WriteFile(filepath.Join(input.Home, wantPath), content, 0600); err != nil {
				return ToolCallResult{}, err
			}
			return ToolCallResult{Data: quoted("written")}, nil
		},
		MapResultJSON: func(data json.RawMessage, id json.RawMessage) (protocol.JSONObject, error) {
			if input.IdentityTrace {
				record("mapped-ID", string(id))
			}
			return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("tool_result")}, protocol.JSONField{Name: "tool_use_id", Value: id}, protocol.JSONField{Name: "content", Value: json.RawMessage(input.ResultContentSource)}), nil
		},
	}
	if input.Backfill != "none" {
		tool.Backfill = backfill
	}
	inProgress := []json.RawMessage{}
	catalog := []ToolDescriptor{}
	for _, d := range input.DeprecatedCatalog {
		catalog = append(catalog, ToolDescriptor{Name: d.Name, Aliases: d.Aliases})
	}
	batch, err := NewSerialToolBatch([]RegisteredTool{tool}, ToolBatchCapabilities{DeprecatedCatalog: catalog, JSON: &JSONToolBatchCapabilities{
		PreTool: func(_ context.Context, _ RegisteredTool, value *protocol.JSONObject, _ JSONToolCallContext) (*protocol.JSONObject, error) {
			return value, nil
		},
		ResolvePermission: func(_ context.Context, _ RegisteredTool, value *protocol.JSONObject, _ JSONToolCallContext) (ToolPermission, error) {
			record("permission", fixtureBytes(*value))
			decision := ToolPermission{Behavior: "allow", Input: value}
			if input.Permission != "allow" {
				copy := value.Clone()
				if input.Permission == "replace-path" {
					copy.Set("file_path", quoted("effect-permission.txt"))
				} else {
					copy.Set("content", quoted("permission content"))
				}
				decision.UpdatedInput = &copy
			}
			return decision, nil
		},
		PostTool: func(context.Context, RegisteredTool, *protocol.JSONObject, ToolCallResult, JSONToolCallContext) ([]Message, error) {
			return nil, nil
		},
		InProgress: func(id json.RawMessage, active bool) error {
			if active {
				inProgress = append(inProgress, id)
			} else {
				inProgress = []json.RawMessage{}
			}
			raw, _ := json.Marshal(inProgress)
			record("in-progress", string(raw))
			return nil
		},
	}}, factory)
	if err != nil {
		return observation, err
	}
	env := map[string]string{"ANTHROPIC_BASE_URL": input.Origin, "ANTHROPIC_API_KEY": "offline-fixture-key", "CLAUDE_CODE_SIMPLE": "1", "CLAUDE_CODE_DISABLE_FAST_MODE": "1", "CLAUDE_CODE_MAX_RETRIES": "0"}
	schemaSession := toolschema.NewSession()
	schemaHost := &toolschema.Host{MainLoopModel: "fixture-model"}
	promptCalls := 0
	system := []json.RawMessage{}
	for _, raw := range input.SystemSources {
		system = append(system, json.RawMessage(raw))
	}
	host := Host{
		Preflight: func(_ context.Context, round Round) ([]Message, error) { return round.History, nil },
		ResolveModelInput: func(_ context.Context, round Round) (requestretry.Input, error) {
			history, err := Snapshot(round.History)
			if err != nil {
				return requestretry.Input{}, err
			}
			original := []protocol.JSONObject{}
			snapshot := []string{}
			for _, raw := range history {
				var object protocol.JSONObject
				if err := json.Unmarshal(raw, &object); err != nil {
					return requestretry.Input{}, err
				}
				original = append(original, object)
				snapshot = append(snapshot, string(raw))
			}
			observation.ModelHistory = append(observation.ModelHistory, snapshot)
			return requestretry.Input{Context: requestcontext.Context{History: original, SystemPrompt: system, Tools: []protocol.JSONObject{input.Definition}, Thinking: requestcontext.Thinking{Type: "disabled"}, Options: requestcontext.Options{Model: "fixture-model", QuerySource: querySource, IsNonInteractiveSession: *input.NonInteractive, MaxOutputTokensOverride: &input.MaxOutputTokens}, Host: requestcontext.Host{ToolSchemas: schemaSession, ToolSchemaHost: schemaHost, Profile: requestcontext.DirectRelayProfile, Env: env, DeviceID: input.DeviceID, SessionID: input.SessionID, SDKBetas: []string{}, TTLEligible: false, TTLAllowlist: []string{}}}, Runtime: input.Runtime}, nil
		},
		Request: requestretry.Dependencies{Random: func() float64 { return 0 }, Sleep: func(context.Context, float64) error { return nil },
			OnPreparation: func([]protocol.JSONObject) {
				for count := schemaSession.PromptCalls()[input.Definition.String("name")]; promptCalls < count; promptCalls++ {
					raw, _ := protocol.ParseAndStringifyJavaScriptJSON(input.Definition.Get("prompt"))
					record("prompt", string(raw))
				}
			},
			ObserveRequest: func(endpoint string, header http.Header, body json.RawMessage) {
				parsed, _ := url.Parse(endpoint)
				pairs := [][2]string{}
				for name, values := range header {
					pairs = append(pairs, [2]string{strings.ToLower(name), strings.Join(values, ", ")})
				}
				sort.Slice(pairs, func(i, j int) bool { return pairs[i][0] < pairs[j][0] })
				observation.Requests = append(observation.Requests, fixtureRequest{parsed.RequestURI(), "POST", pairs, string(body)})
			},
		},
		LookupBackfillJSON: batch.LookupBackfillJSON, ExecuteBatch: batch.Execute, NormalizeToolResult: NormalizeOrdinaryToolResult,
		PostSampling: func(Round, []*streamhistory.Envelope) error { return nil },
		StopFailure: func(Round, *streamhistory.Envelope) error {
			return errors.New("successful fixture received terminal failure")
		},
		AbortCleanup: func(context.Context, Round) error { return errors.New("successful fixture received caller abort") },
		StopHooks: func(context.Context, Round, []*streamhistory.Envelope, Emit) (StopDecision, error) {
			return StopDecision{}, nil
		},
		PauseAfterTools: func(context.Context, Round) (bool, error) { return false, nil },
	}

	observed := []Yield{}
	encode := func(yield Yield) (string, error) {
		if yield.RequestStart {
			return `{"type":"stream_request_start"}`, nil
		}
		if yield.Message != nil {
			raw, err := yield.Message.MarshalJSON()
			return string(raw), err
		}
		raw, err := yield.Stream.EventJSON()
		return string(raw), err
	}
	initial := []Message{}
	for _, raw := range input.HistorySources {
		initial = append(initial, Message{Record: fixtureObject(raw)})
	}
	outcome, err := Run(context.Background(), initial, Options{Profile: SuccessfulToolRound, Model: "fixture-model", QuerySource: querySource, NonInteractive: *input.NonInteractive, MaxTurns: 2, UUID: uuid, Now: now}, host, func(yield Yield) error {
		raw, err := encode(yield)
		if err != nil {
			return err
		}
		observation.Yielded = append(observation.Yielded, raw)
		observed = append(observed, yield)
		return nil
	})
	if err != nil {
		return observation, err
	}
	observation.Terminal = map[string]string{"reason": outcome.Reason}
	for _, yield := range observed {
		raw, err := encode(yield)
		if err != nil {
			return observation, err
		}
		observation.YieldedAfter = append(observation.YieldedAfter, raw)
	}
	rawHistory, err := Snapshot(outcome.History)
	if err != nil {
		return observation, err
	}
	for _, raw := range rawHistory {
		observation.History = append(observation.History, string(raw))
	}
	file := "effect.txt"
	if input.Permission == "replace-path" {
		file = "effect-permission.txt"
	}
	raw, err := os.ReadFile(filepath.Join(input.Home, file))
	if errors.Is(err, os.ErrNotExist) {
		return observation, nil
	}
	if err != nil {
		return observation, err
	}
	text := string(raw)
	observation.File = &text

	return observation, nil
}
func TestQueryLoopFixture(t *testing.T) {
	if os.Getenv("CC_HAHA_QUERY_LOOP_FIXTURE") != "1" {
		t.Skip("private differential fixture")
	}
	var input fixtureInput
	decoder := json.NewDecoder(os.Stdin)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		t.Fatal(err)
	}
	if err := fixtureGuard(input); err != nil {
		t.Fatal(err)
	}
	observation, err := fixtureRun(input)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(observation)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(map[string]string{"observationSource": string(raw)})
	fmt.Println("QUERY_LOOP_RESULT:" + string(encoded))
}
