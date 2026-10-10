package clibootstrap

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (m *MainPrint) MCPControl(ctx context.Context, subtype, serverName string) (protocol.JSONObject, error) {
	var response protocol.JSONObject
	receipt, err := m.Owner.Submit(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if m.closed || m.mcpTools == nil {
			return mainCap("uninitialized native MCP control")
		}
		switch subtype {
		case "mcp_reconnect", "mcp_toggle":
			request := protocol.NewJSONObject(protocol.JSONField{Name: "subtype", Value: rawMain(subtype)}, protocol.JSONField{Name: "serverName", Value: rawMain(serverName)})
			if subtype == "mcp_toggle" {
				request.Set("alreadyPersisted", rawMain(true))
			}
			result, err := m.nativeSDKMCPControl(scope, request)
			response = result.Response
			if !response.IsObject() {
				response = protocol.NewJSONObject()
			}
			return err

		case "mcp_status":
			statuses, err := m.mcpTools.ServerStatuses(ctx, scope)
			if err != nil {
				return err
			}
			data, err := json.Marshal(statuses)
			if err != nil {
				return err
			}
			response = protocol.NewJSONObject(protocol.JSONField{Name: "mcpServers", Value: data})
		default:
			return mainCap("component control " + subtype)
		}
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return response, err
}
