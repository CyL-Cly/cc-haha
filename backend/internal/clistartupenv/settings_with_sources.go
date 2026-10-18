package clistartupenv

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// GetSettingsWithSources follows the original fresh-disk settings control.
// The enabled source order and merged object use the existing shared caches.
func (s *Service) GetSettingsWithSources() (protocol.JSONObject, error) {
	if err := s.checkHostControls(); err != nil {
		return protocol.JSONObject{}, err
	}
	return s.getSettingsWithSources()
}

func (s *Service) getSettingsWithSources() (protocol.JSONObject, error) {
	s.options.SettingsState.ResetSettingsCache()
	sources := []protocol.JSONObject{}
	for _, name := range s.EnabledSettingSources() {
		settings, err := s.getSource(name)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		if settings != nil && len(settings.OwnProperties()) > 0 {
			data, err := settings.MarshalJSON()
			if err != nil {
				return protocol.JSONObject{}, err
			}
			sources = append(sources, protocol.NewJSONObject(protocol.JSONField{Name: "source", Value: quote(name)}, protocol.JSONField{Name: "settings", Value: data}))
		}
	}
	effective, err := s.getInitialSettings()
	if err != nil {
		return protocol.JSONObject{}, err
	}
	effectiveJSON, err := effective.MarshalJSON()
	if err != nil {
		return protocol.JSONObject{}, err
	}
	sourcesJSON, err := json.Marshal(sources)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "effective", Value: effectiveJSON}, protocol.JSONField{Name: "sources", Value: sourcesJSON}), nil
}
