package usercontext

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

var Modes = []string{"claude-md", "claude-md-or-agents-md", "claude-md-and-agents-md", "managed-only"}

const Plugin = "agents-md@builtin"

func quote(value string) json.RawMessage { raw, _ := json.Marshal(value); return raw }
func concat(values ...json.RawMessage) json.RawMessage {
	units := []uint16{}
	for _, value := range values {
		part, _ := protocol.DecodeJavaScriptJSONString(value)
		units = append(units, part...)
	}
	return protocol.QuoteJavaScriptUTF16(units)
}
func rawString(value json.RawMessage) string {
	units, _ := protocol.DecodeJavaScriptJSONString(value)
	return string(utf16.Decode(units))
}
func equalString(raw json.RawMessage, value string) bool {
	units, err := protocol.DecodeJavaScriptJSONString(raw)
	if err != nil {
		return false
	}
	return bytes.Equal(protocol.QuoteJavaScriptUTF16(units), quote(value))
}
func truthy(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) || bytes.Equal(raw, []byte("false")) {
		return false
	}
	if raw[0] == '"' {
		units, _ := protocol.DecodeJavaScriptJSONString(raw)
		return len(units) > 0
	}
	if raw[0] == '{' || raw[0] == '[' || bytes.Equal(raw, []byte("true")) {
		return true
	}
	number, _ := strconv.ParseFloat(string(raw), 64)
	return number != 0 && !math.IsNaN(number)
}
func envTruthy(raw json.RawMessage) (bool, error) {
	if !truthy(raw) {
		return false, nil
	}
	if bytes.Equal(raw, []byte("true")) {
		return true, nil
	}
	if len(raw) == 0 || raw[0] != '"' {
		return false, &CapabilityError{"isEnvTruthy non-string/non-boolean environment"}
	}
	value := strings.TrimFunc(strings.ToLower(rawString(raw)), func(r rune) bool {
		return r >= 9 && r <= 13 || r == ' ' || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
	})
	return value == "1" || value == "true" || value == "yes" || value == "on", nil
}
func localDate(now time.Time) string {
	return fmt.Sprintf("%d-%02d-%02d", now.Year(), now.Month(), now.Day())
}
func NormalizeInstructionFilesMode(value json.RawMessage) string {
	for _, mode := range Modes {
		if equalString(value, mode) {
			return mode
		}
	}
	return "claude-md-or-agents-md"
}
func object(raw json.RawMessage) protocol.JSONObject {
	var out protocol.JSONObject
	_ = json.Unmarshal(raw, &out)
	return out
}

// Sources are original parsed configuration in descending precedence, never a
// Source-resolved mode. An own undefined option does not override lower input.
func InstructionFilesModeFromSettings(sources ...*protocol.JSONObject) string {
	options := protocol.NewJSONObject()
	for index := len(sources) - 1; index >= 0; index-- {
		if sources[index] == nil {
			continue
		}
		plugin := object(object(sources[index].Get("pluginConfigs")).Get(Plugin))
		for _, property := range object(plugin.Get("options")).OwnProperties() {
			if property.Value != nil {
				_ = options.SetPropertyJSON(property.NameJSON, property.Value)
			}
		}
	}
	mode := NormalizeInstructionFilesMode(options.Get("instructionFiles"))
	if mode != "claude-md-or-agents-md" || options.Get("projectInstructions") == nil {
		return mode
	}
	switch raw := options.Get("projectInstructions"); {
	case equalString(raw, "none"):
		return "managed-only"
	case equalString(raw, "both"):
		return "claude-md-and-agents-md"
	case equalString(raw, "agents-fallback"):
		return mode
	default:
		return "claude-md"
	}
}
func (s *Service) InstructionFilesMode() (string, error) {
	// The external producer is absent or an actual empty parsed policy. Resume
	// retains an empty object in its cache; its reference must remain intact.
	if policy, ok := s.options.SettingsState.GetCachedSettingsForSource("policySettings"); ok && policy != nil && (!policy.IsObject() || len(policy.OwnProperties()) != 0) {
		return "", &CapabilityError{"managed policy contradicts absent profile"}
	}
	flag, ok := s.options.SettingsState.GetCachedSettingsForSource("flagSettings")
	if !ok {
		s.mu.Lock()
		originalFlag := s.options.FlagSettings
		s.mu.Unlock()
		if originalFlag != nil {
			raw, err := originalFlag.MarshalJSON()
			if err != nil {
				return "", err
			}
			parsed, err := clisettings.ValidateJSONContent(string(raw), "inline settings")
			if err != nil {
				return "", err
			}
			flag = parsed.Settings
		}
		s.options.SettingsState.SetCachedSettingsForSource("flagSettings", flag)
	}
	user, err := s.options.Settings.GetEditableSource("userSettings", "")
	if err != nil {
		return "", err
	}
	return InstructionFilesModeFromSettings(nil, flag, user), nil
}
