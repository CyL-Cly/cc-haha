// Package automode ports the CLI classifier-rules handlers. Startup, settings
// file loading, and classifier model execution remain host responsibilities.
package automode

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// This is the original prompt resource, not a captured defaults response.
// Parity checks compare its bytes with the Source resource before execution.
//
//go:embed permissions_external.txt
var externalTemplate string

func Template() []byte { return []byte(externalTemplate) }

type Rules struct {
	Allow       []json.RawMessage
	SoftDeny    []json.RawMessage
	HardDeny    []json.RawMessage
	Environment []json.RawMessage
}

// Config keeps absent (nil) and configured-empty sections distinct. Quoted
// JSON strings retain the Source's UTF-16 identities, including lone units.
type Config struct {
	Rules
	ClassifyAllShell *bool
}

type Snapshot struct {
	TranscriptClassifier bool
	AnthropicInternal    bool
	// Sources are the host's parsed settings for each source, before the
	// autoMode getter. The getter intentionally never trusts projectSettings.
	Sources map[string]json.RawMessage
}

func emptyRules() Rules {
	return Rules{Allow: []json.RawMessage{}, SoftDeny: []json.RawMessage{}, HardDeny: []json.RawMessage{}, Environment: []json.RawMessage{}}
}

func trimECMAScript(value string) string {
	return strings.TrimFunc(value, func(r rune) bool {
		return r >= 0x9 && r <= 0xd || r == 0x20 || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
	})
}

func extract(template, tag string) []json.RawMessage {
	match := regexp.MustCompile(`<` + regexp.QuoteMeta(tag) + `>([\s\S]*?)</` + regexp.QuoteMeta(tag) + `>`).FindStringSubmatch(template)
	result := []json.RawMessage{}
	if len(match) == 0 {
		return result
	}
	for _, line := range strings.Split(match[1], "\n") {
		line = trimECMAScript(line)
		if strings.HasPrefix(line, "- ") {
			result = append(result, protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(line[2:]))))
		}
	}
	return result
}

func Defaults(transcriptClassifier bool) Rules {
	if !transcriptClassifier {
		return emptyRules()
	}
	return Rules{
		Allow:       extract(externalTemplate, "user_allow_rules_to_replace"),
		SoftDeny:    extract(externalTemplate, "user_deny_rules_to_replace"),
		HardDeny:    extract(externalTemplate, "user_hard_deny_rules_to_replace"),
		Environment: extract(externalTemplate, "user_environment_to_replace"),
	}
}

func array(raw json.RawMessage) ([]json.RawMessage, bool) {
	if len(raw) == 0 || bytes.TrimSpace(raw)[0] != '[' {
		return nil, false
	}
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) != nil {
		return nil, false
	}
	result := make([]json.RawMessage, 0, len(values))
	for _, value := range values {
		units, err := protocol.DecodeJavaScriptJSONString(value)
		if err != nil {
			return nil, false
		}
		result = append(result, protocol.QuoteJavaScriptUTF16(units))
	}
	return result, true
}

func configured(raw json.RawMessage) (map[string][]json.RawMessage, *bool, bool) {
	var object protocol.JSONObject
	if object.UnmarshalJSON(raw) != nil || !object.IsObject() {
		return nil, nil, false
	}
	sections := map[string][]json.RawMessage{}
	for _, key := range []string{"allow", "soft_deny", "hard_deny", "deny", "environment"} {
		if value := object.Get(key); value != nil {
			parsed, ok := array(value)
			if !ok {
				return nil, nil, false // Zod rejects the complete source object.
			}
			sections[key] = parsed
		}
	}
	var classify *bool
	if value := object.Get("classifyAllShell"); value != nil {
		value = bytes.TrimSpace(value)
		if !bytes.Equal(value, []byte("true")) && !bytes.Equal(value, []byte("false")) {
			return nil, nil, false
		}
		parsed := bytes.Equal(value, []byte("true"))
		classify = &parsed
	}
	return sections, classify, true
}

// MergeTrusted follows getAutoModeConfig's fixed trust order and whole-source
// validation. Settings-source enablement/filtering occurs in the host loader.
func MergeTrusted(snapshot Snapshot) *Config {
	if !snapshot.TranscriptClassifier {
		return nil
	}
	result := &Config{}
	for _, source := range []string{"userSettings", "localSettings", "flagSettings", "policySettings"} {
		var settings protocol.JSONObject
		if settings.UnmarshalJSON(snapshot.Sources[source]) != nil || !settings.IsObject() {
			continue
		}
		sections, classify, ok := configured(settings.Get("autoMode"))
		if !ok {
			continue
		}
		for _, section := range []struct {
			name   string
			target *[]json.RawMessage
		}{
			{"allow", &result.Allow}, {"soft_deny", &result.SoftDeny}, {"hard_deny", &result.HardDeny}, {"environment", &result.Environment},
		} {
			if values, present := sections[section.name]; present {
				if *section.target == nil {
					*section.target = []json.RawMessage{}
				}
				*section.target = append(*section.target, values...)
			}
		}
		if snapshot.AnthropicInternal {
			if values, present := sections["deny"]; present {
				if result.SoftDeny == nil {
					result.SoftDeny = []json.RawMessage{}
				}
				result.SoftDeny = append(result.SoftDeny, values...)
			}
		}
		if classify != nil {
			result.ClassifyAllShell = classify
		}
	}
	if result.Allow == nil && result.SoftDeny == nil && result.HardDeny == nil && result.Environment == nil && result.ClassifyAllShell == nil {
		return nil
	}
	return result
}

func resolve(configured, defaults []json.RawMessage) []json.RawMessage {
	if configured == nil {
		configured = []json.RawMessage{json.RawMessage(`"$defaults"`)}
	}
	result := []json.RawMessage{}
	for _, value := range configured {
		if bytes.Equal(value, []byte(`"$defaults"`)) {
			for _, item := range defaults {
				result = append(result, append(json.RawMessage(nil), item...))
			}
		} else {
			result = append(result, append(json.RawMessage(nil), value...))
		}
	}
	return result
}

func Effective(snapshot Snapshot) Rules {
	defaults := Defaults(snapshot.TranscriptClassifier)
	config := MergeTrusted(snapshot)
	if config == nil {
		return defaults
	}
	return Rules{Allow: resolve(config.Allow, defaults.Allow), SoftDeny: resolve(config.SoftDeny, defaults.SoftDeny), HardDeny: resolve(config.HardDeny, defaults.HardDeny), Environment: resolve(config.Environment, defaults.Environment)}
}

// WriteRules preserves JSON.stringify(..., null, 2), key order and newline.
func WriteRules(writer io.Writer, rules Rules) error {
	object := protocol.NewJSONObject()
	for _, section := range []struct {
		name   string
		values []json.RawMessage
	}{
		{"allow", rules.Allow}, {"soft_deny", rules.SoftDeny}, {"hard_deny", rules.HardDeny}, {"environment", rules.Environment},
	} {
		var raw bytes.Buffer
		raw.WriteByte('[')
		for index, value := range section.values {
			if index > 0 {
				raw.WriteByte(',')
			}
			raw.Write(value)
		}
		raw.WriteByte(']')
		object.Set(section.name, raw.Bytes())
	}
	raw, err := object.MarshalJSON()
	if err != nil {
		return err
	}
	raw, err = protocol.ParseAndStringifyJavaScriptJSON(raw)
	if err != nil {
		return err
	}
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, raw, "", "  "); err != nil {
		return err
	}
	formatted.WriteByte('\n')
	_, err = writer.Write(formatted.Bytes())
	return err
}

func DefaultsHandler(writer io.Writer, transcriptClassifier bool) error {
	return WriteRules(writer, Defaults(transcriptClassifier))
}
func ConfigHandler(writer io.Writer, snapshot Snapshot) error {
	return WriteRules(writer, Effective(snapshot))
}
