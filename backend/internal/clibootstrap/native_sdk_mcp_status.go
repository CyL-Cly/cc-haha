package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (m *MainPrint) nativeSDKMCPStatus(scope *cooperative.Scope) (protocol.JSONObject, error) {
	if !scope.BelongsTo(m.Owner) || m.mcpTools == nil {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	statuses, err := m.mcpTools.ServerStatuses(context.Background(), scope)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "mcpServers", Value: rawMain(statuses)}), nil
}
