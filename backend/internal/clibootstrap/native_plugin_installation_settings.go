package clibootstrap

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (p *NativeProcessSettings) declaredPluginMarketplaces(ctx context.Context) (protocol.JSONObject, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return protocol.JSONObject{}, err
	}
	if err := p.ensureSettings(); err != nil {
		return protocol.JSONObject{}, err
	}
	enabled, additional := protocol.NewJSONObject(), protocol.NewJSONObject()
	merge := func(target *protocol.JSONObject, source protocol.JSONObject) {
		for _, property := range source.OwnProperties() {
			_ = target.SetPropertyJSON(property.NameJSON, property.Value)
		}
	}
	for _, directory := range p.pluginAdditional {
		if !filepath.IsAbs(directory) {
			directory = filepath.Join(p.n.CWD, directory)
		}
		for _, filename := range []string{"settings.json", "settings.local.json"} {
			parsed, err := p.settings.ParseFile(filepath.Join(directory, ".claude", filename))
			if err != nil {
				return protocol.JSONObject{}, err
			}
			if parsed.Settings == nil {
				continue
			}
			merge(&enabled, mainObj(parsed.Settings.Get("enabledPlugins")))
			merge(&additional, mainObj(parsed.Settings.Get("extraKnownMarketplaces")))
		}
	}
	initial, err := p.startup.GetInitialSettings()
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if initial != nil {
		merge(&enabled, mainObj(initial.Get("enabledPlugins")))
	}
	declared := protocol.NewJSONObject()
	for _, property := range enabled.OwnProperties() {
		var id string
		_ = json.Unmarshal(property.NameJSON, &id)
		if planRawTruthy(property.Value) && strings.Contains(id, "@") && strings.Split(id, "@")[1] == "claude-plugins-official" {
			declared.Set("claude-plugins-official", rawMain(map[string]any{"source": map[string]string{"source": "github", "repo": "anthropics/claude-plugins-official"}, "sourceIsFallback": true}))
			break
		}
	}
	merge(&declared, additional)
	if initial != nil {
		merge(&declared, mainObj(initial.Get("extraKnownMarketplaces")))
	}
	return declared, nil
}
