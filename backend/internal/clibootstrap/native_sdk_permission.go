package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

func (m *MainPrint) nativeSDKPermissionResolver(ctx context.Context, scope *cooperative.Scope, tool queryloop.RegisteredTool, input *protocol.JSONObject, call queryloop.JSONToolCallContext) (queryloop.ToolPermission, error) {
	if m.Host == nil {
		return queryloop.ToolPermission{}, cooperative.ErrRestricted
	}
	return m.Host.ResolveNativeSDKPermission(ctx, scope, tool, input, call)
}
