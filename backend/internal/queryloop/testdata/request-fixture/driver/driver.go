// This is an original-input integration fixture, not a production host policy.
// It composes the migrated request builder, client, raw stream and query loop.
package driver

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
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
)

type Input struct {
	Name            string                 `json:"name"`
	NonInteractive  *bool                  `json:"nonInteractive"`
	HistorySources  []string               `json:"historySources"`
	SystemSources   []string               `json:"systemSources"`
	MaxOutputTokens float64                `json:"maxOutputTokens"`
	Env             map[string]string      `json:"env"`
	Wire            string                 `json:"wire"`
	Home            string                 `json:"home"`
	Origin          string                 `json:"origin"`
	Runtime         provider.SourceRuntime `json:"runtime"`
	DeviceID        string                 `json:"deviceID"`
	SessionID       string                 `json:"sessionID"`
}
type Allocation struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}
type Request struct {
	Path    string      `json:"path"`
	Method  string      `json:"method"`
	Headers [][2]string `json:"headers"`
	Body    string      `json:"body"`
}
type Observation struct {
	ModelHistory [][]string        `json:"modelHistory"`
	Yielded      []string          `json:"yielded"`
	YieldedAfter []string          `json:"yieldedAfter"`
	History      []string          `json:"history"`
	Allocations  []Allocation      `json:"allocations"`
	Requests     []Request         `json:"requests"`
	Terminal     map[string]string `json:"terminal"`
	Error        any               `json:"error"`
}

var requestEnv = map[string]bool{
	"CLAUDE_CODE_EXTRA_BODY": true, "CLAUDE_CODE_EXTRA_METADATA": true, "CLAUDE_CODE_MAX_OUTPUT_TOKENS": true, "CLAUDE_CODE_PROVIDER_MAX_OUTPUT_TOKENS": true,
	"CLAUDE_CODE_DISABLE_THINKING": true, "CLAUDE_CODE_DISABLE_ADAPTIVE_THINKING": true, "CC_HAHA_SEND_DISABLED_THINKING": true, "DISABLE_PROMPT_CACHING": true,
	"CLAUDE_CODE_ATTRIBUTION_HEADER": true, "CLAUDE_CODE_ENTRYPOINT": true, "ANTHROPIC_BETAS": true, "CLAUDE_CODE_ALWAYS_ENABLE_EFFORT": true, "CLAUDE_CODE_EFFORT_LEVEL": true, "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS": true,
}

func Guard(input Input) error {
	if os.Getenv("CC_HAHA_QUERY_REQUEST_FIXTURE") != "1" || os.Getenv("NODE_ENV") != "production" || os.Getenv("BUN_OPTIONS") != "--no-env-file" {
		return errors.New("Query request requires explicit tool-free offline profile and original inputs")
	}
	if input.Home == "" || !filepath.IsAbs(input.Home) || input.Home != os.Getenv("HOME") || input.Home != os.Getenv("USERPROFILE") || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(input.Home, ".claude") {
		return errors.New("Query request fixture requires disposable HOME/config before business execution")
	}
	home, err := filepath.EvalSymlinks(input.Home)
	if err != nil {
		return err
	}
	root := os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT")
	if root == "" || !filepath.IsAbs(root) {
		return errors.New("trusted allocation root required")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil || !strings.HasPrefix(home, root+string(filepath.Separator)) {
		return errors.New("trusted allocation root required")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	cwd, err = filepath.EvalSymlinks(cwd)
	if err != nil || cwd != home {
		return errors.New("Query request fixture requires disposable HOME/config before business execution")
	}
	for _, path := range []string{input.Home, filepath.Join(input.Home, ".claude")} {
		stat, err := os.Lstat(path)
		if os.IsNotExist(err) && path != input.Home {
			continue
		}
		if err != nil || !stat.IsDir() || stat.Mode()&os.ModeSymlink != 0 {
			return errors.New("Query request requires private nonsymlink directories")
		}
	}
	origin, err := url.Parse(input.Origin)
	if err != nil || origin.Scheme != "http" || origin.Hostname() != "127.0.0.1" || origin.Port() == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" || input.Origin != os.Getenv("ANTHROPIC_BASE_URL") {
		return errors.New("Query request requires exact HTTP loopback origin")
	}
	if input.NonInteractive == nil || input.Env == nil || input.MaxOutputTokens != 32000 || input.DeviceID != "fixture-query-device" || input.SessionID != "00000000-0000-4000-8000-000000000000" || input.HistorySources == nil || input.SystemSources == nil || input.Runtime.Version == "" || input.Runtime.Platform == "" || input.Runtime.Arch == "" {
		return errors.New("Query request requires explicit original host inputs")
	}
	for name := range input.Env {
		if !requestEnv[name] {
			return errors.New("Query request forbids ambient configuration and provider overrides")
		}
	}
	return nil
}
func headers(value http.Header) [][2]string {
	pairs := [][2]string{}
	for name, values := range value {
		pairs = append(pairs, [2]string{strings.ToLower(name), strings.Join(values, ", ")})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i][0] < pairs[j][0] })
	return pairs
}
func jsonStrings(raw []json.RawMessage) []string {
	values := []string{}
	for _, value := range raw {
		values = append(values, string(value))
	}
	return values
}
func emitJSON(yield queryloop.Yield) ([]byte, error) {
	if yield.RequestStart {
		return []byte(`{"type":"stream_request_start"}`), nil
	}
	if yield.Message != nil {
		return yield.Message.MarshalJSON()
	}
	return yield.Stream.EventJSON()
}
func Observe(input Input) (Observation, error) {
	observation := Observation{ModelHistory: [][]string{}, Yielded: []string{}, YieldedAfter: []string{}, History: []string{}, Allocations: []Allocation{}, Requests: []Request{}}
	calls := 0
	uuid := func() string {
		calls++
		value := fmt.Sprintf("query-request-id-%d", calls)
		observation.Allocations = append(observation.Allocations, Allocation{"uuid", value})
		return value
	}
	now := func() time.Time {
		observation.Allocations = append(observation.Allocations, Allocation{"time", "2023-11-14T22:13:20.000Z"})
		return time.UnixMilli(1700000000000)
	}
	history := []queryloop.Message{}
	for _, source := range input.HistorySources {
		var envelope protocol.JSONObject
		if err := json.Unmarshal([]byte(source), &envelope); err != nil {
			return observation, err
		}
		history = append(history, queryloop.Message{Record: envelope})
	}
	system := []json.RawMessage{}
	for _, source := range input.SystemSources {
		system = append(system, json.RawMessage(source))
	}
	env := map[string]string{"ANTHROPIC_BASE_URL": input.Origin, "ANTHROPIC_API_KEY": "offline-fixture-key", "CLAUDE_CODE_SIMPLE": "1", "CLAUDE_CODE_DISABLE_FAST_MODE": "1", "CLAUDE_CODE_MAX_RETRIES": "0"}
	for name, value := range input.Env {
		env[name] = value
	}
	querySource := "sdk"
	if !*input.NonInteractive {
		querySource = "repl_main_thread"
	}
	host := queryloop.Host{
		Preflight: func(_ context.Context, round queryloop.Round) ([]queryloop.Message, error) { return round.History, nil },
		ResolveModelInput: func(_ context.Context, round queryloop.Round) (requestretry.Input, error) {
			raw, err := queryloop.Snapshot(round.History)
			if err != nil {
				return requestretry.Input{}, err
			}
			observation.ModelHistory = append(observation.ModelHistory, jsonStrings(raw))
			original := []protocol.JSONObject{}
			for _, source := range raw {
				var envelope protocol.JSONObject
				if err := json.Unmarshal(source, &envelope); err != nil {
					return requestretry.Input{}, err
				}
				original = append(original, envelope)
			}
			return requestretry.Input{Context: requestcontext.Context{History: original, SystemPrompt: system, Tools: nil, Thinking: requestcontext.Thinking{Type: "disabled"}, Options: requestcontext.Options{Model: "fixture-model", QuerySource: querySource, IsNonInteractiveSession: *input.NonInteractive, MaxOutputTokensOverride: &input.MaxOutputTokens}, Host: requestcontext.Host{Profile: requestcontext.DirectRelayProfile, Env: env, DeviceID: input.DeviceID, SessionID: input.SessionID, SDKBetas: []string{}, TTLEligible: false, TTLAllowlist: []string{}}}, Runtime: input.Runtime}, nil
		},
		Request: requestretry.Dependencies{Random: func() float64 { return 0 }, Sleep: func(context.Context, float64) error { return nil }, ObserveRequest: func(endpoint string, header http.Header, body json.RawMessage) {
			parsed, _ := url.Parse(endpoint)
			observation.Requests = append(observation.Requests, Request{parsed.RequestURI(), "POST", headers(header), string(body)})
		}},
		LookupBackfill: func(string) (queryloop.Backfill, error) {
			return nil, errors.New("tool-free request cannot resolve native backfill")
		},
		ExecuteBatch: func(context.Context, queryloop.Round, []queryloop.Invocation, func(queryloop.Message) error) (any, error) {
			return nil, errors.New("tool-free request cannot execute native tools")
		},
		NormalizeToolResult: func(queryloop.Message) ([]queryloop.Message, error) {
			return nil, errors.New("tool-free request cannot normalize native tool results")
		},
		PostSampling: func(queryloop.Round, []*streamhistory.Envelope) error { return nil },
		StopFailure: func(queryloop.Round, *streamhistory.Envelope) error {
			return errors.New("successful fixture received terminal failure")
		},
		AbortCleanup: func(context.Context, queryloop.Round) error {
			return errors.New("successful fixture received caller abort")
		},
		StopHooks: func(context.Context, queryloop.Round, []*streamhistory.Envelope, queryloop.Emit) (queryloop.StopDecision, error) {
			return queryloop.StopDecision{}, nil
		},
		PauseAfterTools: func(context.Context, queryloop.Round) (bool, error) {
			return false, errors.New("tool-free request cannot pause a native tool batch")
		},
	}
	observed := []queryloop.Yield{}
	outcome, err := queryloop.Run(context.Background(), history, queryloop.Options{Profile: queryloop.SuccessfulToolRound, Model: "fixture-model", QuerySource: querySource, NonInteractive: *input.NonInteractive, MaxTurns: 1, UUID: uuid, Now: now}, host, func(yield queryloop.Yield) error {
		raw, err := emitJSON(yield)
		if err != nil {
			return err
		}
		observation.Yielded = append(observation.Yielded, string(raw))
		observed = append(observed, yield)
		return nil
	})
	if err != nil {
		return observation, err
	}
	observation.Terminal = map[string]string{"reason": outcome.Reason}
	for _, yield := range observed {
		raw, err := emitJSON(yield)
		if err != nil {
			return observation, err
		}
		observation.YieldedAfter = append(observation.YieldedAfter, string(raw))
	}
	raw, err := queryloop.Snapshot(outcome.History)
	if err != nil {
		return observation, err
	}
	observation.History = jsonStrings(raw)
	return observation, nil
}
func stringPointer(value string) *string { return &value }
