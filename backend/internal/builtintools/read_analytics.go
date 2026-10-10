package builtintools

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

type readAnalyticsKey struct{}

func readAnalyticsContext(ctx context.Context, call queryloop.JSONToolCallContext) context.Context {
	if call.Invocation.Assistant != nil {
		var message protocol.JSONObject
		_ = json.Unmarshal(call.Invocation.Assistant.Record.Get("message"), &message)
		if id := message.String("id"); id != "" {
			return context.WithValue(ctx, readAnalyticsKey{}, id)
		}
	}
	return ctx
}
func readAnalyticsMessageID(ctx context.Context) *string {
	if id, ok := ctx.Value(readAnalyticsKey{}).(string); ok {
		return &id
	}
	return nil
}
