package modelyield

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/providernative"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
)

// Original inputs go through once Prepare, native headers and the same opener.
// No classified error, prepared request, model envelope or result is injected.
func TestNativeCreationOwnerFailureDoesNotEmitSDKTerminal(t *testing.T) {
	for _, mode := range []string{"owner-close", "scope-cancel", "scope-cancel-before-model-cancel"} {
		t.Run(mode, func(t *testing.T) {
			entered := make(chan struct{})
			peerClosed := make(chan struct{})
			var count atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Length", "200")
				w.Header().Set("Request-Id", "original-error-request")
				w.WriteHeader(500)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				close(peerClosed)
			}))
			defer server.Close()
			input, err := contextFromFixture(fixtureInput{Env: map[string]string{"ANTHROPIC_BASE_URL": server.URL, "ANTHROPIC_API_KEY": "private-original-key", "CLAUDE_CODE_DISABLE_FAST_MODE": "1", "CLAUDE_CODE_MAX_RETRIES": "0"}, Model: "deepseek-flash", MaxOutputTokens: 32000, DeviceID: "fixture-device", SessionID: "00000000-0000-4000-8000-000000000000", Runtime: provider.SourceRuntime{Platform: "darwin", Arch: "arm64", Version: "v24.3.0"}})
			if err != nil {
				t.Fatal(err)
			}
			owner := cooperative.New()
			defer owner.Close()
			runctx, stop := context.WithCancelCause(context.Background())
			defer stop(nil)
			caller, cancel := context.WithCancel(context.Background())
			defer cancel()
			original := errors.New("original ASK scope cancelled independently of model")
			if mode == "owner-close" {
				original = cooperative.ErrClosed
			}
			type observation struct {
				outcome              Outcome
				failure              error
				ids, prepares, emits int
			}
			done := make(chan observation, 1)
			returned := make(chan error, 1)
			go func() {
				returned <- owner.Run(runctx, func(scope *cooperative.Scope) error {
					seen := observation{}
					seen.outcome, seen.failure = Run(caller, input, Dependencies{Request: requestretry.Dependencies{
						StreamOwner: owner, StreamScope: scope,
						UUID: func() string { seen.ids++; return fmt.Sprintf("id-%d", seen.ids) }, Now: func() time.Time { return time.UnixMilli(1700000000000) }, Random: func() float64 { return 0 }, Sleep: func(context.Context, float64) error { return errors.New("administrative failure retried") },
						OnPreparation: func(_ []protocol.JSONObject) { seen.prepares++ },
						NativeOpenIO:  providernative.NewFactory(owner, scope, nil), BodyIOAwait: func(kind provider.OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
							if kind != provider.OwnerErrorBody {
								return nil, fmt.Errorf("unexpected body kind %v", kind)
							}
							close(entered)
							return scope.AwaitBytes(cooperative.StreamNext, work)
						},
					}, Emit: func(Yield) (requestretry.NoticeAction, error) { seen.emits++; return requestretry.Resume, nil }})
					done <- seen
					return nil
				})
			}()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("native body not demanded")
			}
			if mode == "owner-close" {
				owner.Close()
			} else {
				// This declared control establishes the administrative scope
				// cause first. An unordered model-cancel/Close race can finish
				// the HTTP error before Close, so it is retained as separate red.
				stop(original)
				if mode == "scope-cancel-before-model-cancel" {
					cancel()
				}
			}
			seen := <-done
			<-returned
			select {
			case <-peerClosed:
			case <-time.After(time.Second):
				t.Fatal("body failure did not release actual peer")
			}
			t.Logf("CREATION_ADMIN mode=%s outcome=%+v failure=%T %v ids=%d preparation=%d emits=%d requests=%d", mode, seen.outcome, seen.failure, seen.failure, seen.ids, seen.prepares, seen.emits, count.Load())
			var exhausted *retrypolicy.CannotRetryError
			if !errors.As(seen.failure, &exhausted) || !provider.IsOwnerWaitFailure(exhausted.Original) {
				t.Fatal("original retry wrapper was replaced", seen.failure)
			}
			if seen.emits != 0 || seen.ids != 0 || seen.prepares != 1 || count.Load() != 1 || !provider.IsOwnerWaitFailure(seen.failure) || !errors.Is(seen.failure, original) || seen.outcome.Reason != "" || seen.outcome.Terminal {
				t.Fatal("administrative body failure became SDK result", seen)
			}
		})
	}
}
