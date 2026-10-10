package queryloop

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestFailureHookStageCollectsBeforeErrorAllocationAndReturnsErrorThenHooksInSerialAndConcurrent(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		for _, stage := range []string{"call", "map"} {
			t.Run(stage+map[bool]string{false: "-serial", true: "-concurrent"}[concurrent], func(t *testing.T) {
				h := newBatchHarness(t)
				h.Capabilities.JSON = jsonCapabilities()
				owner := cooperative.New()
				defer owner.Close()
				h.Tool.IsConcurrencySafe = func(protocol.JSONObject) bool { return concurrent }
				h.Tool.Call = func(context.Context, protocol.JSONObject, ToolCallContext) (ToolCallResult, error) {
					if stage == "call" {
						return ToolCallResult{}, errors.New("actual-inner-failure")
					}
					return ToolCallResult{Data: quoted("result")}, nil
				}
				if stage == "map" {
					h.Tool.MapResult = func(json.RawMessage, string) (protocol.JSONObject, error) {
						return protocol.JSONObject{}, errors.New("actual-inner-failure")
					}
				}
				calls := 0
				h.Capabilities.JSON.FailureToolEffects = func(_ context.Context, tool RegisteredTool, input *protocol.JSONObject, failure error, call JSONToolCallContext) ([]Message, error) {
					calls++
					if tool.Name != "Write" || input.String("file_path") != "original.txt" || failure.Error() != "actual-inner-failure" || string(call.ToolUseID) != `"call"` || call.Opaque != "original-context" {
						t.Fatal("failure lost actual call context", tool.Name, input, failure, call)
					}
					return []Message{{Record: h.Factory.CreateAttachment([]byte(`{"type":"hook_additional_context","content":["failure-context"],"hookName":"PostToolUseFailure:Write","toolUseID":"call","hookEvent":"PostToolUseFailure"}`))}}, nil
				}
				out := []Message{}
				err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
					h.Capabilities.Owner, h.Capabilities.Scope = owner, scope
					if concurrent {
						h.Tool.StartCallJSON = func(ctx context.Context, input protocol.JSONObject, call JSONToolCallContext) (*ownerasync.OwnerFuture[ToolCallResult], error) {
							f, err := ownerasync.NewOwnerFuture[ToolCallResult](owner)
							if err != nil {
								return nil, err
							}
							if stage == "call" {
								err = f.Reject(scope, errors.New("actual-inner-failure"))
							} else {
								err = f.Resolve(scope, ToolCallResult{Data: quoted("result")})
							}
							return f, err
						}
					}
					batch, err := NewToolBatch([]RegisteredTool{h.Tool}, h.Capabilities, h.Factory)
					if err != nil {
						return err
					}
					_, err = batch.Execute(context.Background(), Round{ToolContext: "original-context"}, h.Uses, func(message Message) error { out = append(out, message); return nil })
					return err
				})
				if err != nil {
					t.Fatal(err)
				}
				if calls != 1 || len(out) != 2 || out[0].Type() != "user" || out[1].Type() != "attachment" || out[0].Record.String("uuid") != "2" || out[1].Record.String("uuid") != "1" || !strings.Contains(string(out[0].Record.Get("message")), "actual-inner-failure") {
					t.Fatalf("Source allocation/emission order calls=%d outputs=%v", calls, out)
				}
			})
		}
	}
}
