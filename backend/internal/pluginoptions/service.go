// Package pluginoptions shares the original settings/secure-storage split for
// plugin options across skills, hooks, MCP and desktop connector setup.
package pluginoptions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

type Options struct {
	Settings      func(context.Context) (protocol.JSONObject, error)
	WriteSettings func(context.Context, clisettings.Patch) error
	Secure        *securestorage.Store
	OnError       func(error)
}
type Service struct {
	options     Options
	mu, writeMu sync.Mutex
	cache       map[string]protocol.JSONObject
	generation  uint64
}

func New(o Options) (*Service, error) {
	if o.Settings == nil || o.WriteSettings == nil || o.Secure == nil {
		return nil, errors.New("plugin options require shared settings writer and secure credential storage")
	}
	return &Service{options: o, cache: map[string]protocol.JSONObject{}}, nil
}
func object(raw json.RawMessage) protocol.JSONObject {
	var o protocol.JSONObject
	_ = o.UnmarshalJSON(raw)
	if !o.IsObject() {
		return protocol.NewJSONObject()
	}
	return o
}
func encode(o protocol.JSONObject) json.RawMessage { data, _ := o.MarshalJSON(); return data }
func (s *Service) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache = map[string]protocol.JSONObject{}
	s.generation++
}
func (s *Service) Load(ctx context.Context, id string) (protocol.JSONObject, error) {
	for {
		s.mu.Lock()
		generation := s.generation
		if value, ok := s.cache[id]; ok {
			s.mu.Unlock()
			return value.Clone(), nil
		}
		s.mu.Unlock()
		settings, err := s.options.Settings(ctx)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		value := object(object(object(settings.Get("pluginConfigs")).Get(id)).Get("options"))
		secure, err := s.options.Secure.Read(ctx)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		if secure != nil {
			value.Assign(object(object(secure.Get("pluginSecrets")).Get(id)))
		}
		s.mu.Lock()
		if generation != s.generation {
			s.mu.Unlock()
			continue
		}
		s.cache[id] = value.Clone()
		s.mu.Unlock()
		return value, nil
	}
}
func (s *Service) Save(ctx context.Context, id string, values, schema protocol.JSONObject) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	nonSensitive, sensitive := protocol.NewJSONObject(), protocol.NewJSONObject()
	for _, p := range values.OwnProperties() {
		var key string
		_ = json.Unmarshal(p.NameJSON, &key)
		if string(object(schema.Get(key)).Get("sensitive")) == "true" {
			text, err := protocol.JavaScriptStringJSON(p.Value)
			if err != nil {
				return err
			}
			_ = sensitive.SetPropertyJSON(p.NameJSON, text)
		} else {
			_ = nonSensitive.SetPropertyJSON(p.NameJSON, p.Value)
		}
	}
	existing, err := s.options.Secure.Read(ctx)
	if err != nil {
		return err
	}
	oldSecure := protocol.NewJSONObject()
	if existing != nil {
		oldSecure = object(object(existing.Get("pluginSecrets")).Get(id))
	}
	scrubbed := oldSecure.Clone()
	for _, p := range nonSensitive.OwnProperties() {
		var k string
		_ = json.Unmarshal(p.NameJSON, &k)
		scrubbed.Delete(k)
	}
	if len(sensitive.OwnProperties()) > 0 || len(oldSecure.OwnProperties()) != len(scrubbed.OwnProperties()) {
		result, err := s.options.Secure.Change(ctx, func(current protocol.JSONObject) error {
			secrets := object(current.Get("pluginSecrets"))
			next := scrubbed.Clone()
			next.Assign(sensitive)
			secrets.Set(id, encode(next))
			current.Set("pluginSecrets", encode(secrets))
			return nil
		})
		if err != nil {
			return err
		}
		if !result.Success {
			return fmt.Errorf("Failed to save sensitive plugin options for %s to secure storage", id)
		}
	}
	settings, err := s.options.Settings(ctx)
	if err != nil {
		return err
	}
	configs := object(settings.Get("pluginConfigs"))
	config := object(configs.Get(id))
	oldSettings := object(config.Get("options"))
	paths := [][]json.RawMessage{}
	for _, p := range sensitive.OwnProperties() {
		var key string
		_ = json.Unmarshal(p.NameJSON, &key)
		if oldSettings.Get(key) != nil {
			paths = append(paths, jsonPath("pluginConfigs", id, "options", key))
		}
	}
	if len(nonSensitive.OwnProperties()) > 0 || len(paths) > 0 {
		config.Set("options", encode(nonSensitive))
		configs.Set(id, encode(config))
		settings.Set("pluginConfigs", encode(configs))
		if err := s.options.WriteSettings(ctx, clisettings.Patch{Object: settings, UndefinedPaths: paths}); err != nil {
			return fmt.Errorf("Failed to save plugin options for %s: %s", id, err)
		}
	}
	s.Clear()
	return nil
}
func (s *Service) report(err error) {
	if err != nil && s.options.OnError != nil {
		s.options.OnError(err)
	}
}

// Delete is best effort after the final plugin installation is removed. It
// removes only that identity and its per-server composite secret keys.
func (s *Service) Delete(ctx context.Context, id string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	settings, err := s.options.Settings(ctx)
	if err != nil {
		s.report(err)
	} else if object(settings.Get("pluginConfigs")).Get(id) != nil {
		configs := protocol.NewJSONObject()
		configs.Set(id, []byte("null"))
		patch := protocol.NewJSONObject(protocol.JSONField{Name: "pluginConfigs", Value: encode(configs)})
		s.report(s.options.WriteSettings(ctx, clisettings.Patch{Object: patch, UndefinedPaths: [][]json.RawMessage{jsonPath("pluginConfigs", id)}}))
	}
	existing, err := s.options.Secure.Read(ctx)
	if err != nil {
		s.report(err)
	} else if existing != nil {
		secrets := object(existing.Get("pluginSecrets"))
		changed := false
		for _, p := range secrets.OwnProperties() {
			var key string
			_ = json.Unmarshal(p.NameJSON, &key)
			if key == id || strings.HasPrefix(key, id+"/") {
				changed = true
				secrets.Delete(key)
			}
		}
		if changed {
			result, err := s.options.Secure.Change(ctx, func(current protocol.JSONObject) error {
				all := object(current.Get("pluginSecrets"))
				for _, p := range all.OwnProperties() {
					var key string
					_ = json.Unmarshal(p.NameJSON, &key)
					if key == id || strings.HasPrefix(key, id+"/") {
						all.Delete(key)
					}
				}
				if len(all.OwnProperties()) == 0 {
					current.Delete("pluginSecrets")
				} else {
					current.Set("pluginSecrets", encode(all))
				}
				return nil
			})
			if err != nil {
				s.report(err)
			} else if !result.Success {
				s.report(fmt.Errorf("failed to clear pluginSecrets for %s from secure storage", id))
			}
		}
	}
	s.Clear()
	return nil
}

func jsonPath(parts ...string) []json.RawMessage {
	out := make([]json.RawMessage, len(parts))
	for i, part := range parts {
		out[i], _ = json.Marshal(part)
	}
	return out
}
