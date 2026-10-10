package systemprompt

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/markdownfrontmatter"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// PluginStyles consumes enabled plugins from the actual shared registry. The
// same native filesystem and parser used by style resolution own its content;
// no fixture-rendered style or second settings lifecycle is introduced.
func (l *StyleLoader) PluginStyles(ctx context.Context, plugins []pluginregistry.Loaded) ([]StyleConfig, error) {
	styles := []StyleConfig{}
	for _, plugin := range plugins {
		loaded := map[string]bool{}
		loadFile := func(path string) {
			canonical, err := l.options.Realpath(path)
			if err != nil {
				canonical = filepath.Clean(path)
			}
			if loaded[canonical] {
				return
			}
			loaded[canonical] = true
			data, err := l.options.Files.ReadFile(path)
			if err != nil {
				return
			}
			parsed, err := markdownfrontmatter.Parse(string(protocol.DecodeJavaScriptUTF8(data)))
			if err != nil {
				return
			}
			base := strings.TrimSuffix(filepath.Base(path), ".md")
			if name := parsed.Get("name"); name.Truthy() {
				base = name.String()
			}
			name := plugin.Name + ":" + base
			description := ""
			if value := parsed.Get("description"); value != nil {
				switch value.Kind {
				case "string":
					description = styleTrim(value.String())
				case "boolean", "number":
					description = value.String()
				}
			}
			if description == "" {
				description = styleDescription(nil, parsed.Content, "")
				if styleTrim(parsed.Content) == "" {
					description = "Output style from " + plugin.Name + " plugin"
				}
			}
			force := parsed.Get("force-for-plugin")
			forced := force != nil && (force.Kind == "boolean" || force.Kind == "string") && force.String() == "true"
			styles = append(styles, StyleConfig{OutputStyle: OutputStyle{Name: name, Prompt: styleTrim(parsed.Content)}, Description: description, Source: "plugin", ForceForPlugin: forced})
		}
		var walk func(string) error
		walk = func(dir string) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			entries, err := l.options.ReadDir(dir)
			if err != nil {
				return nil
			}
			for _, entry := range entries {
				if err := ctx.Err(); err != nil {
					return err
				}
				path := filepath.Join(dir, entry.Name())
				if entry.IsDir() {
					if err := walk(path); err != nil {
						return err
					}
				} else if entry.Type().IsRegular() && strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
					loadFile(path)
				}
			}
			return nil
		}
		if plugin.OutputStylesPath != "" {
			if err := walk(plugin.OutputStylesPath); err != nil {
				return nil, err
			}
		}
		for _, path := range plugin.OutputStylesPaths {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			stat, err := l.options.Files.Stat(path)
			if err != nil {
				continue
			}
			if stat.IsDir() {
				if err := walk(path); err != nil {
					return nil, err
				}
			} else if stat.Mode().IsRegular() && strings.HasSuffix(path, ".md") {
				loadFile(path)
			}
		}
	}
	return styles, nil
}
