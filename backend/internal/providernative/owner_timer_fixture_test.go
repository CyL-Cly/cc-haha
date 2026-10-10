package providernative

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
)

type ownerPhaseInput struct {
	Home, Origin, APIKey string
	Body                 json.RawMessage
	Profile              struct {
		Name                  string
		Timeout, ReleaseDelay int
		Headers, BodyRelease  string
		CallerAbortDuringHold bool
	}
	Old bool
}

func TestOwnerTimerCompositionFixture(t *testing.T) {
	if os.Getenv("CC_HAHA_OWNER_TIMER_FIXTURE") != "1" {
		t.Skip("explicit original-input fixture only")
	}
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		t.Fatal(err)
	}
	var input ownerPhaseInput
	if err = json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	origin, originErr := url.Parse(input.Origin)
	home, err := filepath.EvalSymlinks(input.Home)
	if err != nil || home != os.Getenv("HOME") || home != input.Home || !strings.HasPrefix(filepath.Base(home), "cc-haha-") || originErr != nil || origin.Scheme != "http" || origin.Hostname() != "127.0.0.1" || origin.Port() == "" || origin.User != nil || input.Origin != "http://"+origin.Host || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(home, ".claude") {
		t.Fatal("requires explicit private fixture home/origin")
	}
	clockStart := time.Now()
	var clockDiagnostics []map[string]any
	var trace []map[string]any
	event := func(kind string, fields map[string]any) {
		if kind == "sdk-timer-arm" || kind == "owner-held-enter" || kind == "owner-held-exit" {
			clockDiagnostics = append(clockDiagnostics, map[string]any{"kind": kind, "millis": float64(time.Since(clockStart).Microseconds()) / 1000})
		}
		value := map[string]any{"kind": kind}
		for key, v := range fields {
			value[key] = v
		}
		trace = append(trace, value)
	}
	mark := func(name string) {
		if e := os.WriteFile(filepath.Join(home, name), []byte("1"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	caller, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	owner := cooperative.New()
	defer owner.Close()
	entered := make(chan struct{})
	held, released, cleared := false, false, false
	var holdReceipt *cooperative.Receipt
	queued := make(chan struct{})
	go func() {
		<-entered
		holdReceipt, err = owner.Submit(context.Background(), cooperative.Read, func(scope *cooperative.Scope) error {
			end := time.Now().Add(3 * time.Second)
			for {
				if _, e := os.Stat(filepath.Join(home, "request-ready")); e == nil {
					break
				}
				if time.Now().After(end) {
					return errors.New("request-ready missing")
				}
			}
			held = true
			event("owner-held-enter", nil)
			mark("owner-entered")
			end = time.Now().Add(3 * time.Second)
			for {
				if _, e := os.Stat(filepath.Join(home, "owner-release")); e == nil {
					break
				}
				if time.Now().After(end) {
					return errors.New("owner-release missing")
				}
			}
			if input.Profile.CallerAbortDuringHold {
				event("original-caller-abort", map[string]any{"whileHeld": true})
				cancel(errors.New("original caller cancellation"))
			}
			released = true
			event("owner-held-exit", map[string]any{"releaseReached": true, "callerAborted": caller.Err() != nil})
			return nil
		})
		close(queued)
	}()
	body, err := protocol.ParseAndStringifyJavaScriptJSON(input.Body)
	if err != nil {
		t.Fatal(err)
	}
	headers := http.Header{}
	for _, entry := range [][2]string{{"User-Agent", "Anthropic/JS 0.80.0"}, {"X-Stainless-Retry-Count", "0"}, {"X-Stainless-Timeout", "0"}, {"X-Stainless-Lang", "js"}, {"X-Stainless-Package-Version", "0.80.0"}, {"X-Stainless-OS", "MacOS"}, {"X-Stainless-Arch", "arm64"}, {"X-Stainless-Runtime", "node"}, {"X-Stainless-Runtime-Version", "v24.3.0"}} {
		headers.Set(entry[0], entry[1])
	}
	outcome := map[string]any{}
	var responseMetadata any
	var leaseState any
	err = owner.Run(caller, func(scope *cooperative.Scope) error {
		observe := func(value provider.OwnerNativeEvent) {
			switch value.Kind {
			case "timer-arm":
				event("sdk-timer-arm", map[string]any{"delay": input.Profile.Timeout})
			case "timer-fire":
				event("sdk-timer-fire", nil)
			case "timer-clear":
				cleared = true
				event("sdk-timer-clear", nil)
				mark("fetch-finally-clear")
			case "fetch-enter":
				event("actual-fetch-enter", map[string]any{"method": "POST", "aborted": false})
				close(entered)
				<-queued
			case "fetch-fulfilled":
				event("actual-fetch-fulfilled", map[string]any{"aborted": caller.Err() != nil, "status": value.Status})
			case "failed-lease-closed":
				state := value.Resource
				leaseState = map[string]any{"closed": state.Closed, "joined": state.Joined, "operationClosed": state.OperationClosed, "pendingReadiness": state.PendingReadiness}
			case "fetch-rejected":
				event("actual-fetch-rejected", map[string]any{"nativeError": value.Err.Error()})
			}
		}
		driver, e := New(owner, scope, observe)
		if e != nil {
			return e
		}
		config := provider.AnthropicConfig{APIKey: input.APIKey, BaseURL: input.Origin, Timeout: time.Duration(input.Profile.Timeout) * time.Millisecond, DefaultHeaders: headers, HTTPExchange: &provider.RawHTTP1Exchange{}, OwnerNativeIO: driver}
		client := provider.NewAnthropic(config)
		exchange, stop, e := client.OpenPreparedResponse(caller, &provider.MessagesRequest{Body: body, Path: "/v1/messages", Headers: http.Header{}})
		if e != nil {
			var sdk *provider.SDKStreamError
			// Existing public SDK projection already owns this error.
			if errors.As(e, &sdk) {
				outcome = map[string]any{"kind": "rejected", "class": sdk.Class, "name": sdk.Name, "message": sdk.Message}
			} else {
				return e
			}
			return nil
		}
		defer stop()
		defer exchange.Response.Body.Close()
		response := exchange.Response
		responseMetadata = map[string]any{"status": response.StatusCode, "headers": response.Header, "requestID": response.Header.Get("request-id")}
		raw, e := scope.AwaitBytes(cooperative.StreamNext, func(ctx context.Context) ([]byte, error) {
			done := make(chan struct{})
			stop := context.AfterFunc(ctx, func() { _ = response.Body.Close(); close(done) })
			defer func() {
				if !stop() {
					<-done
				}
			}()
			return io.ReadAll(response.Body)
		})
		if e != nil {
			return e
		}
		if response.StatusCode >= 400 {
			return provider.NewSDKResponseError(&response.StatusCode, string(raw), exchange.OriginalHeaders, true)
		}
		value, e := protocol.ParseAndStringifyJavaScriptJSON(raw)
		if e != nil {
			return e
		}
		outcome = map[string]any{"kind": "fulfilled", "message": json.RawMessage(value)}
		_ = response.Body.Close()
		state, e := provider.InspectNativeOwnerBody(response.Body)
		if e != nil {
			return e
		}
		leaseState = map[string]any{"closed": state.Closed, "joined": state.Joined, "operationClosed": state.OperationClosed, "pendingReadiness": state.PendingReadiness}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if holdReceipt == nil {
		t.Fatal("owner hold missing")
	}
	if err = holdReceipt.Wait(); err != nil {
		t.Fatal(err)
	}
	event("request-settled", map[string]any{"outcome": outcome["kind"]})
	observed := map[string]any{"profile": input.Profile.Name, "outcome": outcome, "trace": trace, "fetchCalls": 1, "held": held, "released": released, "fetchFinallyCleared": cleared, "callerAborted": caller.Err() != nil, "responseMetadata": responseMetadata, "leaseState": leaseState}
	clockRaw, _ := json.Marshal(clockDiagnostics)
	if err = os.WriteFile(filepath.Join(home, "clock-diagnostics.json"), clockRaw, 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	_ = json.NewEncoder(&out).Encode(observed)
	if err = os.WriteFile(filepath.Join(home, "observation.json"), out.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
}
