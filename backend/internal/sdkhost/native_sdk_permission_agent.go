package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

func sdkPermissionAgentID(call queryloop.JSONToolCallContext) string {
	if context, ok := call.Opaque.(*builtinwrite.CallContext); ok && context != nil {
		return context.AgentID
	}
	return ""
}
