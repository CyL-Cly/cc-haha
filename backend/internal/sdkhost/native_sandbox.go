package sdkhost

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

func (h *Host) StartNativeSandboxNetwork(ctx context.Context, scope *cooperative.Scope, host string) (*OwnerFuture[bool], error) {
	if scope == nil || !scope.BelongsTo(h.options.Owner) || h.options.Print == nil || !h.options.Print.NativeProcess {
		return nil, cooperative.ErrRestricted
	}
	start := h.options.Print.StartSandboxNetworkPermission
	if start == nil {
		return h.startSDKNativeSandboxPermission(ctx, scope, host)
	}
	quote := func(v string) json.RawMessage { raw, _ := json.Marshal(v); return raw }
	input := protocol.NewJSONObject(protocol.JSONField{Name: "host", Value: quote(host)})
	decision := queryloop.ToolPermission{Behavior: "ask", Input: &input, Description: quote("Allow network connection to " + host + "?")}
	return start(ctx, scope, queryloop.RegisteredTool{Name: "SandboxNetworkAccess"}, &input, queryloop.JSONToolCallContext{Opaque: &builtinwrite.CallContext{Scope: scope}, ToolUseID: quote(h.options.UUID()), PermDecision: &decision})
}
