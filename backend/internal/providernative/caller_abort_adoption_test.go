package providernative

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

// This private observer retains the real Boundary/native HTTP reservation.
// It pauses only after the real complete-header receipt, before adoption. The
// caller's own cancellation closes that resource; no fabricated response or
// injected transport fault substitutes for the physical close.
type abortAfterHeadersBoundary struct {
	*Boundary
	cancel            context.CancelFunc
	checkpointFailure error
}

func (b *abortAfterHeadersBoundary) AwaitHeaders(op provider.NativeOwnerOperation) ([]byte, error) {
	raw, e := b.Boundary.AwaitHeaders(op)
	if e != nil {
		return raw, e
	}
	b.cancel()
	limit, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	for {
		_, _, failure := op.Checkpoint()
		if failure != nil {
			b.checkpointFailure = failure
			return raw, nil
		}
		if e := op.WaitReadable(limit); e != nil {
			_, _, b.checkpointFailure = op.Checkpoint()
			if b.checkpointFailure == nil {
				return nil, fmt.Errorf("physical close not observed: %w", e)
			}
			return raw, nil
		}
	}
}
func TestActualHeadersReceiptCallerAbortBeforeResponseAdoptionProducesSDKAbort(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("original Darwin native HTTP1 boundary")
	}
	peerClosed := make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(peerClosed)
	}))
	defer peer.Close()
	owner := cooperative.New()
	defer owner.Close()
	caller, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		boundary := &abortAfterHeadersBoundary{Boundary: &Boundary{owner, scope}, cancel: cancel}
		events := []provider.OwnerNativeEvent{}
		driver, e := provider.NewOwnerNativeIO(boundary, func(event provider.OwnerNativeEvent) { events = append(events, event) })
		if e != nil {
			return e
		}
		body, e := provider.NewOwnerBodyIO(driver, func(kind provider.OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
			return scope.AwaitBytes(cooperative.StreamNext, work)
		})
		if e != nil {
			return e
		}
		client := provider.NewAnthropic(provider.AnthropicConfig{BaseURL: peer.URL, APIKey: "private-fake", Timeout: 10 * time.Second, HTTPExchange: &provider.RawHTTP1Exchange{}, OwnerNativeIO: driver, OwnerBodyIO: body})
		stream, failure := client.OpenMessagesStream(caller, &provider.MessagesRequest{Path: "/v1/messages", Body: []byte(`{"model":"private","messages":[],"max_tokens":8,"stream":true}`)})
		if stream != nil {
			_ = stream.Close()
			return errors.New("closed original caller became a live stream")
		}
		t.Logf("real checkpoint cause=%T %v; SDK creation=%T %v; caller=%T %v", boundary.checkpointFailure, boundary.checkpointFailure, failure, failure, context.Cause(caller), context.Cause(caller))
		if !errors.Is(boundary.checkpointFailure, net.ErrClosed) {
			return fmt.Errorf("expected actual physical closed checkpoint, got %T %v", boundary.checkpointFailure, boundary.checkpointFailure)
		}
		var sdk *provider.SDKStreamError
		if !errors.As(failure, &sdk) || sdk.Class != "APIUserAbortError" || sdk.Status != nil || sdk.Payload != nil {
			return fmt.Errorf("original caller abort lost SDK identity: %T %v", failure, failure)
		}
		closed := false
		for _, event := range events {
			if event.Kind == "failed-lease-closed" && event.Resource != nil {
				state := event.Resource
				if !state.Closed || !state.Joined || !state.OperationClosed || state.PendingReadiness {
					return fmt.Errorf("closed failed lease not joined: %+v", state)
				}
				closed = true
			}
		}
		if !closed {
			return errors.New("failed actual fetch did not dispose its native lease")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	select {
	case <-peerClosed:
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not close physical peer")
	}
}

func TestActualPinnedSDKCallerAbortCreationAndAfterCreationLifetimes(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-sdk-abort-creation.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SDK_ABORT_FIXTURE=1", "PATH=/usr/bin:/bin"}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var original struct {
		Cases []struct {
			Phase, Class, Name, Message string
			Stream                      bool
			Events                      int
			Status, Payload, Cause      any
			NativeFailure               *struct{ Constructor, Name, Message string }
		}
	}
	if e = json.Unmarshal(out, &original); e != nil || len(original.Cases) != 3 {
		t.Fatal(e, string(out))
	}
	for _, row := range original.Cases {
		t.Logf("actual Source phase%s class%s native%+v", row.Phase, row.Class, row.NativeFailure)
		if row.Phase == "after-stream-creation" {
			if !row.Stream || row.Events != 0 {
				t.Fatal("Source after-creation abort did not finish original iterator quietly", string(out))
			}
			continue
		}
		if row.Class != "APIUserAbortError" || row.Name != "Error" || row.Message != "Request was aborted." || row.Status != nil || row.Payload != nil || row.Cause != nil {
			t.Fatal("actual Source aborted-create contract", string(out))
		}
		if row.Phase == "headers-before-fetch-handoff" && (row.NativeFailure == nil || row.NativeFailure.Name != "AbortError") {
			t.Fatal("Source SDK handoff did not consume actual physical body abort", string(out))
		}
	}
}

// A close without caller cancellation is deliberately a different fact. The
// native profile guard must remain visible rather than becoming SDK user abort.
type closeAfterHeadersBoundary struct{ *Boundary }

func (b *closeAfterHeadersBoundary) AwaitHeaders(op provider.NativeOwnerOperation) ([]byte, error) {
	raw, e := b.Boundary.AwaitHeaders(op)
	if e != nil {
		return raw, e
	}
	closer, ok := op.(interface{ Close() error })
	if !ok {
		return nil, errors.New("actual reservation close interface missing")
	}
	if e = closer.Close(); e != nil {
		return nil, e
	}
	return raw, nil
}
func TestPhysicalCloseWithoutCallerAbortAndForeignOwnerKeepDistinctFailures(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("original Darwin native HTTP1 boundary")
	}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer peer.Close()
	owner := cooperative.New()
	defer owner.Close()
	if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		driver, e := provider.NewOwnerNativeIO(&closeAfterHeadersBoundary{&Boundary{owner, scope}}, nil)
		if e != nil {
			return e
		}
		client := provider.NewAnthropic(provider.AnthropicConfig{BaseURL: peer.URL, APIKey: "private", Timeout: time.Second, HTTPExchange: &provider.RawHTTP1Exchange{}, OwnerNativeIO: driver})
		stream, failure := client.OpenMessagesStream(context.Background(), &provider.MessagesRequest{Path: "/v1/messages", Body: []byte(`{}`)})
		if stream != nil {
			_ = stream.Close()
			return errors.New("closed physical resource became live")
		}
		var sdk *provider.SDKStreamError
		if !errors.Is(failure, provider.ErrHTTPExchangeProfile) || errors.As(failure, &sdk) {
			return fmt.Errorf("non-caller framing close reclassified: %T %v", failure, failure)
		}
		foreign := cooperative.New()
		defer foreign.Close()
		return foreign.Run(context.Background(), func(other *cooperative.Scope) error {
			_, e := provider.NewOwnerNativeIO(&Boundary{owner, other}, nil)
			if !provider.IsOwnerWaitFailure(e) || !errors.Is(e, cooperative.ErrRestricted) {
				return fmt.Errorf("foreign owner became SDK/network fault: %T %v", e, e)
			}
			return nil
		})
	}); e != nil {
		t.Fatal(e)
	}
}
