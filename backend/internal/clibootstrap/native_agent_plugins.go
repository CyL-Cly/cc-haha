package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginoptions"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/systemprompt"
)

func (p *NativeProcessSettings) nativePluginAgentDefinitions(owner *cooperative.Owner, scope *cooperative.Scope, n *NativeBoundary, options *pluginoptions.Service) ([]agentcatalog.Definition, error) {
	if !scope.BelongsTo(owner) {
		return nil, cooperative.ErrRestricted
	}
	plugins, err := p.LoadPlugins(context.Background(), "", false)
	if err != nil {
		return nil, err
	}
	if len(plugins.Enabled) == 0 {
		return []agentcatalog.Definition{}, nil
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
				definition, err := agentcatalog.PluginDefinitionJSON(path, plugin.Name, plugin.Source, namespace, data, memory, func(body json.RawMessage) (json.RawMessage, error) {
					return nativeAgentPluginSubstituteJSON(n, plugin, options, p.platform, body)
				})
				if err != nil {
					var unavailable *MainCapabilityError
					if errors.As(err, &unavailable) {
						return err
					}
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

func nativeAgentPluginSubstituteJSON(n *NativeBoundary, plugin pluginregistry.Loaded, options *pluginoptions.Service, platform string, body json.RawMessage) (json.RawMessage, error) {
	normalize := func(path string) string {
		if platform == "windows" || platform == "win32" {
			return strings.ReplaceAll(path, "\\", "/")
		}
		return path
	}
	body, err := protocol.ReplaceJavaScriptLiteralJSON(body, "${CLAUDE_PLUGIN_ROOT}", func() (json.RawMessage, error) { return quoteMain(normalize(plugin.Path)), nil })
	if err != nil {
		return nil, err
	}
	if plugin.Source != "" {
		body, err = protocol.ReplaceJavaScriptLiteralJSON(body, "${CLAUDE_PLUGIN_DATA}", func() (json.RawMessage, error) {
			path, err := nativePluginDataDirectory(n, context.Background(), plugin.Source)
			return quoteMain(normalize(path)), err
		})
		if err != nil {
			return nil, err
		}
	}
	var schema protocol.JSONObject
	_ = schema.UnmarshalJSON(plugin.Manifest.Get("userConfig"))
	if schema.IsObject() {
		if options == nil {
			return nil, mainCap("plugin Agent shared saved options producer")
		}
		values, err := options.Load(context.Background(), plugin.Source)
		if err != nil {
			return nil, err
		}
		return pluginoptions.SubstituteUserConfigInContentJSON(body, values, schema)
	}
	return body, nil
}
