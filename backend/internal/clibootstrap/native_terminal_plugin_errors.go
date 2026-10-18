package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (p *NativeProcessSettings) populateTerminalPluginErrors(ctx context.Context, known protocol.JSONObject, options *protocol.JSONObject) error {
	settings, scopes := protocol.NewJSONObject(), protocol.NewJSONObject()
	additional, err := p.pluginCLIAdditionalEnabled(ctx)
	if err != nil {
		return err
	}
	collect := func(enabled protocol.JSONObject, scope string) {
		for _, property := range enabled.OwnProperties() {
			var id string
			_ = json.Unmarshal(property.NameJSON, &id)
			if !strings.Contains(id, "@") {
				continue
			}
			if string(property.Value) == "true" {
				scopes.Set(id, quoteMain(scope))
			} else if string(property.Value) == "false" {
				scopes.Delete(id)
			}
		}
	}
	collect(additional, "flag")
	for _, scope := range []string{"managed", "user", "project", "local", "flag"} {
		value, err := p.pluginCLIReadSettings(ctx, scope, "")
		if err != nil {
			return err
		}
		settings.Set(scope, rawMain(value))
		collect(mainObj(value.Get("enabledPlugins")), scope)
	}
	policy := pluginregistry.PolicyFromSettings(mainObj(settings.Get("managed")))
	broken := []protocol.JSONObject{}
	for _, property := range known.OwnProperties() {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry := mainObj(property.Value)
		if !policy.Allowed(mainObj(entry.Get("source"))) {
			continue
		}
		if _, err := pluginsapi.ReadMarketplace(entry.String("installLocation")); err != nil {
			var name string
			_ = json.Unmarshal(property.NameJSON, &name)
			broken = append(broken, protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: quoteMain(name)}, protocol.JSONField{Name: "error", Value: quoteMain(err.Error())}))
		}
	}
	options.Set("errorSettings", rawMain(settings))
	options.Set("pluginScopes", rawMain(scopes))
	options.Set("brokenMarketplaces", rawMain(broken))
	return nil
}

// Errors removal is the original narrowly scoped settings action. Only
// sources which currently declare this extra marketplace are mutated, using
// the existing native writer and cache invalidation under this same owner.
func (m *MainPrint) RemoveTerminalPluginExtraMarketplace(ctx context.Context, name string) (protocol.JSONObject, error) {
	return m.terminalPluginCacheEdit(ctx, func(worker context.Context, jobs *pluginCLIJobs) (protocol.JSONObject, error) {
		removed := []string{}
		for _, scope := range []string{"user", "project", "local"} {
			settings, err := m.nativeSettings.pluginCLIReadSettings(worker, scope, "")
			if err != nil {
				return protocol.JSONObject{}, err
			}
			extra := mainObj(settings.Get("extraKnownMarketplaces"))
			if !nativeSettingsTruthy(extra.Get(name)) {
				continue
			}
			patch := clisettings.Patch{Object: protocol.NewJSONObject(), UndefinedPaths: [][]json.RawMessage{{quoteMain("extraKnownMarketplaces"), quoteMain(name)}}}
			patch.Object.Set("extraKnownMarketplaces", rawMain(extra))
			enabled := mainObj(settings.Get("enabledPlugins"))
			for _, property := range enabled.OwnProperties() {
				var id string
				_ = json.Unmarshal(property.NameJSON, &id)
				if strings.HasSuffix(id, "@"+name) {
					patch.Object.Set("enabledPlugins", rawMain(enabled))
					patch.UndefinedPaths = append(patch.UndefinedPaths, []json.RawMessage{quoteMain("enabledPlugins"), property.NameJSON})
				}
			}
			if err = m.nativeSettings.pluginCLIWrite(worker, scope, "", patch, jobs); err != nil {
				return protocol.JSONObject{}, err
			}
			removed = append(removed, scope)
		}
		if err := m.nativeSettings.pluginCLIClear(worker); err != nil {
			return protocol.JSONObject{}, err
		}
		m.RetireNativePluginMarketplaceInstallation(name)
		return protocol.NewJSONObject(protocol.JSONField{Name: "message", Value: quoteMain("✔ Removed \"" + name + "\" from " + strings.Join(removed, ", ") + " settings")}), nil
	})
}

func (m *MainPrint) RemoveTerminalFailedPlugin(ctx context.Context, id, scope string) error {
	_, err := m.terminalPluginCacheEdit(ctx, func(worker context.Context, jobs *pluginCLIJobs) (protocol.JSONObject, error) {
		if scope == "managed" {
			return protocol.JSONObject{}, fmt.Errorf("Managed by your organization — contact your admin")
		}
		selectedScope := scope
		if scope != "user" && scope != "project" && scope != "local" {
			selectedScope = "user"
		}
		service, err := m.nativeSettings.pluginCLIOperations(m.pluginOptions, jobs)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		if _, err = service.Uninstall(worker, id, selectedScope, "", true); err == nil {
			return protocol.NewJSONObject(), nil
		}
		// The original recovery path removes declarations from every editable
		// source when installation removal failed, retaining persistent data.
		removed := false
		for _, source := range []string{"user", "project", "local"} {
			settings, readErr := m.nativeSettings.pluginCLIReadSettings(worker, source, "")
			if readErr != nil {
				return protocol.JSONObject{}, readErr
			}
			if mainObj(settings.Get("enabledPlugins")).Get(id) != nil {
				if writeErr := m.nativeSettings.pluginCLIWriteEnabled(worker, source, "", id, nil, jobs); writeErr != nil {
					return protocol.JSONObject{}, writeErr
				}
				removed = true
			}
		}
		if cacheErr := m.nativeSettings.pluginCLIClear(worker); cacheErr != nil {
			return protocol.JSONObject{}, cacheErr
		}
		if !removed {
			return protocol.JSONObject{}, err
		}
		return protocol.NewJSONObject(), nil
	})
	return err
}
