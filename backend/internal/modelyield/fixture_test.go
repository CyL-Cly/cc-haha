package modelyield

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

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
)

// This child observes the production model-yield adapter using original inputs.
// Its isolated host, clock and cancellation controls do not supply model history,
// prepared requests, classified faults or rendered terminal messages.
type fixtureInput struct {
	Name, Mode, Home              string
	Env                           map[string]string
	HistorySources, SystemSources []string
	Model                         string
	MaxOutputTokens               float64
	DeviceID, SessionID           string
	Runtime                       provider.SourceRuntime
	SDKBetas                      []string
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
}

func contextFromFixture(input fixtureInput) (requestretry.Input, error) {
	ctx := requestcontext.Context{Thinking: requestcontext.Thinking{Type: "disabled"}, Options: requestcontext.Options{Model: input.Model, QuerySource: "sdk", IsNonInteractiveSession: true, MaxOutputTokensOverride: &input.MaxOutputTokens}, Host: requestcontext.Host{Profile: requestcontext.DirectRelayProfile, Env: input.Env, DeviceID: input.DeviceID, SessionID: input.SessionID, SDKBetas: input.SDKBetas}}
	for _, source := range input.HistorySources {
		var value protocol.JSONObject
		if err := json.Unmarshal([]byte(source), &value); err != nil {
			return requestretry.Input{}, err
		}
		ctx.History = append(ctx.History, value)
	}
	for _, source := range input.SystemSources {
		ctx.SystemPrompt = append(ctx.SystemPrompt, json.RawMessage(source))
	}
	return requestretry.Input{Context: ctx, Runtime: input.Runtime}, nil
}
func headerPairs(headers http.Header) [][2]string {
	pairs := [][2]string{}
	for name, values := range headers {
		pairs = append(pairs, [2]string{strings.ToLower(name), strings.Join(values, ", ")})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i][0] < pairs[j][0] })
	return pairs
}

type modelFixtureInput struct {
	fixtureInput
	Layer string
}
type httpsCARead struct {
	Path string `json:"path"`
	Code string `json:"code"`
}
type modelFixtureObservation struct {
	fixtureObservation
	Failures []any         `json:"failures"`
	CAReads  []httpsCARead `json:"caReads"`
}

func projectHTTPSFailure(failure error) (any, error) {
	var sdk *provider.SDKStreamError
	if errors.As(failure, &sdk) {
		record, err := (retrypolicy.RetryNotice{Error: sdk}).Envelope()
		if err != nil {
			return nil, err
		}
		var cause any
		if sdk.Cause != nil {
			cause, err = projectHTTPSFailure(sdk.Cause)
			if err != nil {
				return nil, err
			}
		}
		quoted := sdk.MessageJSON
		if quoted == nil {
			quoted, _ = json.Marshal(sdk.Message)
			quoted = protocol.StringifyValidatedJavaScriptJSON(quoted)
		}
		return map[string]any{"class": sdk.Class, "name": sdk.Name, "message": sdk.Message, "messageJSON": string(quoted), "json": string(protocol.StringifyValidatedJavaScriptJSON(record.Get("error"))), "cause": cause}, nil
	}
	var native *provider.TLSConnectionFault
	if errors.As(failure, &native) {
		record, err := native.EnumerableErrorJSON()
		if err != nil {
			return nil, err
		}
		raw, err := record.MarshalJSON()
		if err != nil {
			return nil, err
		}
		quoted, _ := json.Marshal(native.Error())
		quoted = protocol.StringifyValidatedJavaScriptJSON(quoted)
		return map[string]any{"class": "Error", "name": "Error", "message": native.Error(), "messageJSON": string(quoted), "json": string(protocol.StringifyValidatedJavaScriptJSON(raw)), "cause": nil}, nil
	}
	return nil, fmt.Errorf("unported actual HTTPS failure %T: %v", failure, failure)
}
func observeModelFixture(input modelFixtureInput) (observation modelFixtureObservation, failure error) {
	observation.fixtureObservation = fixtureObservation{Requests: []fetchObservation{}, Notices: []string{}, Sleeps: []float64{}, Yielded: []string{}, YieldedAfter: []string{}, HistoryBefore: append([]string{}, input.HistorySources...), HistoryAfter: append([]string{}, input.HistorySources...), NormalizedHistory: []historyObservation{}, UUIDCallsOnRequest: []int{}}
	original, err := contextFromFixture(input.fixtureInput)
	if err != nil {
		return observation, err
	}
	observation.Failures = []any{}
	observation.CAReads = []httpsCARead{}
	observationTLS, err := tlsconfig.NewState(func(path string) ([]byte, error) {
		raw, failure := os.ReadFile(path)
		relative, _ := filepath.Rel(input.Home, path)
		code := ""
		if os.IsNotExist(failure) {
			code = "ENOENT"
		} else if failure != nil {
			code = "unported:" + failure.Error()
		}
		observation.CAReads = append(observation.CAReads, httpsCARead{filepath.ToSlash(relative), code})
		return raw, failure
	})
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
		tlsConfig, err := observationTLS.VerifiedConfig(input.Env)
		if err != nil {
			return nil, err
		}
		controlClient := &http.Client{Transport: &http.Transport{TLSClientConfig: tlsConfig, ForceAttemptHTTP2: false}}
		defer controlClient.CloseIdleConnections()
		response, err := controlClient.Get(input.Env["ANTHROPIC_BASE_URL"] + path)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		var value map[string]any
		err = json.NewDecoder(response.Body).Decode(&value)
		return value, err
	}
	var observerFailure error
	deps := requestretry.Dependencies{TLSState: observationTLS, ObserveFailure: func(err error) {
		projected, failure := projectHTTPSFailure(err)
		if failure != nil {
			observerFailure = failure
			return
		}
		observation.Failures = append(observation.Failures, projected)
	}, UUID: uuid, Now: func() time.Time { return time.UnixMilli(1700000000000) }, Random: func() float64 { return 0 },
		Sleep: func(ctx context.Context, ms float64) error {
			observation.Sleeps = append(observation.Sleeps, ms)
			if input.Mode == "abort-sleep" {
				cancel()
				return &provider.SDKStreamError{Kind: "caller_abort", Class: "APIUserAbortError", Name: "Error", Message: "Request was aborted."}
			}
			return nil
		},
		Notice: func(ctx context.Context, notice retrypolicy.RetryNotice) (requestretry.NoticeAction, error) {
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
				return requestretry.Stop, nil
			}
			return requestretry.Resume, nil
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
	if input.Mode == "abort-handshake" {
		done := make(chan struct{})
		defer func() { cancel(); <-done }()
		go func() {
			defer close(done)
			for {
				if _, err := os.ReadFile(filepath.Join(input.Home, "accepted")); err == nil {
					cancel()
					return
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(5 * time.Millisecond):
				}
			}
		}()
	}
	noticeConsumer := deps.Notice
	deps.Notice = nil
	outcome, err := Run(ctx, original, Dependencies{Request: deps, Emit: func(value Yield) (requestretry.NoticeAction, error) {
		if value.Notice != nil {
			return noticeConsumer(ctx, *value.Notice)
		}
		update := value.Update
		if update == nil {
			return 0, errors.New("missing model yield")
		}
		if update.Envelope != nil {
			raw, err := update.Envelope.MarshalJSON()
			if err != nil {
				return 0, err
			}
			appendYield(raw, update.Envelope.MarshalJSON)
		} else {
			raw, err := update.EventJSON()
			if err != nil {
				return 0, err
			}
			captured := append([]byte{}, raw...)
			appendYield(raw, func() ([]byte, error) { return captured, nil })
		}
		if input.Mode == "stop-stream" {
			return requestretry.Stop, nil
		}
		return requestretry.Resume, nil
	}})
	if err != nil {
		return observation, err
	}
	observation.Outcome = outcome.Reason
	if outcome.Reason == "returned" {
		// The original fixture calls normal generator return success even when
		// its last value is an assistant API error. Preserve that observation.
		observation.Outcome = "success"
	}
	if observerFailure != nil {
		return observation, observerFailure
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
	return observation, nil
}
func validateModelFixture(input modelFixtureInput) error {
	home, err := filepath.EvalSymlinks(input.Home)
	allocation, rootErr := filepath.EvalSymlinks(os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT"))
	cwd, _ := os.Getwd()
	cwd, _ = filepath.EvalSymlinks(cwd)
	if err != nil || rootErr != nil || input.Home != os.Getenv("HOME") || input.Home != os.Getenv("USERPROFILE") || cwd != home || !strings.HasPrefix(home, allocation+string(filepath.Separator)) || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(input.Home, ".claude") || os.Getenv("NODE_ENV") != "production" || os.Getenv("BUN_OPTIONS") != "--no-env-file" || os.Getenv("CC_HAHA_MODEL_YIELD_FIXTURE") != "1" {
		return errors.New("HTTPS request requires disposable HOME/config before business")
	}
	for _, path := range []string{input.Home, filepath.Join(input.Home, ".claude")} {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("HTTPS request requires non-symlink private directories")
		}
	}
	origin, err := url.Parse(input.Env["ANTHROPIC_BASE_URL"])
	if err != nil || origin.Scheme != "https" || origin.Hostname() != "127.0.0.1" || origin.Port() == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return errors.New("HTTPS request requires exact private loopback origin")
	}
	if input.Layer != "query" && input.Layer != "creation" {
		return errors.New("HTTPS request requires declared layer")
	}
	if path, ok := input.Env["NODE_EXTRA_CA_CERTS"]; ok {
		if path != filepath.Join(input.Home, "original-ca.pem") {
			return errors.New("HTTPS request requires private original CA path")
		}
		info, err := os.Lstat(path)
		if err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
			return errors.New("HTTPS request requires non-symlink private CA file")
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
func TestModelYieldFixture(t *testing.T) {
	if os.Getenv("CC_HAHA_MODEL_YIELD_FIXTURE") == "" {
		t.Skip("actual TLS differential child")
	}
	var input modelFixtureInput
	decoder := json.NewDecoder(os.Stdin)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		t.Fatal(err)
	}
	if err := validateModelFixture(input); err != nil {
		t.Fatal(err)
	}
	observation, err := observeModelFixture(input)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"observation": observation})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("MODEL_YIELD_RESULT:%s\n", raw)
}
