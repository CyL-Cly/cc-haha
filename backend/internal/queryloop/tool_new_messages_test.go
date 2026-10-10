package queryloop

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"reflect"
	"testing"
)

func TestToolProvidedMessagesFollowResultBeforeRemainingHookOutputs(t *testing.T) {
	h := newBatchHarness(t)
	h.Tool.Call = func(context.Context, protocol.JSONObject, ToolCallContext) (ToolCallResult, error) {
		return ToolCallResult{Data: quoted("ok"), NewMessages: []Message{{Record: fixtureObject(`{"type":"user","message":{"content":"skill prompt"},"isMeta":true}`)}, {Record: fixtureObject(`{"type":"attachment","attachment":{"type":"command_permissions"}}`)}}}, nil
	}
	h.Capabilities.PostTool = func(context.Context, RegisteredTool, *protocol.JSONObject, ToolCallResult, ToolCallContext) ([]Message, error) {
		return []Message{{Record: fixtureObject(`{"type":"progress","data":{"type":"hook"}}`)}}, nil
	}
	kinds := []string{}
	h.Emit = func(message Message) error { kinds = append(kinds, message.Type()); return nil }
	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(kinds, []string{"user", "user", "attachment", "progress"}) {
		t.Fatal("tool result/newMessages/hooks Source order changed", kinds)
	}
}
func TestToolProvidedMessageConsumerFailureStopsFollowingHooks(t *testing.T) {
	h := newBatchHarness(t)
	sentinel := errors.New("newMessages consumer stopped")
	h.Tool.Call = func(context.Context, protocol.JSONObject, ToolCallContext) (ToolCallResult, error) {
		return ToolCallResult{Data: quoted("ok"), NewMessages: []Message{{Record: fixtureObject(`{"type":"attachment"}`)}}}, nil
	}
	delivered := 0
	h.Emit = func(message Message) error {
		delivered++
		if message.Type() == "attachment" {
			return sentinel
		}
		return nil
	}
	if _, err := h.run(); err != sentinel {
		t.Fatal(err)
	}
	if delivered != 2 {
		t.Fatal("execution continued after failed message consumer", delivered)
	}
}
