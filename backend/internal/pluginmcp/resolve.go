package pluginmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// ResolveValue shares the original plugin-variable, saved user_config and
// environment expansion order with LSP workspaceFolder and other scalar fields.
func (s *Service) ResolveValue(ctx context.Context, value json.RawMessage, plugin protocol.JSONObject, userConfig *protocol.JSONObject, missingVariables ...*[]string) (json.RawMessage, error) {
	missing := []string{}
	result, err := s.resolvePluginValue(ctx, value, plugin, userConfig, &missing)
	for _, target := range missingVariables {
		if target != nil {
			*target = append(*target, missing...)
		}
	}
	return result, err
}
func (s *Service) resolvePluginValue(ctx context.Context, value json.RawMessage, plugin protocol.JSONObject, userConfig *protocol.JSONObject, missing *[]string) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	normalize := func(value json.RawMessage) (json.RawMessage, error) {
		units, err := protocol.DecodeJavaScriptJSONString(value)
		if err != nil {
			return nil, err
		}
		if s.options.Platform == "windows" || s.options.Platform == "win32" {
			for i, unit := range units {
				if unit == '\\' {
					units[i] = '/'
				}
			}
		}
		return protocol.QuoteJavaScriptUTF16(units), nil
	}
	value, err := protocol.ReplaceJavaScriptLiteralJSON(value, "${CLAUDE_PLUGIN_ROOT}", func() (json.RawMessage, error) { return normalize(plugin.Get("path")) })
	if err != nil {
		return nil, err
	}
	if plugin.String("source") != "" {
		value, err = protocol.ReplaceJavaScriptLiteralJSON(value, "${CLAUDE_PLUGIN_DATA}", func() (json.RawMessage, error) {
			path, err := s.DataDirectory(plugin.String("source"))
			if err != nil {
				return nil, err
			}
			return normalize(raw(path))
		})
		if err != nil {
			return nil, err
		}
	}
	if userConfig != nil {
		value, err = protocol.ReplaceJavaScriptTemplatesJSON(value, "${user_config.", func(key json.RawMessage) (json.RawMessage, error) {
			input, err := userConfig.GetOrdinaryPropertyStringJSON(key)
			if err != nil {
				return nil, err
			}
			if input == nil {
				return nil, fmt.Errorf("Missing required user configuration value: %s. This should have been validated before variable substitution.", stringValue(key))
			}
			return input, nil
		})
		if err != nil {
			return nil, err
		}
	}
	return protocol.ReplaceJavaScriptTemplatesJSON(value, "${", func(content json.RawMessage) (json.RawMessage, error) {
		units, err := protocol.DecodeJavaScriptJSONString(content)
		if err != nil {
			return nil, err
		}
		key, fallback := units, []uint16(nil)
		for i := 0; i+1 < len(units); i++ {
			if units[i] == ':' && units[i+1] == '-' {
				key, fallback = units[:i], units[i+2:]
				for j := 0; j+1 < len(fallback); j++ {
					if fallback[j] == ':' && fallback[j+1] == '-' {
						fallback = fallback[:j]
						break
					}
				}
				break
			}
		}
		name := stringValue(protocol.QuoteJavaScriptUTF16(key))
		if found, ok := s.options.Environment(name); ok {
			return raw(found), nil
		}
		if fallback != nil {
			return protocol.QuoteJavaScriptUTF16(fallback), nil
		}
		*missing = append(*missing, name)
		return nil, nil
	})
}

func (s *Service) Resolve(ctx context.Context, config, plugin protocol.JSONObject, userConfig *protocol.JSONObject, failures *[]protocol.JSONObject, pluginName, serverName string, missingVariables ...*[]string) (protocol.JSONObject, error) {
	missing := []string{}
	defer func() {
		for _, target := range missingVariables {
			if target != nil {
				*target = append(*target, missing...)
			}
		}
	}()
	resolve := func(value json.RawMessage) (json.RawMessage, error) {
		return s.resolvePluginValue(ctx, value, plugin, userConfig, &missing)
	}
	result := config.Clone()
	set := func(target *protocol.JSONObject, key string) error {
		if truthy(target.Get(key)) {
			v, e := resolve(target.Get(key))
			if e != nil {
				return e
			}
			target.Set(key, v)
		}
		return nil
	}
	switch config.String("type") {
	case "", "stdio":
		if e := set(&result, "command"); e != nil {
			return protocol.JSONObject{}, e
		}
		if args := config.Get("args"); args != nil && truthy(args) {
			var values []json.RawMessage
			if json.Unmarshal(args, &values) != nil {
				return protocol.JSONObject{}, fmt.Errorf("stdioConfig.args.map is not a function")
			}
			for i, v := range values {
				expanded, e := resolve(v)
				if e != nil {
					return protocol.JSONObject{}, e
				}
				values[i] = expanded
			}
			result.Set("args", raw(values))
		}
		data, e := s.DataDirectory(plugin.String("source"))
		if e != nil {
			return protocol.JSONObject{}, e
		}
		env := record(field("CLAUDE_PLUGIN_ROOT", plugin.String("path")), field("CLAUDE_PLUGIN_DATA", data))
		env.Assign(object(config.Get("env")))
		for _, p := range properties(env) {
			key := name(p)
			if key == "CLAUDE_PLUGIN_ROOT" || key == "CLAUDE_PLUGIN_DATA" {
				continue
			}
			expanded, e := resolve(p.Value)
			if e != nil {
				return protocol.JSONObject{}, e
			}
			_ = env.SetPropertyJSON(p.NameJSON, expanded)
		}
		result.Set("env", raw(env))
	case "http", "sse", "ws":
		if e := set(&result, "url"); e != nil {
			return protocol.JSONObject{}, e
		}
		if headers := config.Get("headers"); truthy(headers) {
			next := protocol.NewJSONObject()
			for _, p := range properties(object(headers)) {
				expanded, e := resolve(p.Value)
				if e != nil {
					return protocol.JSONObject{}, e
				}
				_ = next.SetPropertyJSON(p.NameJSON, expanded)
			}
			result.Set("headers", raw(next))
		}
	case "sse-ide", "ws-ide", "sdk", "claudeai-proxy":
		return result, nil
	default:
		return protocol.JSONObject{}, nil
	}
	if failures != nil && len(missing) > 0 && pluginName != "" && serverName != "" {
		seen := map[string]bool{}
		unique := []string{}
		for _, v := range missing {
			if !seen[v] {
				seen[v] = true
				unique = append(unique, v)
			}
		}
		appendError(failures, record(field("type", "mcp-config-invalid"), field("source", "plugin:"+pluginName), field("plugin", pluginName), field("serverName", serverName), field("validationError", "Missing environment variables: "+strings.Join(unique, ", "))))
	}
	return result, nil
}
func AddPluginScope(servers protocol.JSONObject, pluginName, pluginSource string) protocol.JSONObject {
	result := protocol.NewJSONObject()
	for _, p := range properties(servers) {
		config := object(p.Value)
		config.Set("scope", raw("dynamic"))
		config.Set("pluginSource", raw(pluginSource))
		result.Set("plugin:"+pluginName+":"+name(p), raw(config))
	}
	return result
}
func (s *Service) userConfig(ctx context.Context, plugin protocol.JSONObject, server string) (*protocol.JSONObject, error) {
	manifest := object(plugin.Get("manifest"))
	var top *protocol.JSONObject
	if truthy(manifest.Get("userConfig")) {
		value, e := s.options.PluginOptions.Load(ctx, plugin.String("source"))
		if e != nil {
			return nil, e
		}
		top = &value
	}
	var channel *protocol.JSONObject
	var channels []protocol.JSONObject
	_ = json.Unmarshal(manifest.Get("channels"), &channels)
	for _, item := range channels {
		if item.String("server") == server {
			if truthy(item.Get("userConfig")) {
				var err error
				channel, err = s.LoadServerUserConfig(ctx, plugin.String("repository"), server)
				if err != nil {
					return nil, err
				}
			}
			break
		}
	}
	if top == nil && channel == nil {
		return nil, nil
	}
	result := protocol.NewJSONObject()
	if top != nil {
		result.Assign(*top)
	}
	if channel != nil {
		result.Assign(*channel)
	}
	return &result, nil
}
func (s *Service) resolveServers(ctx context.Context, plugin, servers protocol.JSONObject, failures *[]protocol.JSONObject) (protocol.JSONObject, error) {
	result := protocol.NewJSONObject()
	for _, p := range properties(servers) {
		server := name(p)
		if server == "__proto__" {
			continue
		}
		values, e := s.userConfig(ctx, plugin, server)
		if e != nil {
			return protocol.JSONObject{}, e
		}
		resolved, e := s.Resolve(ctx, object(p.Value), plugin, values, failures, plugin.String("name"), server)
		if e != nil {
			if ctx.Err() != nil {
				return protocol.JSONObject{}, ctx.Err()
			}
			appendError(failures, record(field("type", "generic-error"), field("source", server), field("plugin", plugin.String("name")), field("error", e.Error())))
			continue
		}
		_ = result.SetPropertyJSON(p.NameJSON, raw(resolved))
	}
	return result, nil
}
func (s *Service) Get(ctx context.Context, plugin protocol.JSONObject, failures *[]protocol.JSONObject) (*protocol.JSONObject, error) {
	if !truthy(plugin.Get("enabled")) {
		return nil, nil
	}
	var servers *protocol.JSONObject
	if truthy(plugin.Get("mcpServers")) {
		value := object(plugin.Get("mcpServers"))
		servers = &value
	} else {
		var e error
		servers, e = s.LoadPluginMCPServers(ctx, plugin, failures)
		if e != nil {
			return nil, e
		}
	}
	if servers == nil {
		return nil, nil
	}
	resolved, e := s.resolveServers(ctx, plugin, *servers, failures)
	if e != nil {
		return nil, e
	}
	value := AddPluginScope(resolved, plugin.String("name"), plugin.String("source"))
	return &value, nil
}

// Extract preserves input order for collisions and writes only the unresolved
// server cache to each plugin in the caller's supplied slice.
func (s *Service) Extract(ctx context.Context, plugins []protocol.JSONObject, failures *[]protocol.JSONObject) (protocol.JSONObject, error) {
	scoped := make([]protocol.JSONObject, len(plugins))
	caches := make([]*protocol.JSONObject, len(plugins))
	errs := make([]error, len(plugins))
	var group sync.WaitGroup
	var mu sync.Mutex
	completedErrors := []protocol.JSONObject{}
	for i := range plugins {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			local := []protocol.JSONObject{}
			plugin := plugins[i]
			if !truthy(plugin.Get("enabled")) {
				return
			}
			servers, e := s.LoadPluginMCPServers(ctx, plugin, &local)
			if e != nil {
				errs[i] = e
				return
			}
			if servers == nil {
				return
			}
			resolved, e := s.resolveServers(ctx, plugin, *servers, &local)
			if e != nil {
				errs[i] = e
				return
			}
			caches[i] = servers
			scoped[i] = AddPluginScope(resolved, plugin.String("name"), plugin.String("source"))
			mu.Lock()
			completedErrors = append(completedErrors, local...)
			mu.Unlock()
		}(i)
	}
	group.Wait()
	for _, failure := range completedErrors {
		appendError(failures, failure)
	}
	all := protocol.NewJSONObject()
	for i, value := range scoped {
		if caches[i] != nil {
			plugins[i].Set("mcpServers", raw(*caches[i]))
		}
		if errs[i] != nil {
			return protocol.JSONObject{}, errs[i]
		}
		if value.IsObject() {
			all.Assign(value)
		}
	}
	return all, nil
}
