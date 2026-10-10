package modelyield

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/apipresentation"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/diagnostics"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/runtimecleanup"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionactivity"
)

func TestModelActivityCreationEnteredStopAndResourceAccountingOrder(t *testing.T) {
	for _, mode := range []string{"before-prepared", "creation-error", "normal", "consumer-return", "consumer-error"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if mode == "creation-error" {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(500)
					_, _ = io.WriteString(w, `{"message":"original creation failure"}`)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Request-Id", "original-activity-request")
				_, _ = io.WriteString(w, queryWire("normal"))
			}))
			defer server.Close()
			owner := cooperative.New()
			defer owner.Close()
			registry, _ := runtimecleanup.New(owner)
			entries := []string{}
			nowCalls := 0
			now := 1000.0
			base := filepath.Join(t.TempDir(), "diag")
			process, e := diagnostics.NewProcess(diagnostics.Options{Environment: func(string) string { return base }, Now: func() time.Time { return time.UnixMilli(1700000000123) }, NowMillis: func() int64 { nowCalls++; return int64(now) }, PID: 1, Platform: "darwin", Files: diagnostics.OSFileSystem{}, Stringify: func(v protocol.JSONObject) ([]byte, error) {
				b, e := diagnostics.StringifyEntry(v)
				entries = append(entries, string(v.Get("data")))
				return b, e
			}})
			if e != nil {
				t.Fatal(e)
			}
			tracker, e := sessionactivity.NewCallbackNull(owner, registry, process)
			if e != nil {
				t.Fatal(e)
			}
			original := errors.New("original consumer/prepared failure")
			opened, finalized := 0, 0
			var owned *provider.OwnedMessageStream
			var cleanup *runtimecleanup.Future
			e = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				if e := tracker.Start(scope, sessionactivity.ToolExec); e != nil {
					return e
				}
				input := queuedInput(server.URL)
				deps := Dependencies{Activity: tracker, Request: requestretry.Dependencies{StreamOwner: owner, StreamScope: scope, UUID: func() string { return "original-activity-id" }, Now: func() time.Time { return time.UnixMilli(1700000000123) }, Random: func() float64 { return 0 }, Sleep: func(context.Context, float64) error { return errors.New("unexpected retry sleep") }}, Emit: func(v Yield) (requestretry.NoticeAction, error) {
					if mode == "consumer-error" {
						return 0, original
					}
					if mode == "consumer-return" {
						return requestretry.Stop, nil
					}
					return requestretry.Resume, nil
				}}
				if mode == "before-prepared" {
					deps.Request.OnPrepared = func([]protocol.JSONObject) error { return original }
				}
				life := &producerLifecycle{opened: func(s *provider.OwnedMessageStream) { opened++; owned = s }, feed: func([]byte) error { return nil }, finally: func(bool, *string) error {
					finalized++
					aborted, e := owned.SDKAborted(scope)
					if e != nil {
						return e
					}
					if !aborted {
						return errors.New("accounting executed before stream resource abort")
					}
					var err error
					cleanup, err = registry.Start(scope)
					return err
				}}
				outcome, failure := runOwnedLifecycle(context.Background(), input, deps, owner, scope, life)
				if mode == "before-prepared" || mode == "consumer-error" {
					if failure != original {
						return errors.New("original error identity replaced")
					}
				} else if mode == "creation-error" {
					var capability *apipresentation.CapabilityError
					if !errors.As(failure, &capability) || capability.Boundary != "typed no-status SDK creation error" {
						return errors.New("existing status-error presentation capability changed")
					}
				} else if failure != nil {
					return failure
				}
				if mode == "consumer-return" && outcome.Reason != "consumer_stopped" {
					return errors.New("original consumer return changed")
				}
				if cleanup == nil {
					var e error
					cleanup, e = registry.Start(scope)
					if e != nil {
						return e
					}
				}
				_, e = cleanup.Await(scope)
				return e
			})
			if e != nil {
				t.Fatal(e)
			}
			expected := `{"refcount":1,"active":{"tool_exec":1},"oldest_activity_ms":0}`
			switch mode {
			case "before-prepared":
				if opened != 0 || finalized != 0 {
					t.Fatal(opened, finalized)
				}
			case "creation-error":
				expected = `{"refcount":0,"active":{"tool_exec":1},"oldest_activity_ms":null}`
				if opened != 0 || finalized != 0 {
					t.Fatal(opened, finalized)
				}
			default:
				if opened != 1 || finalized != 1 {
					t.Fatal(opened, finalized)
				}
			}
			if len(entries) != 1 || entries[0] != expected {
				t.Fatalf("%s wanted %s got %v", mode, expected, entries)
			}
			calls := 2
			if mode == "creation-error" {
				calls = 1
			}
			if nowCalls != calls {
				t.Fatal("extra/missing elapsed clock", nowCalls, calls)
			}
		})
	}
}
func TestModelActivityRejectsForeignOwnerBeforeStartingBusiness(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	other := cooperative.New()
	defer other.Close()
	registry, _ := runtimecleanup.New(other)
	process, e := diagnostics.NewProcess(diagnostics.Options{Environment: func(string) string { return "" }, Now: time.Now, NowMillis: func() int64 { return 0 }, PID: 1, Platform: "darwin", Files: diagnostics.OSFileSystem{}})
	if e != nil {
		t.Fatal(e)
	}
	tracker, _ := sessionactivity.NewCallbackNull(other, registry, process)
	e = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, e := Run(context.Background(), queuedInput("http://127.0.0.1:1"), Dependencies{Activity: tracker, Request: requestretry.Dependencies{StreamOwner: owner, StreamScope: scope}})
		var capability *CapabilityError
		if !errors.As(e, &capability) {
			return errors.New("foreign tracker admitted")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}
