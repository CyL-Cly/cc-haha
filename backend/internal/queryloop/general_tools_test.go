package queryloop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestGeneralToolBatchEmitsActualFailuresAndContinues(t *testing.T) {
	for _, kind := range []string{"schema", "validation", "deny", "call", "backfill", "pre", "permission", "map", "post"} {
		t.Run(kind, func(t *testing.T) {
			h := newBatchHarness(t)
			h.Uses = append(h.Uses, h.Uses[0])
			h.Calls = 0
			failed := false
			switch kind {
			case "schema":
				first := *h.Uses[0].Block
				first.Object = first.Object.Clone()
				first.Object.Set("input", []byte(`{"file_path":"bad","content":"text"}`))
				h.Uses[0].Block = &first
				h.Tool.ParseInput = func(in protocol.JSONObject) (protocol.JSONObject, error) {
					if in.String("file_path") == "bad" {
						return protocol.JSONObject{}, errors.New("bad input")
					}
					return in, nil
				}
			case "validation":
				h.Tool.ValidateInput = func(context.Context, protocol.JSONObject, any) error {
					if !failed {
						failed = true
						return errors.New("stale file")
					}
					return nil
				}
			case "deny":
				h.Capabilities.ResolvePermission = func(_ context.Context, _ RegisteredTool, in *protocol.JSONObject, _ ToolCallContext) (ToolPermission, error) {
					if !failed {
						failed = true
						return ToolPermission{Behavior: "deny", Input: in, Message: quoted("actual permission rejection")}, nil
					}
					return ToolPermission{Behavior: "allow", Input: in}, nil
				}
			case "call":
				original := h.Tool.Call
				h.Tool.Call = func(ctx context.Context, in protocol.JSONObject, c ToolCallContext) (ToolCallResult, error) {
					if !failed {
						failed = true
						return ToolCallResult{}, errors.New("disk failure")
					}
					return original(ctx, in, c)
				}
			case "backfill":
				h.Tool.Backfill = func(*protocol.JSONObject) error {
					if !failed {
						failed = true
						return errors.New("backfill failure")
					}
					return nil
				}
			case "pre":
				h.Capabilities.PreTool = func(_ context.Context, _ RegisteredTool, in *protocol.JSONObject, _ ToolCallContext) (*protocol.JSONObject, error) {
					if !failed {
						failed = true
						return nil, errors.New("pre failure")
					}
					return in, nil
				}
			case "permission":
				h.Capabilities.ResolvePermission = func(_ context.Context, _ RegisteredTool, in *protocol.JSONObject, _ ToolCallContext) (ToolPermission, error) {
					if !failed {
						failed = true
						return ToolPermission{}, errors.New("permission failure")
					}
					return ToolPermission{Behavior: "allow", Input: in}, nil
				}
			case "map":
				original := h.Tool.MapResult
				h.Tool.MapResult = func(data json.RawMessage, id string) (protocol.JSONObject, error) {
					if !failed {
						failed = true
						return protocol.JSONObject{}, errors.New("map failure")
					}
					return original(data, id)
				}
			case "post":
				h.Capabilities.PostTool = func(context.Context, RegisteredTool, *protocol.JSONObject, ToolCallResult, ToolCallContext) ([]Message, error) {
					if !failed {
						failed = true
						return nil, errors.New("post failure")
					}
					return nil, nil
				}
			}
			batch, e := NewToolBatch([]RegisteredTool{h.Tool}, h.Capabilities, h.Factory)
			if e != nil {
				t.Fatal(e)
			}
			out := []Message{}
			_, e = batch.Execute(context.Background(), Round{}, h.Uses, func(m Message) error { out = append(out, m); return nil })
			if e != nil {
				t.Fatal(e)
			}
			if len(out) != 2 {
				t.Fatalf("outputs %d", len(out))
			}
			first, _ := out[0].MarshalJSON()
			last, _ := out[1].MarshalJSON()
			wrapped := strings.Contains(string(first), "tool_use_error")
			inner := kind == "call" || kind == "map" || kind == "post"
			if !strings.Contains(string(first), `"is_error":true`) || wrapped != (kind != "deny" && !inner) || strings.Contains(string(last), `"is_error":true`) {
				t.Fatalf("feedback %s then %s", first, last)
			}
			prefix := "Error calling tool (Write): "
			if inner {
				prefix = "Error: "
			}
			if kind != "schema" && kind != "validation" && kind != "deny" && !strings.HasPrefix(out[0].Record.String("toolUseResult"), prefix) {
				t.Fatalf("outer error receipt changed: %s", out[0].Record.Get("toolUseResult"))
			}
		})
	}
}

func TestPermissionFeedbackPreservesActualMessageAndUndefined(t *testing.T) {
	for _, message := range [][]byte{nil, []byte(`"file denied"`), []byte(`"denied \ud800"`)} {
		h := newBatchHarness(t)
		var result Message
		batch, e := NewToolBatch([]RegisteredTool{h.Tool}, h.Capabilities, h.Factory)
		if e != nil {
			t.Fatal(e)
		}
		if e = batch.permissionFeedback(h.Uses[0], quoted("call"), ToolPermission{Behavior: "deny", Message: message}, func(m Message) error { result = m; return nil }); e != nil {
			t.Fatal(e)
		}
		var api protocol.JSONObject
		if e = api.UnmarshalJSON(result.Record.Get("message")); e != nil {
			t.Fatal(e)
		}
		var blocks []protocol.JSONObject
		if e = json.Unmarshal(api.Get("content"), &blocks); e != nil {
			t.Fatal(e)
		}
		if !bytes.Equal(blocks[0].Get("content"), message) || bytes.Contains(blocks[0].Get("content"), []byte("tool_use_error")) {
			t.Fatalf("denial message changed %s", api.Get("content"))
		}
		want, _ := protocol.JavaScriptStringJSON(message)
		want, _ = protocol.JoinJavaScriptValuesJSON([]json.RawMessage{quoted("Error: "), want}, "")
		if !bytes.Equal(result.Record.Get("toolUseResult"), want) {
			t.Fatalf("denial receipt %s want %s", result.Record.Get("toolUseResult"), want)
		}
	}
}
func TestGeneralReadSingleInvocationRequiresAsyncOwnerAndKeepsFiniteBoundary(t *testing.T) {
	h := newBatchHarness(t)
	h.Tool.Name = "Read"
	h.Uses[0].Block.Object.Set("name", quoted("Read"))
	h.Tool.IsConcurrencySafe = func(protocol.JSONObject) bool { return true }
	general, e := NewToolBatch([]RegisteredTool{h.Tool}, h.Capabilities, h.Factory)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = general.Execute(context.Background(), Round{}, h.Uses, h.Emit); e == nil || h.Calls != 0 {
		t.Fatalf("single readonly call silently used serial fallback: %v calls=%d", e, h.Calls)
	}
	legacy, _ := NewSerialToolBatch([]RegisteredTool{h.Tool}, h.Capabilities, h.Factory)
	if _, e = legacy.Execute(context.Background(), Round{}, h.Uses, h.Emit); e == nil {
		t.Fatal("legacy profile was silently broadened")
	}
}

func TestGeneralNonObjectToolInputEmitsSchemaFeedbackBeforeNextActualTool(t *testing.T) {
	for _, input := range []string{`[]`, `"string"`, `3`, `false`, `null`} {
		t.Run(input, func(t *testing.T) {
			h := newBatchHarness(t)
			bad := h.Uses[0]
			block := *bad.Block
			block.Object = block.Object.Clone()
			block.Object.Set("input", []byte(input))
			bad.Block = &block
			uses := append([]Invocation{bad}, h.Uses...)
			batch, err := NewToolBatch([]RegisteredTool{h.Tool}, h.Capabilities, h.Factory)
			if err != nil {
				t.Fatal(err)
			}
			messages := []Message{}
			_, err = batch.Execute(context.Background(), Round{}, uses, func(message Message) error { messages = append(messages, message); return nil })
			if err != nil || h.Calls != 1 || len(messages) != 2 {
				t.Fatalf("invalid shape stopped actual next tool: %v calls=%d messages=%d", err, h.Calls, len(messages))
			}
			data, _ := messages[0].MarshalJSON()
			if !bytes.Contains(data, []byte(`"is_error":true`)) || !bytes.Contains(data, []byte("expected as `object`")) || messages[0].Record.String("sourceToolAssistantUUID") != "original-assistant" {
				t.Fatalf("actual Source object schema feedback lost: %s", data)
			}
			finite, _ := NewSerialToolBatch([]RegisteredTool{h.Tool}, h.Capabilities, h.Factory)
			if _, err = finite.Execute(context.Background(), Round{}, []Invocation{bad}, h.Emit); err == nil {
				t.Fatal("old finite shape refusal changed")
			}
		})
	}
}
