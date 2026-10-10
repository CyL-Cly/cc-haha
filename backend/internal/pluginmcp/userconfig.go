package pluginmcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Separate per-server keys retain the original `${pluginId}/${serverName}`
// trust domain; top-level plugin options never replace channel credentials.
func (s *Service) LoadServerUserConfig(ctx context.Context, id, server string) (*protocol.JSONObject, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	settings, err := s.options.Settings(ctx)
	if err != nil {
		s.debug("Failed to load user config for " + id + "/" + server + ": " + err.Error())
		return nil, nil
	}
	nonSensitive := object(object(object(settings.Get("pluginConfigs")).Get(id)).Get("mcpServers")).Get(server)
	data, err := s.options.Secure.Read(ctx)
	if err != nil {
		s.debug("Failed to load user config for " + id + "/" + server + ": " + err.Error())
		return nil, nil
	}
	var sensitive json.RawMessage
	if data != nil {
		sensitive = object(data.Get("pluginSecrets")).Get(id + "/" + server)
	}
	if !truthy(nonSensitive) && !truthy(sensitive) {
		return nil, nil
	}
	value := object(nonSensitive)
	value.Assign(object(sensitive))
	return &value, nil
}
func jsonPath(parts ...string) []json.RawMessage {
	result := []json.RawMessage{}
	for _, part := range parts {
		result = append(result, raw(part))
	}
	return result
}
func (s *Service) SaveServerUserConfig(ctx context.Context, id, server string, values, schema protocol.JSONObject) (err error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	defer func() {
		if err != nil {
			err = fmt.Errorf("Failed to save user configuration for %s/%s: %s", id, server, err.Error())
		}
	}()
	nonSensitive, sensitive := protocol.NewJSONObject(), protocol.NewJSONObject()
	for _, p := range properties(values) {
		key := name(p)
		if string(object(schema.Get(key)).Get("sensitive")) == "true" {
			value, e := protocol.ExactJavaScriptStringJSON(p.Value)
			if e != nil {
				return e
			}
			_ = sensitive.SetPropertyJSON(p.NameJSON, value)
		} else {
			_ = nonSensitive.SetPropertyJSON(p.NameJSON, p.Value)
		}
	}
	secure, err := s.options.Secure.Read(ctx)
	if err != nil {
		return err
	}
	old := protocol.NewJSONObject()
	key := id + "/" + server
	if secure != nil {
		old = object(object(secure.Get("pluginSecrets")).Get(key))
	}
	scrubbed := old.Clone()
	for _, p := range properties(nonSensitive) {
		scrubbed.Delete(name(p))
	}
	if len(sensitive.OwnProperties()) > 0 || len(scrubbed.OwnProperties()) != len(old.OwnProperties()) {
		result, e := s.options.Secure.Change(ctx, func(data protocol.JSONObject) error {
			secrets := object(data.Get("pluginSecrets"))
			next := scrubbed.Clone()
			next.Assign(sensitive)
			secrets.Set(key, raw(next))
			data.Set("pluginSecrets", raw(secrets))
			return nil
		})
		if e != nil {
			return e
		}
		if !result.Success {
			return fmt.Errorf("Failed to save sensitive config to secure storage for %s", key)
		}
		if result.Warning != "" {
			s.debug("Server secrets save warning: " + result.Warning)
		}
	}
	settings, err := s.options.Settings(ctx)
	if err != nil {
		return err
	}
	configs := object(settings.Get("pluginConfigs"))
	plugin := object(configs.Get(id))
	servers := object(plugin.Get("mcpServers"))
	oldSettings := object(servers.Get(server))
	deletions := [][]json.RawMessage{}
	for _, p := range properties(sensitive) {
		key := name(p)
		if oldSettings.Get(key) != nil {
			deletions = append(deletions, jsonPath("pluginConfigs", id, "mcpServers", server, key))
		}
	}
	if len(nonSensitive.OwnProperties()) > 0 || len(deletions) > 0 {
		servers.Set(server, raw(nonSensitive))
		plugin.Set("mcpServers", raw(servers))
		configs.Set(id, raw(plugin))
		settings.Set("pluginConfigs", raw(configs))
		if err = s.options.WriteSettings(ctx, clisettings.Patch{Object: settings, UndefinedPaths: deletions}); err != nil {
			return err
		}
	}
	return nil
}
