package clistartupenv

import (
	"encoding/json"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/diagnostics"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func validSource(source string) bool {
	return source == "userSettings" || source == "projectSettings" || source == "localSettings" || source == "flagSettings" || source == "policySettings"
}
func (s *Service) EnabledSettingSources() []string {
	allowed := s.options.Host.AllowedSources
	if allowed == nil {
		allowed = []string{"userSettings", "projectSettings", "localSettings", "flagSettings", "policySettings"}
	}
	result := []string{}
	seen := map[string]bool{}
	for _, source := range append(append([]string{}, allowed...), "policySettings", "flagSettings") {
		if !seen[source] {
			result = append(result, source)
			seen[source] = true
		}
	}
	return result
}
func (s *Service) enabled(source string) bool {
	for _, value := range s.EnabledSettingSources() {
		if value == source {
			return true
		}
	}
	return false
}
func (s *Service) checkUserPath() error {
	cowork, err := envTruthy(s.options.Environment.Get("CLAUDE_CODE_USE_COWORK_PLUGINS"))
	if err != nil {
		return err
	}
	if cowork {
		return &CapabilityError{"cowork user settings path"}
	}
	return nil
}
func (s *Service) inline() (*protocol.JSONObject, error) {
	raw := s.options.Host.FlagInline
	if raw == nil {
		return nil, nil
	}
	parsed, err := s.options.Settings.ValidateJSONContent(string(raw), "inline settings")
	if err != nil {
		return nil, err
	}
	return parsed.Settings, nil
}
func (s *Service) getSource(source string) (*protocol.JSONObject, error) {
	if source == "userSettings" {
		if err := s.checkUserPath(); err != nil {
			return nil, err
		}
	}
	if cached, ok := s.options.SettingsState.GetCachedSettingsForSource(source); ok {
		return cached, nil
	}
	if source != "flagSettings" && source != "policySettings" {
		return s.options.Settings.GetEditableSource(source, "")
	}
	var result *protocol.JSONObject
	if source == "policySettings" && s.options.Host.Profile == ResolvedPolicyReleaseProfile {
		var err error
		result, err = s.options.Host.ResolvePolicy()
		if err != nil {
			return nil, err
		}
	}
	if source == "flagSettings" {
		if path := s.options.Host.FlagPath; path != nil {
			parsed, err := s.options.Settings.ParseFile(*path)
			if err != nil {
				return nil, err
			}
			result = parsed.Settings
		}
		inline, err := s.inline()
		if err != nil {
			return nil, err
		}
		if inline != nil {
			object := protocol.NewJSONObject()
			if result != nil {
				object = result.Clone()
			}
			if e := mergeSettings(&object, *inline); e != nil {
				return nil, e
			}
			result = &object
		}
	}
	s.options.SettingsState.SetCachedSettingsForSource(source, result)
	return result, nil
}

// GetInitialSettings exposes the existing complete merged settings cache.
// It does not apply its environment or allocate another settings lifecycle.
// Returned cached objects retain Source getter identity; session owner controls
// mutation and invalidates through the existing clisettings.State.
func (s *Service) GetInitialSettings() (*protocol.JSONObject, error) {
	if err := s.checkHostControls(); err != nil {
		return nil, err
	}
	if _, err := s.mergedEnv(); err != nil {
		return nil, err
	}
	if cached := s.options.SettingsState.GetSessionSettingsCache(); cached != nil && cached.Settings != nil {
		return cached.Settings, nil
	}
	// Source getInitialSettings uses settings || {} for a cached absent value.
	empty := protocol.NewJSONObject()
	return &empty, nil
}

// The CLI settings service supplies finite whole-schema validation. The
// shared session cache retains the complete merged supported/unknown JSON,
// although this component consumes only its environment projection.
func (s *Service) mergedEnv() (json.RawMessage, error) {
	if cached := s.options.SettingsState.GetSessionSettingsCache(); cached != nil {
		return envFrom(cached.Settings), nil
	}

	start := s.options.NowMillis()
	if s.options.Diagnostics != nil {
		if err := s.options.Diagnostics.Log("info", "settings_load_started", nil); err != nil {
			return nil, err
		}
	}
	result := protocol.NewJSONObject()
	if plugin := s.options.SettingsState.GetPluginSettingsBase(); plugin != nil {
		if s.options.Host.PluginBaseAbsent || !s.options.Plugins.SharesSettingsState(s.options.SettingsState) {
			return nil, &CapabilityError{"unbound plugin settings producer"}
		}
		if err := mergeSettings(&result, *plugin); err != nil {
			return nil, err
		}
	}
	seen := map[string]bool{}
	allErrors := []json.RawMessage{}
	seenErrors := map[string]bool{}
	for _, source := range s.EnabledSettingSources() {
		if source == "policySettings" {
			if s.options.Host.Profile == ResolvedPolicyReleaseProfile {
				policy, err := s.getSource(source)
				if err != nil {
					return nil, err
				}
				if policy != nil {
					if err = mergeSettings(&result, *policy); err != nil {
						return nil, err
					}
				}
			}
			continue
		}
		var path string
		if source == "userSettings" {
			if err := s.checkUserPath(); err != nil {
				return nil, err
			}
		}
		if source == "flagSettings" {
			if s.options.Host.FlagPath != nil {
				path = *s.options.Host.FlagPath
			}
		} else {
			value, err := s.options.Settings.Path(source, "")
			if err != nil {
				return nil, err
			}
			path = value
		}
		if path != "" && !seen[filepath.Clean(path)] {
			seen[filepath.Clean(path)] = true
			parsed, err := s.options.Settings.ParseFile(path)
			if err != nil {
				return nil, err
			}
			for _, raw := range parsed.Errors {
				key := string(raw)
				if !seenErrors[key] {
					seenErrors[key] = true
					allErrors = append(allErrors, raw)
				}
			}
			if parsed.Settings != nil {
				if err = mergeSettings(&result, *parsed.Settings); err != nil {
					return nil, err
				}
			}
		}
		if source == "flagSettings" {
			inline, err := s.inline()
			if err != nil {
				return nil, err
			}
			if inline != nil {
				if err = mergeSettings(&result, *inline); err != nil {
					return nil, err
				}
			}
		}
	}

	duration := s.options.NowMillis() - start
	if s.options.Diagnostics != nil {
		data := protocol.NewJSONObject(
			protocol.JSONField{Name: "duration_ms", Value: diagnostics.Number(duration)},
			protocol.JSONField{Name: "source_count", Value: diagnostics.Number(float64(len(seen)))},
			protocol.JSONField{Name: "error_count", Value: diagnostics.Number(float64(len(allErrors)))},
		)
		if err := s.options.Diagnostics.Log("info", "settings_load_completed", &data); err != nil {
			return nil, err
		}
	}
	s.options.SettingsState.SetSessionSettingsCache(&clisettings.ParsedSettings{Settings: &result, Errors: allErrors})
	return result.Get("env"), nil
}
