package errorlog

import (
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// MCP diagnostics use their own Source JSONL channel. They do not pass through
// logError's telemetry/privacy gate or enter its in-memory recent-error list.
func (s *NativeSink) MCPDebug(serverName, message string) error {
	return s.writeMCP(serverName, "debug", message, "MCP server \""+serverName+"\": "+message, "DEBUG")
}
func (s *NativeSink) MCPErrorMessage(serverName, message string) error {
	return s.writeMCP(serverName, "error", message, "MCP server \""+serverName+"\" "+message, "ERROR")
}
func (s *NativeSink) MCPError(serverName string, failure error) error {
	detail := Details(failure)
	name := detail.Name
	if name == "" {
		name = "Error"
	}
	return s.writeMCP(serverName, "error", Text(failure), "MCP server \""+serverName+"\" "+name+": "+detail.Message, "ERROR")
}
func (s *NativeSink) writeMCP(serverName, field, value, debug, level string) error {
	if err := s.debugLog(debug, level); err != nil {
		return err
	}
	m := s.options.Metadata()
	row := protocol.NewJSONObject(protocol.JSONField{Name: field, Value: quoted(value)}, protocol.JSONField{Name: "timestamp", Value: quoted(timestamp(s.options.Now()))}, protocol.JSONField{Name: "sessionId", Value: quoted(m.SessionID)}, protocol.JSONField{Name: "cwd", Value: quoted(m.CWD)})
	data, err := row.MarshalJSON()
	if err != nil {
		return err
	}
	base := m.Environment["CLAUDE_CONFIG_DIR"]
	if base != "" {
		base = filepath.Join(base, "Cache")
	} else {
		switch m.Platform {
		case "darwin":
			base = filepath.Join(m.Home, "Library", "Caches", "claude-cli-nodejs")
		case "win32", "windows":
			base = m.Environment["LOCALAPPDATA"]
			if base == "" {
				base = filepath.Join(m.Home, "AppData", "Local")
			}
			base = filepath.Join(base, "claude-cli-nodejs", "Cache")
		default:
			base = m.Environment["XDG_CACHE_HOME"]
			if base == "" {
				base = filepath.Join(m.Home, ".cache")
			}
			base = filepath.Join(base, "claude-cli-nodejs")
		}
	}
	return s.write(filepath.Join(base, sanitize(m.CWD), "mcp-logs-"+sanitize(serverName), s.date+".jsonl"), string(data)+"\n", false, 50, false)
}
