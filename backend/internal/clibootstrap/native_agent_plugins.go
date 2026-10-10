package clibootstrap

import (
	"context"
	"encoding/json"
	"io/fs"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/systemprompt"
)

var agentConfigVariable = regexp.MustCompile(`\$\{user_config\.([^}]+)\}`)

func (p *NativeProcessSettings) nativePluginAgentDefinitions(owner *cooperative.Owner, scope *cooperative.Scope, n *NativeBoundary) ([]agentcatalog.Definition, error) {
	if !scope.BelongsTo(owner) {
		return nil, cooperative.ErrRestricted
	}
	plugins, err := p.LoadPlugins(context.Background(), "", false)
	if err != nil {
		return nil, err
	}
	settings, err := p.startup.GetInitialSettings()
	if err != nil || settings == nil {
		return nil, err
	}
	memory := systemprompt.AutoMemoryEnabled(p.environment, *settings)
	out := []agentcatalog.Definition{}
	for _, plugin := range plugins.Enabled {
		seen := map[string]bool{}
		roots := append([]string{}, plugin.AgentsPaths...)
		if plugin.AgentsPath != "" {
			roots = append([]string{plugin.AgentsPath}, roots...)
		}
		for _, root := range roots {
			info, err := n.stat(root)
			if err != nil {
				continue
			}
			visit := func(path string, namespace []string) error {
				info, err := n.lstat(path)
				if err != nil {
					return nil
				}
				identity := (mainAgentsFiles{n}).Identity(info)
				if identity != "" && seen[identity] {
					return nil
				}
				if identity != "" {
					seen[identity] = true
				}
				data, err := n.readFile(path)
				if err != nil {
					return nil
				}
				definition, err := agentcatalog.PluginDefinition(path, plugin.Name, plugin.Source, namespace, data, memory, func(body string) (string, error) {
					return nativeAgentPluginSubstitute(n, plugin, *settings, body)
				})
				if err != nil {
					return nil // Source logs and skips a malformed individual plugin Agent.
				}
				out = append(out, *definition)
				return nil
			}
			if info.Mode().IsRegular() && strings.HasSuffix(root, ".md") {
				if err := visit(root, nil); err != nil {
					return nil, err
				}
				continue
			}
			var walk func(string, []string) error
			walk = func(directory string, namespace []string) error {
				entries, err := n.readDir(directory)
				if err != nil {
					return nil
				}
				for _, entry := range entries {
					path := filepath.Join(directory, entry.Name())
					if entry.IsDir() {
						if err := walk(path, append(append([]string{}, namespace...), entry.Name())); err != nil {
							return err
						}
					} else if entry.Type()&fs.ModeSymlink == 0 && strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
						if err := visit(path, namespace); err != nil {
							return err
						}
					}
				}
				return nil
			}
			if err := walk(root, nil); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

func nativeAgentPluginSubstitute(n *NativeBoundary, plugin pluginregistry.Loaded, settings protocol.JSONObject, body string) (string, error) {
	normalize := func(path string) string {
		if runtime.GOOS == "windows" {
			return strings.ReplaceAll(path, "\\", "/")
		}
		return path
	}
	body = strings.ReplaceAll(body, "${CLAUDE_PLUGIN_ROOT}", normalize(plugin.Path))
	if strings.Contains(body, "${CLAUDE_PLUGIN_DATA}") && plugin.Source != "" {
		path, err := nativePluginDataDirectory(n, context.Background(), plugin.Source)
		if err != nil {
			return "", err
		}
		body = strings.ReplaceAll(body, "${CLAUDE_PLUGIN_DATA}", normalize(path))
	}
	var schema, configured, config, values protocol.JSONObject
	_ = schema.UnmarshalJSON(plugin.Manifest.Get("userConfig"))
	_ = configured.UnmarshalJSON(settings.Get("pluginConfigs"))
	_ = config.UnmarshalJSON(configured.Get(plugin.Source))
	_ = values.UnmarshalJSON(config.Get("options"))
	if schema.IsObject() {
		body = agentConfigVariable.ReplaceAllStringFunc(body, func(match string) string {
			key := agentConfigVariable.FindStringSubmatch(match)[1]
			var declaration protocol.JSONObject
			_ = declaration.UnmarshalJSON(schema.Get(key))
			if string(declaration.Get("sensitive")) == "true" {
				return "[sensitive option '" + key + "' not available in skill content]"
			}
			value := values.Get(key)
			if value == nil {
				return match
			}
			text, err := protocol.JavaScriptStringJSON(value)
			if err != nil {
				return match
			}
			var decoded string
			_ = json.Unmarshal(text, &decoded)
			return decoded
		})
	}
	return body, nil
}
