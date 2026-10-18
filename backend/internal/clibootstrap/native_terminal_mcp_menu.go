package clibootstrap

import (
	"context"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (m *MainPrint) TerminalMCPMenuSnapshot(ctx context.Context) (protocol.JSONObject, error) {
	var result protocol.JSONObject
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if m.closed || m.mcpTools == nil {
			return cooperative.ErrClosed
		}
		servers, err := m.mcpTools.TerminalCatalogue(ctx, scope)
		if err != nil {
			return err
		}
		for i := range servers {
			config := mainObj(servers[i].Get("config"))
			servers[i].Set("usesIdentityProvider", rawMain(string(mainObj(config.Get("oauth")).Get("xaa")) == "true"))
			config.Delete("headers")
			config.Delete("oauth")
			servers[i].Set("config", rawMain(config))
			location := servers[i].String("scope")
			switch location {
			case "user":
				location = m.Global.Path()
			case "project":
				location = filepath.Join(m.options.Native.CWD, ".mcp.json")
			case "local":
				location = m.Global.Path() + " [project: " + m.options.Native.CWD + "]"
			case "dynamic":
				location = "Dynamically configured"
			case "enterprise":
				location = filepath.Join(m.options.Native.ManagedRoot, "managed-mcp.json")
			case "claudeai":
				location = "claude.ai"
			}
			servers[i].Set("configLocation", quoteMain(location))
		}
		agentServers, err := m.terminalAgentMCPServers(scope)
		if err != nil {
			return err
		}
		result = protocol.NewJSONObject(protocol.JSONField{Name: "servers", Value: rawMain(servers)}, protocol.JSONField{Name: "agentServers", Value: rawMain(agentServers)})
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return result, err
}
