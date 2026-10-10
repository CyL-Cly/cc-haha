package sdkhost

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeSDKMCPCallerCancellationPreservesControlProducerUntilReplyAndOwnerClose(t *testing.T) {
	for _, action := range []string{"peer-reply", "EOF", "OwnerClose"} {
		t.Run(action, func(t *testing.T) {
			owner := cooperative.New()
			defer owner.Close()
			state, e := NewNativeSDKMCPState(owner)
			if e != nil {
				t.Fatal(e)
			}
			ctx, finish := context.WithTimeout(context.Background(), 3*time.Second)
			defer finish()
			var future *OwnerFuture[json.RawMessage]
			sent := 0
			e = owner.Run(ctx, func(scope *cooperative.Scope) error {
				if e := state.bind(scope, func(_ *cooperative.Scope, frame protocol.JSONObject) error {
					request, _ := frame.Object("request")
					if request.String("subtype") != "mcp_message" {
						t.Fatal("not actual MCP control", frame)
					}
					sent++
					return nil
				}, func() string { return "held-mcp" }); e != nil {
					return e
				}
				var e error
				future, e = state.Send(scope, "peer", protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: quote("2.0")}, protocol.JSONField{Name: "id", Value: []byte(`1`)}, protocol.JSONField{Name: "method", Value: quote("tools/call")}))
				return e
			})
			if e != nil {
				t.Fatal(e)
			}
			cancelled, cancel := context.WithCancel(context.Background())
			cancel()
			if _, e = future.Wait(cancelled); !errors.Is(e, context.Canceled) {
				t.Fatal("caller cancellation", e)
			}
			e = owner.Run(ctx, func(scope *cooperative.Scope) error {
				if len(state.pending) != 1 || sent != 1 {
					t.Fatal("caller destroyed Source no-AbortSignal producer", len(state.pending), sent)
				}
				return nil
			})
			if e != nil {
				t.Fatal(e)
			}
			switch action {
			case "peer-reply":
				e = owner.Run(ctx, func(scope *cooperative.Scope) error {
					handled, e := state.response(scope, protocol.NewJSONObject(protocol.JSONField{Name: "response", Value: raw(map[string]any{"subtype": "success", "request_id": "held-mcp", "response": map[string]any{"mcp_response": map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"content": []any{}}}}})}))
					if !handled && e == nil {
						t.Fatal("actual response not consumed")
					}
					return e
				})
				if e != nil {
					t.Fatal(e)
				}
				value, e := future.Wait(ctx)
				if e != nil || string(value) != `{"id":1,"jsonrpc":"2.0","result":{"content":[]}}` {
					t.Fatal("late response original future", string(value), e)
				}
			case "EOF":
				e = owner.Run(ctx, state.close)
				if e != nil {
					t.Fatal(e)
				}
				_, e = future.Wait(ctx)
				if e == nil || e.Error() != "Tool permission stream closed before response received" {
					t.Fatal("Source EOF", e)
				}
			case "OwnerClose":
				owner.Close()
				_, e = future.Wait(ctx)
				if !errors.Is(e, cooperative.ErrClosed) {
					t.Fatal("closed canonical owner did not settle", e)
				}
			}
		})
	}
}
