package modelyield

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/providernative"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
)

func TestNativeDemandAdministrativeCancelDoesNotFlushPartialAsModelAbort(t *testing.T) {
	var requests atomic.Int32
	peer := make(chan struct{})
	prefix := responseStart + abortEvent(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`) + abortEvent(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"visible but not committed"}}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, prefix)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(peer)
	}))
	defer server.Close()
	input, err := contextFromFixture(fixtureInput{Env: map[string]string{"ANTHROPIC_BASE_URL": server.URL, "ANTHROPIC_API_KEY": "private-native-key", "CLAUDE_CODE_MAX_RETRIES": "0", "CLAUDE_CODE_DISABLE_FAST_MODE": "1"}, Model: "deepseek-flash", MaxOutputTokens: 32000, DeviceID: "fixture-device", SessionID: "00000000-0000-4000-8000-000000000000", Runtime: provider.SourceRuntime{Platform: "darwin", Arch: "arm64", Version: "v24.3.0"}})
	if err != nil {
		t.Fatal(err)
	}
	owner := cooperative.New()
	defer owner.Close()
	admission, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	original := errors.New("original host scope cancellation before next demanded read")
	var failure error
	var outcome Outcome
	events, committed := 0, 0
	if err := owner.Run(admission, func(scope *cooperative.Scope) error {
		outcome, failure = Run(context.Background(), input, Dependencies{Request: requestretry.Dependencies{StreamOwner: owner, StreamScope: scope, UUID: func() string { return "original-id" }, Now: func() time.Time { return time.UnixMilli(1700000000000) }, Random: func() float64 { return 0 }, Sleep: func(context.Context, float64) error { return nil }, NativeOpenIO: providernative.NewFactory(owner, scope, nil), BodyIOAwait: func(kind provider.OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
			return scope.AwaitBytes(cooperative.StreamNext, work)
		}}, Emit: func(value Yield) (requestretry.NoticeAction, error) {
			if value.Update.Envelope != nil {
				committed++
			} else {
				events++
				if events == 3 {
					cancel(original)
				}
			}
			return requestretry.Resume, nil
		}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-peer:
	case <-time.After(time.Second):
		t.Fatal("administrative demand failure did not close actual peer")
	}
	if outcome.Reason != "" || committed != 0 || events != 3 || requests.Load() != 1 || !provider.IsOwnerWaitFailure(failure) || !errors.Is(failure, original) {
		t.Fatal("administrative failure became model quiet abort/partial history", outcome, failure, events, committed, requests.Load())
	}
}
