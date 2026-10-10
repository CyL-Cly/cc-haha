// Package plugincli retains the ordinary CLI projection over the shared
// installed registry and loader. Desktop summaries are a separate boundary.
package plugincli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type ListSnapshot struct {
	Installed protocol.JSONObject
	Enabled   map[string]bool
	Loaded    pluginregistry.Result
}

type ListOptions struct {
	LoadMCP func(context.Context, pluginregistry.Loaded) (*protocol.JSONObject, error)
}

func raw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func object(v json.RawMessage) protocol.JSONObject {
	var o protocol.JSONObject
	_ = o.UnmarshalJSON(v)
	return o
}
func array(v json.RawMessage) []json.RawMessage {
	var out []json.RawMessage
	_ = json.Unmarshal(v, &out)
	return out
}
func record(fields ...protocol.JSONField) protocol.JSONObject {
	return protocol.NewJSONObject(fields...)
}
func field(name string, v any) protocol.JSONField {
	return protocol.JSONField{Name: name, Value: raw(v)}
}

func inlineError(e protocol.JSONObject) bool {
	return strings.HasSuffix(e.String("source"), "@inline") || strings.HasPrefix(e.String("source"), "inline[")
}
func errorsFor(errors []protocol.JSONObject, id, name string, inline bool) []string {
	result := []string{}
	for _, e := range errors {
		if inline && !inlineError(e) {
			continue
		}
		if e.String("source") == id || e.Get("plugin") != nil && e.String("plugin") == name {
			result = append(result, pluginsapi.ErrorMessage(e))
		}
	}
	return result
}

// InstalledJSON includes every installation, rather than picking one current
// desktop scope. Inline failures remain visible even without a loaded plugin.
func InstalledJSON(ctx context.Context, snapshot ListSnapshot, options ListOptions) ([]protocol.JSONObject, error) {
	if options.LoadMCP == nil {
		return nil, errors.New("ordinary CLI plugin list requires actual MCP component loader")
	}
	installed := object(snapshot.Installed.Get("plugins"))
	ids := []string{}
	for _, p := range installed.OwnProperties() {
		var id string
		if err := json.Unmarshal(p.NameJSON, &id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return compareUTF16(ids[i], ids[j]) < 0 })
	loaded := append(append([]pluginregistry.Loaded{}, snapshot.Loaded.Enabled...), snapshot.Loaded.Disabled...)
	byID := map[string]pluginregistry.Loaded{}
	for _, p := range loaded {
		byID[p.Source] = p
	}
	servers := func(p pluginregistry.Loaded) (*protocol.JSONObject, error) {
		if p.MCPServers != nil {
			return p.MCPServers, nil
		}
		return options.LoadMCP(ctx, p)
	}
	rows := []protocol.JSONObject{}
	for _, id := range ids {
		entries := array(installed.Get(id))
		if len(entries) == 0 {
			continue
		}
		name := strings.Split(id, "@")[0]
		issues := errorsFor(snapshot.Loaded.Errors, id, name, false)
		for _, entry := range entries {
			v := object(entry)
			version := v.Get("version")
			if !truthy(version) {
				version = raw("unknown")
			}
			row := record(field("id", id), protocol.JSONField{Name: "version", Value: version}, protocol.JSONField{Name: "scope", Value: v.Get("scope")}, field("enabled", snapshot.Enabled[id]), protocol.JSONField{Name: "installPath", Value: v.Get("installPath")})
			for _, key := range []string{"installedAt", "lastUpdated", "projectPath"} {
				if value := v.Get(key); value != nil {
					row.Set(key, value)
				}
			}
			if p, ok := byID[id]; ok {
				mcp, err := servers(p)
				if err != nil {
					return nil, err
				}
				if mcp != nil && len(mcp.OwnProperties()) != 0 {
					row.Set("mcpServers", raw(*mcp))
				}
			}
			if len(issues) != 0 {
				row.Set("errors", raw(issues))
			}
			rows = append(rows, row)
		}
	}
	for _, p := range loaded {
		if !strings.HasSuffix(p.Source, "@inline") {
			continue
		}
		mcp, err := servers(p)
		if err != nil {
			return nil, err
		}
		version := p.Manifest.Get("version")
		if version == nil || string(version) == "null" {
			version = raw("unknown")
		}
		row := record(field("id", p.Source), protocol.JSONField{Name: "version", Value: version}, field("scope", "session"), field("enabled", p.Enabled), field("installPath", p.Path))
		if mcp != nil && len(mcp.OwnProperties()) != 0 {
			row.Set("mcpServers", raw(*mcp))
		}
		if issues := errorsFor(snapshot.Loaded.Errors, p.Source, p.Name, true); len(issues) != 0 {
			row.Set("errors", raw(issues))
		}
		rows = append(rows, row)
	}
	for _, e := range snapshot.Loaded.Errors {
		if !strings.HasPrefix(e.String("source"), "inline[") {
			continue
		}
		path := e.Get("path")
		if path == nil {
			path = raw("")
		}
		rows = append(rows, record(field("id", e.String("source")), field("version", "unknown"), field("scope", "session"), field("enabled", false), protocol.JSONField{Name: "installPath", Value: path}, field("errors", []string{pluginsapi.ErrorMessage(e)})))
	}
	return rows, nil
}

func compareUTF16(a, b string) int {
	x, y := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(x) && i < len(y); i++ {
		if x[i] < y[i] {
			return -1
		}
		if x[i] > y[i] {
			return 1
		}
	}
	if len(x) < len(y) {
		return -1
	}
	if len(x) > len(y) {
		return 1
	}
	return 0
}
func truthy(v json.RawMessage) bool {
	if len(v) == 0 {
		return false
	}
	var data any
	d := json.NewDecoder(bytes.NewReader(v))
	d.UseNumber()
	if d.Decode(&data) != nil {
		return false
	}
	switch x := data.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case json.Number:
		n, _ := x.Float64()
		return n != 0
	default:
		return true
	}
}
