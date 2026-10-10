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
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
)

type httpsFixtureInput struct {
	fixtureInput
	Layer string
}
type httpsCARead struct {
	Path string `json:"path"`
	Code string `json:"code"`
}
type httpsFixtureObservation struct {
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
func observeHTTPSFixture(input httpsFixtureInput, await provider.OwnerAwait) (observation httpsFixtureObservation, failure error) {
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
	deps := Dependencies{OpenIOAwait: await, TLSState: observationTLS, ObserveFailure: func(err error) {
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
	stream, err := Open(ctx, original, deps)
	if stream != nil && input.Mode == "abort-body" {
		timer := time.AfterFunc(40*time.Millisecond, cancel)
		defer timer.Stop()
	}
	if stream != nil {
		defer stream.Close()
	}
	if errors.Is(err, ErrConsumerStopped) {
		observation.Outcome = "consumer_stopped"
	} else if IsCallerAbort(err) {
		observation.Outcome = "caller_aborted"
	} else if err != nil {
		if input.Layer == "creation" {
			observation.Outcome = "creation_failed"
		} else {
			return observation, err
		}
	} else {
		controller, err := streamhistory.New(streamhistory.Options{UUID: uuid, Now: deps.Now, RequestID: stream.RequestID, RefusalProfile: &streamhistory.RefusalProfile{Model: input.Model, NonInteractive: true}})
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
func validateHTTPSFixture(input httpsFixtureInput) error {
	home, err := filepath.EvalSymlinks(input.Home)
	allocation, rootErr := filepath.EvalSymlinks(os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT"))
	cwd, _ := os.Getwd()
	cwd, _ = filepath.EvalSymlinks(cwd)
	if err != nil || rootErr != nil || input.Home != os.Getenv("HOME") || input.Home != os.Getenv("USERPROFILE") || cwd != home || !strings.HasPrefix(home, allocation+string(filepath.Separator)) || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(input.Home, ".claude") || os.Getenv("NODE_ENV") != "production" || os.Getenv("BUN_OPTIONS") != "--no-env-file" || os.Getenv("CC_HAHA_HTTPS_REQUEST_FIXTURE") != "1" {
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
func TestHTTPSRequestFixture(t *testing.T) {
	if os.Getenv("CC_HAHA_HTTPS_REQUEST_FIXTURE") == "" {
		t.Skip("actual TLS differential child")
	}
	var input httpsFixtureInput
	decoder := json.NewDecoder(os.Stdin)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		t.Fatal(err)
	}
	if err := validateHTTPSFixture(input); err != nil {
		t.Fatal(err)
	}
	owner := cooperative.New()
	defer owner.Close()
	var observation httpsFixtureObservation
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		var failure error
		observation, failure = observeHTTPSFixture(input, func(kind provider.OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
			wait := cooperative.NetworkOpen
			if kind == provider.OwnerBodyRead {
				wait = cooperative.StreamNext
			}
			return scope.AwaitBytes(wait, work)
		})
		return failure
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"observation": observation})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("HTTPS_REQUEST_RESULT:%s\n", raw)
}
