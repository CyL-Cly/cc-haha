package mcpconfig

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/nativepaths"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"path/filepath"
	"strings"
)

// CLI loads public --mcp-config inputs once, before discovery. File parsing,
// expansion, validation, enterprise policy and SDK exemptions are shared with
// the ordinary configuration service.
func (s *Service) CLI(ctx context.Context, inputs []string, strict bool) (protocol.JSONObject, []string, error) {
	servers := protocol.NewJSONObject()
	failures := []protocol.JSONObject{}
	for _, input := range inputs {
		input = protocol.TrimJavaScriptString(input)
		if input == "" {
			continue
		}
		raw := json.RawMessage(input)
		var parsed *protocol.JSONObject
		var issues []protocol.JSONObject
		if json.Valid(raw) && truthy(raw) {
			parsed, issues = s.Parse(raw, true, "dynamic", "command line")
		} else {
			path := input
			if s.options.Platform == "windows" || s.options.Platform == "win32" {
				path = nativepaths.ResolveWindows(input, s.options.CWD)
			} else if !filepath.IsAbs(path) {
				path = filepath.Join(s.options.CWD, path)
			}
			parsed, issues = s.ParseFile(path, true, "dynamic")
		}
		if len(issues) > 0 {
			failures = append(failures, issues...)
		} else if parsed != nil {
			servers.Assign(object(parsed.Get("mcpServers")))
		}
	}
	if len(failures) > 0 {
		texts := []string{}
		for _, failure := range failures {
			prefix := ""
			if failure.String("path") != "" {
				prefix = failure.String("path") + ": "
			}
			texts = append(texts, prefix+failure.String("message"))
		}
		return protocol.JSONObject{}, nil, errors.New("Error: Invalid MCP configuration:\n" + strings.Join(texts, "\n"))
	}
	for _, field := range servers.OwnProperties() {
		config := object(field.Value)
		if config.String("type") == "sdk" {
			continue
		}
		if mcpclient.NormalizeName(name(field)) == "claude-in-chrome" {
			return protocol.JSONObject{}, nil, errors.New(`Error: Invalid MCP configuration: "claude-in-chrome" is a reserved MCP name.`)
		}
	}
	for _, field := range servers.OwnProperties() {
		config := object(field.Value)
		if config.String("type") != "sdk" && mcpclient.NormalizeName(name(field)) == "computer-use" {
			return protocol.JSONObject{}, nil, errors.New(`Error: Invalid MCP configuration: "computer-use" is a reserved MCP name.`)
		}
	}
	allowed, blocked, err := s.FilterProcessInjection(ctx, addScope(servers, "dynamic"))
	if err != nil {
		return protocol.JSONObject{}, nil, err
	}
	warnings := []string{}
	if len(blocked) > 0 {
		noun := "server"
		if len(blocked) > 1 {
			noun = "servers"
		}
		warnings = append(warnings, "Warning: MCP "+noun+" blocked by enterprise policy: "+strings.Join(blocked, ", "))
	}
	if s.EnterpriseExists() {
		if strict {
			return protocol.JSONObject{}, warnings, errors.New("You cannot use --strict-mcp-config when an enterprise MCP config is present")
		}
		for _, field := range allowed.OwnProperties() {
			if object(field.Value).String("type") != "sdk" || object(field.Value).String("name") != "claude-vscode" {
				return protocol.JSONObject{}, warnings, errors.New("You cannot dynamically configure MCP servers when an enterprise MCP config is present")
			}
		}
	}
	return allowed, warnings, nil
}
