package clibootstrap

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (m *MainPrint) TerminalHookConfigurations(ctx context.Context) ([]protocol.JSONObject, error) {
	rows := []protocol.JSONObject{}
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		for _, source := range []string{"userSettings", "projectSettings", "localSettings", "flagSettings", "policySettings"} {
			settings, err := m.Startup.GetSettingsForSource(source)
			if err != nil {
				return err
			}
			if settings == nil {
				continue
			}
			var hooks protocol.JSONObject
			_ = hooks.UnmarshalJSON(settings.Get("hooks"))
			for _, eventProperty := range hooks.OwnProperties() {
				var event string
				_ = json.Unmarshal(eventProperty.NameJSON, &event)
				var matchers []protocol.JSONObject
				_ = json.Unmarshal(eventProperty.Value, &matchers)
				for matcherIndex, matcher := range matchers {
					var values []protocol.JSONObject
					_ = json.Unmarshal(matcher.Get("hooks"), &values)
					for hookIndex, hook := range values {
						rows = append(rows, protocol.NewJSONObject(protocol.JSONField{Name: "source", Value: quoteMain(source)}, protocol.JSONField{Name: "event", Value: quoteMain(event)}, protocol.JSONField{Name: "matcher", Value: matcher.Get("matcher")}, protocol.JSONField{Name: "matcherIndex", Value: rawMain(matcherIndex)}, protocol.JSONField{Name: "hookIndex", Value: rawMain(hookIndex)}, protocol.JSONField{Name: "hook", Value: rawMain(hook)}))
					}
				}
			}
		}
		registered, err := m.nativeSettings.RegisteredPluginHooks(ctx)
		if err != nil {
			return err
		}
		if registered != nil {
			for _, property := range registered.OwnProperties() {
				var event string
				_ = json.Unmarshal(property.NameJSON, &event)
				var matchers []protocol.JSONObject
				_ = json.Unmarshal(property.Value, &matchers)
				for _, matcher := range matchers {
					if matcher.Get("pluginRoot") == nil {
						continue
					}
					var hooks []protocol.JSONObject
					_ = json.Unmarshal(matcher.Get("hooks"), &hooks)
					for _, hook := range hooks {
						rows = append(rows, protocol.NewJSONObject(protocol.JSONField{Name: "source", Value: quoteMain("pluginHook")}, protocol.JSONField{Name: "pluginName", Value: matcher.Get("pluginId")}, protocol.JSONField{Name: "event", Value: quoteMain(event)}, protocol.JSONField{Name: "matcher", Value: matcher.Get("matcher")}, protocol.JSONField{Name: "hook", Value: rawMain(hook)}))
					}
				}
			}
		}
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return rows, err
}
