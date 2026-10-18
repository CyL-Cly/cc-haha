package mcpconfig

import (
	"context"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Diagnostics is McpParsingWarnings' one-mount scoped read. It deliberately
// does not use AllConfigurations: managed precedence, approval and connected
// peer status cannot hide a malformed file from this diagnostic consumer.
func (s *Service) Diagnostics(ctx context.Context, globalFile string) ([]protocol.JSONObject, error) {
	rows := make([]protocol.JSONObject, 0, 4)
	for _, scope := range []string{"user", "project", "local", "enterprise"} {
		config, err := s.Scope(ctx, scope)
		if err != nil {
			return nil, err
		}
		errors, warnings := []protocol.JSONObject{}, []protocol.JSONObject{}
		for _, failure := range config.Errors {
			switch object(failure.Get("mcpErrorMetadata")).String("severity") {
			case "fatal":
				errors = append(errors, failure.Clone())
			case "warning":
				warnings = append(warnings, failure.Clone())
			}
		}
		label, path := "", ""
		switch scope {
		case "user":
			label, path = "User config (available in all your projects)", globalFile
		case "project":
			label, path = "Project config (shared via .mcp.json)", filepath.Join(s.options.CWD, ".mcp.json")
		case "local":
			label, path = "Local config (private to you in this project)", globalFile+" [project: "+s.options.CWD+"]"
		case "enterprise":
			label, path = "Enterprise config (managed by your organization)", filepath.Join(s.options.ManagedRoot, "managed-mcp.json")
		}
		rows = append(rows, protocol.NewJSONObject(protocol.JSONField{Name: "scope", Value: quote(scope)}, protocol.JSONField{Name: "path", Value: quote(path)}, protocol.JSONField{Name: "label", Value: quote(label)}, protocol.JSONField{Name: "errors", Value: encode(errors)}, protocol.JSONField{Name: "warnings", Value: encode(warnings)}))
	}
	return rows, nil
}
