package computeruse

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

type object = map[string]json.RawMessage

func encode(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }
func copyObject(value object) object {
	copy := object{}
	for key, raw := range value {
		copy[key] = append(json.RawMessage(nil), raw...)
	}
	return copy
}
func decodeObject(raw json.RawMessage) (object, bool) {
	var value object
	if len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{' || json.Unmarshal(raw, &value) != nil || value == nil {
		return nil, false
	}
	return value, true
}
func stringValue(raw json.RawMessage) (string, bool) {
	var value string
	if len(raw) == 0 || bytes.TrimSpace(raw)[0] != '"' || json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}
func trim(value string) string {
	return strings.TrimFunc(value, func(r rune) bool {
		return r == '\t' || r == '\n' || r == '\v' || r == '\f' || r == '\r' || r == ' ' || r == 0xa0 || r == 0x1680 || (r >= 0x2000 && r <= 0x200a) || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
	})
}
func boolean(raw json.RawMessage) (bool, bool) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("true")) {
		return true, true
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("false")) {
		return false, true
	}
	return false, false
}
func defaultFlags(enabled bool) object {
	return object{"clipboardRead": encode(enabled), "clipboardWrite": encode(enabled), "systemKeyCombos": encode(enabled)}
}
func defaultConfig() object {
	return object{"enabled": encode(false), "authorizedApps": encode([]any{}), "grantFlags": encode(defaultFlags(true)), "pythonPath": encode(nil)}
}
func normalizeApps(raw json.RawMessage) (json.RawMessage, bool) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '[' {
		return nil, false
	}
	var apps []json.RawMessage
	if json.Unmarshal(raw, &apps) != nil {
		return nil, false
	}
	result := []object{}
	for _, raw := range apps {
		app, ok := decodeObject(raw)
		if !ok {
			return nil, false
		}
		id, idOK := stringValue(app["bundleId"])
		name, nameOK := stringValue(app["displayName"])
		if !idOK || !nameOK || trim(id) == "" || trim(name) == "" {
			return nil, false
		}
		if value, found := app["authorizedAt"]; found {
			if _, ok := stringValue(value); !ok {
				return nil, false
			}
		}
		app["bundleId"], app["displayName"] = encode(trim(id)), encode(trim(name))
		result = append(result, app)
	}
	return encode(result), true
}

// Stored documents accept unknown fields. HTTP patches reject unknown fields,
// matching the two separate Source parsers; no schema marker is introduced.
func parseConfig(raw json.RawMessage, patch bool) (object, bool) {
	value, ok := decodeObject(raw)
	if !ok {
		return nil, false
	}
	if patch {
		for key := range value {
			if key != "enabled" && key != "authorizedApps" && key != "grantFlags" && key != "pythonPath" {
				return nil, false
			}
		}
	}
	if raw, found := value["enabled"]; found {
		if _, ok := boolean(raw); !ok {
			return nil, false
		}
	}
	if raw, found := value["authorizedApps"]; found {
		normalized, ok := normalizeApps(raw)
		if !ok {
			return nil, false
		}
		value["authorizedApps"] = normalized
	}
	if raw, found := value["grantFlags"]; found {
		flags, ok := decodeObject(raw)
		if !ok {
			return nil, false
		}
		for key, raw := range flags {
			known := key == "clipboardRead" || key == "clipboardWrite" || key == "systemKeyCombos"
			if patch && !known {
				return nil, false
			}
			if known {
				if _, ok := boolean(raw); !ok {
					return nil, false
				}
			}
		}
	}
	if raw, found := value["pythonPath"]; found {
		if !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			text, ok := stringValue(raw)
			if !ok {
				return nil, false
			}
			if patch {
				text = trim(text)
				if text == "" {
					value["pythonPath"] = encode(nil)
				} else {
					value["pythonPath"] = encode(text)
				}
			}
		}
	}
	return value, true
}
func resolveConfig(stored object) object {
	result := defaultConfig()
	for _, key := range []string{"enabled", "authorizedApps"} {
		if raw, found := stored[key]; found {
			result[key] = append(json.RawMessage(nil), raw...)
		}
	}
	flags := defaultFlags(true)
	if old, ok := decodeObject(stored["grantFlags"]); ok {
		for key, raw := range old {
			flags[key] = raw
		}
	}
	result["grantFlags"] = encode(flags)
	if text, ok := stringValue(stored["pythonPath"]); ok && trim(text) != "" {
		result["pythonPath"] = encode(trim(text))
	}
	return result
}
func (service *Service) ConfigPath() string {
	return filepath.Join(service.options.ConfigDir, "cc-haha", "computer-use-config.json")
}
func (service *Service) readStored() (object, error) {
	raw, err := service.options.Store.FileSystem().ReadFile(service.ConfigPath())
	if errors.Is(err, fs.ErrNotExist) {
		return object{}, nil
	}
	if err != nil {
		return nil, err
	}
	value, ok := parseConfig(raw, false)
	if !ok {
		return nil, errors.New("Computer Use config is invalid")
	}
	return value, nil
}
func (service *Service) Config(ctx context.Context) (object, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stored, err := service.readStored()
	if err != nil {
		return nil, err
	}
	return resolveConfig(stored), nil
}
func (service *Service) UpdateConfig(ctx context.Context, patch json.RawMessage) error {
	parsed, ok := parseConfig(patch, true)
	if !ok {
		return &ConfigError{InvalidPatch: true}
	}
	return service.options.Store.WithWriteLock(service.ConfigPath(), func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		existing, err := service.readStored()
		if err != nil {
			return &ConfigError{Existing: true, Err: err}
		}
		resolved := resolveConfig(existing)
		for key, raw := range parsed {
			if key == "grantFlags" {
				flags, _ := decodeObject(resolved[key])
				next, _ := decodeObject(raw)
				for flag, value := range next {
					flags[flag] = value
				}
				resolved[key] = encode(flags)
			} else {
				resolved[key] = raw
			}
		}
		merged := copyObject(existing)
		for key, raw := range resolved {
			merged[key] = raw
		}
		oldFlags, _ := decodeObject(existing["grantFlags"])
		if oldFlags == nil {
			oldFlags = object{}
		}
		flags, _ := decodeObject(resolved["grantFlags"])
		for key, raw := range flags {
			oldFlags[key] = raw
		}
		merged["grantFlags"] = encode(oldFlags)
		pretty, err := json.MarshalIndent(merged, "", "  ")
		if err != nil {
			return err
		}
		fileSystem := service.options.Store.FileSystem()
		if err = fileSystem.MkdirAll(filepath.Dir(service.ConfigPath()), 0700); err != nil {
			return err
		}
		temporary := service.ConfigPath() + "." + randomID() + ".tmp"
		defer fileSystem.Remove(temporary)
		if err = fileSystem.WriteFile(temporary, pretty, 0600); err == nil {
			err = fileSystem.Rename(temporary, service.ConfigPath())
		}
		return err
	})
}

type ConfigError struct {
	InvalidPatch, Existing bool
	Err                    error
}

func (err *ConfigError) Error() string {
	if err.Err != nil {
		return err.Err.Error()
	}
	return "Invalid Computer Use config"
}
func (err *ConfigError) Unwrap() error { return err.Err }
func invalidConfigResponse(service *Service, err error, update bool) map[string]any {
	prefix := "Computer Use config could not be loaded: "
	if update {
		prefix = "Computer Use config must be repaired before it can be changed: "
	}
	return map[string]any{"error": "COMPUTER_USE_CONFIG_INVALID", "message": prefix + fmt.Sprint(err), "configPath": service.ConfigPath(), "recoveryHint": "删除或修复该文件即可恢复"}
}
