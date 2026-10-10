package requestretry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

type fixtureInput struct {
	Name, Mode, Home              string
	Env                           map[string]string
	HistorySources, SystemSources []string
	Model                         string
	MaxOutputTokens               float64
	DeviceID, SessionID           string
	Runtime                       provider.SourceRuntime
	SDKBetas                      []string
	NonInteractive                *bool
	QuerySource                   string
	ToolDefinitions               []protocol.JSONObject
	ExtraToolSchemaSources        []string
	ToolChoiceSource              string
	Auth                          OriginalAuthInputs
}
type fetchObservation struct {
	Path    string      `json:"path"`
	Method  string      `json:"method"`
	Headers [][2]string `json:"headers"`
	Body    string      `json:"body"`
}
type historyObservation struct {
	Type          string `json:"type"`
	UUID          any    `json:"uuid"`
	MessageSource string `json:"messageSource"`
}
type fixtureObservation struct {
	Clients            int                  `json:"clients"`
	KeyLookups         int                  `json:"keyLookups"`
	Requests           []fetchObservation   `json:"requests"`
	Notices            []string             `json:"notices"`
	Sleeps             []float64            `json:"sleeps"`
	Yielded            []string             `json:"yielded"`
	YieldedAfter       []string             `json:"yieldedAfter"`
	HistoryBefore      []string             `json:"historyBefore"`
	HistoryAfter       []string             `json:"historyAfter"`
	NormalizedHistory  []historyObservation `json:"normalizedHistory"`
	PreparationCalls   int                  `json:"preparationCalls"`
	UUIDCallsOnRequest []int                `json:"uuidCallsOnRequest"`
	UUIDCalls          int                  `json:"uuidCalls"`
	Outcome            string               `json:"outcome"`
	Pause              any                  `json:"pause"`
	PromptCalls        map[string]int       `json:"promptCalls,omitempty"`
}

func contextFromFixture(input fixtureInput) (Input, error) {
	ctx := requestcontext.Context{Thinking: requestcontext.Thinking{Type: "disabled"}, Options: requestcontext.Options{Model: input.Model, QuerySource: "sdk", IsNonInteractiveSession: true, MaxOutputTokensOverride: &input.MaxOutputTokens}, Host: requestcontext.Host{Profile: requestcontext.DirectRelayProfile, Env: input.Env, DeviceID: input.DeviceID, SessionID: input.SessionID, SDKBetas: input.SDKBetas}}
	if input.NonInteractive != nil {
		ctx.Options.IsNonInteractiveSession = *input.NonInteractive
	}
	if input.QuerySource != "" {
		ctx.Options.QuerySource = input.QuerySource
	}
	if input.ToolDefinitions != nil {
		ctx.Tools = input.ToolDefinitions
		ctx.Host.ToolSchemas = toolschema.NewSession()
		ctx.Host.ToolSchemaHost = &toolschema.Host{MainLoopModel: input.Model}
	}
	for _, source := range input.ExtraToolSchemaSources {
		ctx.Options.ExtraToolSchemas = append(ctx.Options.ExtraToolSchemas, json.RawMessage(source))
	}
	if input.ToolChoiceSource != "" {
		ctx.Options.ToolChoice = json.RawMessage(input.ToolChoiceSource)
	}
	for _, source := range input.HistorySources {
		var value protocol.JSONObject
		if err := json.Unmarshal([]byte(source), &value); err != nil {
			return Input{}, err
		}
		ctx.History = append(ctx.History, value)
	}
	for _, source := range input.SystemSources {
		ctx.SystemPrompt = append(ctx.SystemPrompt, json.RawMessage(source))
	}
	return Input{Context: ctx, Runtime: input.Runtime, Auth: input.Auth}, nil
}
func headerPairs(headers http.Header) [][2]string {
	pairs := [][2]string{}
	for name, values := range headers {
		pairs = append(pairs, [2]string{strings.ToLower(name), strings.Join(values, ", ")})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i][0] < pairs[j][0] })
	return pairs
}
func observeFixture(input fixtureInput) (observation fixtureObservation, failure error) {
	observation = fixtureObservation{Requests: []fetchObservation{}, Notices: []string{}, Sleeps: []float64{}, Yielded: []string{}, YieldedAfter: []string{}, HistoryBefore: append([]string{}, input.HistorySources...), HistoryAfter: append([]string{}, input.HistorySources...), NormalizedHistory: []historyObservation{}, UUIDCallsOnRequest: []int{}}
	original, err := contextFromFixture(input)
	if err != nil {
		return observation, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if input.Mode == "abort-before" {
		cancel()
	}
	uuid := func() string { observation.UUIDCalls++; return fmt.Sprintf("retry-id-%d", observation.UUIDCalls) }
	live := []func() ([]byte, error){}
	appendYield := func(raw []byte, after func() ([]byte, error)) {
		observation.Yielded = append(observation.Yielded, string(raw))
		live = append(live, after)
	}
	control := func(path string) (map[string]any, error) {
		response, err := http.Get(input.Env["ANTHROPIC_BASE_URL"] + path)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		var value map[string]any
		err = json.NewDecoder(response.Body).Decode(&value)
		return value, err
	}
	deps := Dependencies{UUID: uuid, Now: func() time.Time { return time.UnixMilli(1700000000000) }, Random: func() float64 { return 0 },
		Sleep: func(ctx context.Context, ms float64) error {
			observation.Sleeps = append(observation.Sleeps, ms)
			if input.Mode == "abort-sleep" {
				cancel()
				return &provider.SDKStreamError{Kind: "caller_abort", Class: "APIUserAbortError", Name: "Error", Message: "Request was aborted."}
			}
			return nil
		},
		Notice: func(ctx context.Context, notice retrypolicy.RetryNotice) (NoticeAction, error) {
			envelope, err := notice.Envelope()
			if err != nil {
				return 0, err
			}
			raw, err := envelope.MarshalJSON()
			if err != nil {
				return 0, err
			}
			raw = protocol.StringifyValidatedJavaScriptJSON(raw)
			observation.Notices = append(observation.Notices, string(raw))
			captured := append([]byte{}, raw...)
			appendYield(raw, func() ([]byte, error) { return captured, nil })
			if input.Mode == "pause" && len(observation.Notices) == 1 {
				time.Sleep(15 * time.Millisecond)
				server, err := control("/__fixture/state")
				if err != nil {
					return 0, err
				}
				observation.Pause = map[string]any{"sleeps": append([]float64{}, observation.Sleeps...), "requests": len(observation.Requests), "serverRequests": server["requests"]}
			}
			if input.Mode == "stop-notice" {
				return Stop, nil
			}
			return Resume, nil
		},
		OnClient: func() { observation.Clients++ }, OnKeyLookup: func() { observation.KeyLookups++ },
		OnPreparation: func(history []protocol.JSONObject) {
			observation.PreparationCalls++
			for _, envelope := range history {
				var identity any
				if raw := envelope.Get("uuid"); raw != nil {
					_ = json.Unmarshal(raw, &identity)
				}
				observation.NormalizedHistory = append(observation.NormalizedHistory, historyObservation{envelope.String("type"), identity, string(protocol.StringifyValidatedJavaScriptJSON(envelope.Get("message")))})
			}
		},
		ObserveRequest: func(endpoint string, headers http.Header, body json.RawMessage) {
			parsed, _ := url.Parse(endpoint)
			observation.UUIDCallsOnRequest = append(observation.UUIDCallsOnRequest, observation.UUIDCalls)
			observation.Requests = append(observation.Requests, fetchObservation{parsed.RequestURI(), "POST", headerPairs(headers), string(body)})
		},
	}
	// Cancellation happens only after the actual socket server has received a
	// complete POST. No fabricated SDK fault replaces the physical operation.
	if input.Mode == "abort-headers" {
		done := make(chan struct{})
		defer func() { <-done }()
		go func() { defer close(done); _, _ = control("/__fixture/request-seen"); cancel() }()
	}
	stream, err := Open(ctx, original, deps)
	if stream != nil {
		defer stream.Close()
	}
	if errors.Is(err, ErrConsumerStopped) {
		observation.Outcome = "consumer_stopped"
	} else if IsCallerAbort(err) {
		observation.Outcome = "caller_aborted"
	} else if err != nil {
		return observation, err
	} else {
		controller, err := streamhistory.New(streamhistory.Options{UUID: uuid, Now: deps.Now, RequestID: stream.RequestID, RefusalProfile: &streamhistory.RefusalProfile{Model: input.Model, NonInteractive: original.Context.Options.IsNonInteractiveSession}})
		if err != nil {
			return observation, err
		}
		emit := func(update streamhistory.Update) error {
			if update.Envelope != nil {
				raw, err := update.Envelope.MarshalJSON()
				if err != nil {
					return err
				}
				appendYield(raw, update.Envelope.MarshalJSON)
			} else {
				raw, err := update.EventJSON()
				if err != nil {
					return err
				}
				captured := append([]byte{}, raw...)
				appendYield(raw, func() ([]byte, error) { return captured, nil })
			}
			if input.Mode == "stop-stream" {
				return ErrConsumerStopped
			}
			return nil
		}
		for {
			part, err := stream.Next()
			if err == nil {
				err = controller.Feed(part, streamhistory.Timing{}, emit)
				if err == nil {
					continue
				}
			}
			if errors.Is(err, ErrConsumerStopped) {
				observation.Outcome = "consumer_stopped"
				break
			}
			if errors.Is(err, io.EOF) {
				if stream.AbortCause() != nil {
					observation.Outcome = "caller_aborted"
					break
				}
				if err = controller.Finish(nil, emit); err != nil {
					return observation, err
				}
				observation.Outcome = "success"
				break
			}
			return observation, err
		}
	}
	for _, value := range live {
		raw, err := value()
		if err != nil {
			return observation, err
		}
		observation.YieldedAfter = append(observation.YieldedAfter, string(raw))
	}
	for index, envelope := range original.Context.History {
		raw, _ := envelope.MarshalJSON()
		observation.HistoryAfter[index] = string(protocol.StringifyValidatedJavaScriptJSON(raw))
	}
	if original.Context.Host.ToolSchemas != nil {
		observation.PromptCalls = original.Context.Host.ToolSchemas.PromptCalls()
	}
	return observation, nil
}
func fixtureHome(input fixtureInput) error {
	home, err := filepath.EvalSymlinks(input.Home)
	root, rootErr := filepath.EvalSymlinks(os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT"))
	cwd, _ := os.Getwd()
	cwd, _ = filepath.EvalSymlinks(cwd)
	if err != nil || rootErr != nil || input.Home != os.Getenv("HOME") || input.Home != os.Getenv("USERPROFILE") || cwd != home || !strings.HasPrefix(home, root+string(filepath.Separator)) || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(input.Home, ".claude") || os.Getenv("NODE_ENV") != "production" || os.Getenv("BUN_OPTIONS") != "--no-env-file" || os.Getenv("CC_HAHA_REQUEST_RETRY_FIXTURE") != "1" {
		return errors.New("request retry requires disposable HOME/config before business execution")
	}
	for _, path := range []string{input.Home, filepath.Join(input.Home, ".claude")} {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("request retry requires non-symlink private directories")
		}
	}
	origin, err := url.Parse(input.Env["ANTHROPIC_BASE_URL"])
	if err != nil || origin.Scheme != "http" || origin.Hostname() != "127.0.0.1" || origin.Port() == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return errors.New("request retry requires exact HTTP loopback origin")
	}
	return nil
}
func TestRequestRetryFixture(t *testing.T) {
	if os.Getenv("CC_HAHA_REQUEST_RETRY_FIXTURE") == "" {
		t.Skip("subprocess oracle")
	}
	var input fixtureInput
	decoder := json.NewDecoder(os.Stdin)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		t.Fatal(err)
	}
	if err := fixtureHome(input); err != nil {
		t.Fatal(err)
	}
	observation, err := observeFixture(input)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"observation": observation})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("REQUEST_RETRY_RESULT:%s\n", raw)
}
