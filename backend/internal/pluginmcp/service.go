// Package pluginmcp supplies native plugin MCP configurations to every runtime
// consumer. Configurations retain unresolved cache data and are re-expanded
// against the shared settings and secure-storage producers on activation.
package pluginmcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginoptions"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

type MCPBResult struct {
	NeedsConfig                bool
	Manifest, Config           protocol.JSONObject
	ExtractedPath, ContentHash string
}
type MCPBLoader func(context.Context, string, string, string, func(string)) (MCPBResult, error)
type Options struct {
	Home, ConfigDir, CWD, Platform string
	Environment                    func(string) (string, bool)
	Cowork                         bool
	PluginOptions                  *pluginoptions.Service
	Secure                         *securestorage.Store
	Settings                       func(context.Context) (protocol.JSONObject, error)
	WriteSettings                  func(context.Context, clisettings.Patch) error
	LoadMCPB                       MCPBLoader
	OnDebug                        func(string)
}
type Service struct {
	options Options
	writeMu sync.Mutex
}

func New(o Options) (*Service, error) {
	if !filepath.IsAbs(o.Home) || !filepath.IsAbs(o.ConfigDir) || !filepath.IsAbs(o.CWD) || o.Platform == "" || o.Environment == nil || o.PluginOptions == nil || o.Secure == nil || o.Settings == nil || o.WriteSettings == nil || o.LoadMCPB == nil {
		return nil, errors.New("plugin MCP requires explicit paths/environment and actual shared options, settings, secure-storage and MCPB loader")
	}

	return &Service{options: o}, nil
}
func object(data json.RawMessage) protocol.JSONObject {
	var o protocol.JSONObject
	_ = o.UnmarshalJSON(data)
	if !o.IsObject() {
		return protocol.NewJSONObject()
	}
	return o
}
func raw(value any) json.RawMessage { data, _ := json.Marshal(value); return data }
func record(fields ...protocol.JSONField) protocol.JSONObject {
	return protocol.NewJSONObject(fields...)
}
func field(name string, value any) protocol.JSONField {
	return protocol.JSONField{Name: name, Value: raw(value)}
}
func truthy(value json.RawMessage) bool {
	var v any
	if json.Unmarshal(value, &v) != nil {
		return false
	}
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return x != ""
	case bool:
		return x
	case float64:
		return x != 0
	}
	return true
}
func stringValue(value json.RawMessage) string {
	var text string
	_ = json.Unmarshal(value, &text)
	return text
}
func properties(o protocol.JSONObject) []protocol.JSONProperty { return o.OwnProperties() }
func name(p protocol.JSONProperty) string                      { return stringValue(p.NameJSON) }
func appendError(list *[]protocol.JSONObject, value protocol.JSONObject) {
	if list != nil {
		*list = append(*list, value)
	}
}
func (s *Service) debug(message string) {
	if s.options.OnDebug != nil {
		s.options.OnDebug(message)
	}
}
func envTruthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func (s *Service) environment(key string) string {
	value, _ := s.options.Environment(key)
	return value
}

var invalidID = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

func (s *Service) DataDirectory(source string) (string, error) {
	directory := s.environment("CLAUDE_CODE_PLUGIN_CACHE_DIR")
	if directory == "~" {
		directory = s.options.Home
	} else if strings.HasPrefix(directory, "~/") || strings.HasPrefix(directory, "~\\") {
		directory = filepath.Join(s.options.Home, directory[2:])
	}
	if directory == "" {
		kind := "plugins"
		if s.options.Cowork || envTruthy(s.environment("CLAUDE_CODE_USE_COWORK_PLUGINS")) {
			kind = "cowork_plugins"
		}
		directory = filepath.Join(s.options.ConfigDir, kind)
	}
	if !filepath.IsAbs(directory) {
		directory = filepath.Join(s.options.CWD, directory)
	}
	directory = filepath.Join(directory, "data", invalidID.ReplaceAllString(source, "-"))
	return directory, os.MkdirAll(directory, 0777)
}
func (s *Service) LoadPluginMCPServers(ctx context.Context, plugin protocol.JSONObject, failures *[]protocol.JSONObject) (*protocol.JSONObject, error) {
	servers := protocol.NewJSONObject()
	if value := s.loadFile(ctx, plugin.String("path"), ".mcp.json"); value != nil {
		servers.Assign(*value)
	}
	spec := object(plugin.Get("manifest")).Get("mcpServers")
	if truthy(spec) {
		var text string
		var list []json.RawMessage
		if json.Unmarshal(spec, &text) == nil {
			value, err := s.loadSpec(ctx, plugin, spec, failures)
			if err != nil {
				return nil, err
			}
			if value != nil {
				servers.Assign(*value)
			}
		} else if len(spec) > 0 && spec[0] == '[' && json.Unmarshal(spec, &list) == nil {
			values := make([]*protocol.JSONObject, len(list))
			errs := make([]error, len(list))
			var group sync.WaitGroup
			var mu sync.Mutex
			completedErrors := []protocol.JSONObject{}
			for i, item := range list {
				group.Add(1)
				go func(i int, item json.RawMessage) {
					defer group.Done()
					local := []protocol.JSONObject{}
					values[i], errs[i] = s.loadSpec(ctx, plugin, item, &local)
					mu.Lock()
					completedErrors = append(completedErrors, local...)
					mu.Unlock()
				}(i, item)
			}
			group.Wait()
			for _, failure := range completedErrors {
				appendError(failures, failure)
			}
			// Promise.all results merge in source order despite completion order.
			for i, value := range values {
				if errs[i] != nil {
					return nil, errs[i]
				}
				if value != nil {
					servers.Assign(*value)
				}
			}
		} else {
			servers.Assign(object(spec))
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(servers.OwnProperties()) == 0 {
		return nil, nil
	}
	return &servers, nil
}
func (s *Service) loadSpec(ctx context.Context, plugin protocol.JSONObject, spec json.RawMessage, failures *[]protocol.JSONObject) (*protocol.JSONObject, error) {
	var path string
	if json.Unmarshal(spec, &path) != nil {
		value := object(spec)
		return &value, nil
	}
	if !strings.HasSuffix(path, ".mcpb") && !strings.HasSuffix(path, ".dxt") {
		return s.loadFile(ctx, plugin.String("path"), path), nil
	}
	result, err := s.options.LoadMCPB(ctx, path, plugin.String("path"), plugin.String("repository"), func(status string) { s.debug("MCPB [" + plugin.String("name") + "]: " + status) })
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		message := err.Error()
		rawMessage := raw(message)
		if original, ok := err.(interface{ JSONMessage() json.RawMessage }); ok {
			rawMessage = original.JSONMessage()
		}
		base := record(field("source", plugin.String("name")+"@"+plugin.String("repository")), field("plugin", plugin.String("name")))
		if strings.HasPrefix(path, "http") && (strings.Contains(message, "download") || strings.Contains(message, "network")) {
			base.Set("type", raw("mcpb-download-failed"))
			base.Set("url", raw(path))
			base.Set("reason", rawMessage)
		} else if strings.Contains(message, "manifest") || strings.Contains(message, "user configuration") {
			base.Set("type", raw("mcpb-invalid-manifest"))
			base.Set("mcpbPath", raw(path))
			base.Set("validationError", rawMessage)
		} else {
			base.Set("type", raw("mcpb-extract-failed"))
			base.Set("mcpbPath", raw(path))
			base.Set("reason", rawMessage)
		}
		appendError(failures, base)
		return nil, nil
	}
	if result.NeedsConfig {
		return nil, nil
	}
	if result.Manifest.String("name") == "" || !result.Config.IsObject() {
		return nil, fmt.Errorf("native MCPB loader returned invalid success for %s", path)
	}
	value := record(field(result.Manifest.String("name"), result.Config))
	return &value, nil
}
func (s *Service) loadFile(ctx context.Context, root, path string) *protocol.JSONObject {
	if ctx.Err() != nil {
		return nil
	}
	// path.join on the original POSIX Source app retains plugin root even for
	// a leading-slash spec. The production OS determines separators.
	full := filepath.Join(root, path)
	data, err := os.ReadFile(full)
	if err != nil {
		if !os.IsNotExist(err) {
			s.debug("Failed to load MCP servers from " + full + ": " + err.Error())
		}
		return nil
	}
	decoded := protocol.DecodeJavaScriptUTF8(data)

	var parsed protocol.JSONObject
	parsedValid := json.Valid(decoded)
	if parsedValid {
		parsed = spreadJSON(decoded)
	}
	if !parsedValid || string(decoded) == "null" {
		s.debug("Failed to load MCP servers from " + full)
		return nil
	}
	source := parsed
	if truthy(parsed.Get("mcpServers")) {
		source = spreadJSON(parsed.Get("mcpServers"))
	}
	result := protocol.NewJSONObject()
	for _, p := range properties(source) {
		if name(p) == "__proto__" {
			continue
		}
		if config, ok := validateServer(p.Value); ok {
			_ = result.SetPropertyJSON(p.NameJSON, raw(config))
		} else {
			s.debug("Invalid MCP server config for " + name(p) + " in " + full)
		}
	}
	return &result
}
func spreadJSON(value json.RawMessage) protocol.JSONObject {
	var o protocol.JSONObject
	if o.UnmarshalJSON(value) == nil && o.IsObject() {
		return o
	}
	var list []json.RawMessage
	if len(value) > 0 && value[0] == '[' && json.Unmarshal(value, &list) == nil {
		result := protocol.NewJSONObject()
		for i, item := range list {
			result.Set(fmt.Sprint(i), item)
		}
		return result
	}
	return protocol.NewJSONObject()
}
