package clisettings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// PermissionEditingBindings is a separate native editing authority. It never
// turns opaque saved fields into executable settings or relaxes finite reads.
type PermissionEditingBindings struct{ StartLocalIgnore func(string, string) error }

// ReadPermissionValidatedSource is an editing-only view of the source schema.
// It filters original invalid rule strings before using the existing native
// SettingsSchema projection. It does not install those rules in execution.
func (s *Service) ReadPermissionValidatedSource(source string) (*protocol.JSONObject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readPermissionValidatedSource(source)
}

func (s *Service) readPermissionValidatedSource(source string) (*protocol.JSONObject, error) {
	if s.options.Profile != NativeProcessProfile {
		return nil, &CapabilityError{"native permission editing profile"}
	}
	path, e := s.Path(source, "")
	if e != nil {
		return nil, e
	}
	content, e := readContent(s.options.FS, path)
	if e != nil {
		if os.IsNotExist(e) {
			return nil, nil
		}
		return nil, e
	}
	data := parseJSONContent(content)
	if trimJS(content) == "" {
		data = []byte("{}")
	}
	var input protocol.JSONObject
	if json.Unmarshal(data, &input) != nil || !input.IsObject() {
		return nil, nil
	}
	var permissions protocol.JSONObject
	originalRules := protocol.NewJSONObject()
	if json.Unmarshal(input.Get("permissions"), &permissions) == nil && permissions.IsObject() {
		for _, behavior := range []string{"allow", "deny", "ask"} {
			value := permissions.Get(behavior)
			if value == nil {
				continue
			}
			var rows []json.RawMessage
			if json.Unmarshal(value, &rows) != nil {
				continue
			}
			filtered := []json.RawMessage{}
			validationRows := []json.RawMessage{}
			for _, row := range rows {
				units, e := protocol.DecodeJavaScriptJSONString(row)
				if e != nil {
					continue
				}
				valid := validPermissionEditingRule(units)
				for i := 0; i+1 < len(units); i++ {
					if units[i] == '(' && units[i+1] == ')' {
						slashes := 0
						for j := i - 1; j >= 0 && units[j] == '\\'; j-- {
							slashes++
						}
						if slashes%2 == 0 {
							valid = false
						}
					}
				}
				if valid {
					filtered = append(filtered, row)
					// The execution parser keeps its strict UTF-16 boundary. This
					// editing-only schema probe uses replacement characters and
					// restores the original rule JSON after schema ordering.
					validationRows = append(validationRows, permissionEditingValidationString(units))
				}
			}
			raw, _ := json.Marshal(filtered)
			originalRules.Set(behavior, raw)
			raw, _ = json.Marshal(validationRows)
			permissions.Set(behavior, raw)
		}
		raw, _ := permissions.MarshalJSON()
		input.Set("permissions", raw)
	}
	raw, _ := input.MarshalJSON()
	parsed, e := ValidateNativeJSONContent(string(raw), path)
	if e != nil {
		return nil, e
	}
	if parsed.Settings != nil && len(originalRules.OwnProperties()) > 0 {
		var final protocol.JSONObject
		_ = final.UnmarshalJSON(parsed.Settings.Get("permissions"))
		for _, property := range originalRules.OwnProperties() {
			_ = final.SetPropertyJSON(property.NameJSON, property.Value)
		}
		raw, _ := final.MarshalJSON()
		parsed.Settings.Set("permissions", raw)
	}
	return parsed.Settings, nil
}

func validPermissionEditingRule(units []uint16) bool {
	probe, _ := protocol.DecodeJavaScriptJSONString(permissionEditingValidationString(units))
	decoded := string(utf16.Decode(probe))
	if !validNativeRule(decoded) {
		return false
	}
	// Source MCP validation precedes uppercase validation.
	if strings.HasPrefix(decoded, "mcp__") {
		return true
	}
	if len(units) > 0 && (units[0] < 0xd800 || units[0] > 0xdfff) {
		first := string(rune(units[0]))
		if cases.Upper(language.Und).String(first) != first {
			return false
		}
	}
	return true
}

func permissionEditingValidationString(units []uint16) json.RawMessage {
	probe := append([]uint16(nil), units...)
	for i, unit := range probe {
		if unit >= 0xd800 && unit <= 0xdbff && (i+1 == len(probe) || probe[i+1] < 0xdc00 || probe[i+1] > 0xdfff) || unit >= 0xdc00 && unit <= 0xdfff && (i == 0 || probe[i-1] < 0xd800 || probe[i-1] > 0xdbff) {
			probe[i] = 0xfffd
		}
	}
	// JavaScript validates tool-name case with its first UTF-16 unit. An
	// astral letter starts with a surrogate, whose uppercase is itself.
	if len(units) > 1 && units[0] >= 0xd800 && units[0] <= 0xdbff && units[1] >= 0xdc00 && units[1] <= 0xdfff {
		probe[0], probe[1] = 0xfffd, 0xfffd
	}
	return protocol.QuoteJavaScriptUTF16(probe)
}

func (s *Service) ReadPermissionEditingSource(source string) (*protocol.JSONObject, error) {
	if s.options.Profile != NativeProcessProfile {
		return nil, &CapabilityError{"native permission editing profile"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path, e := s.Path(source, "")
	if e != nil {
		return nil, e
	}
	content, e := readContent(s.options.FS, path)
	if e != nil {
		if os.IsNotExist(e) {
			return nil, nil
		}
		return nil, e
	}
	if trimJS(content) == "" {
		o := protocol.NewJSONObject()
		return &o, nil
	}
	data := parseJSONContent(content)
	var o protocol.JSONObject
	if json.Unmarshal(data, &o) != nil || !o.IsObject() {
		return nil, nil
	}
	return &o, nil
}

func (s *Service) UpdatePermissionEditingSource(source string, patch protocol.JSONObject, bindings PermissionEditingBindings) (WriteResult, error) {
	if s.options.Profile != NativeProcessProfile {
		return WriteResult{}, &CapabilityError{"native permission editing profile"}
	}
	if source != "userSettings" && source != "projectSettings" && source != "localSettings" {
		return WriteResult{}, nil
	}
	if source == "localSettings" && bindings.StartLocalIgnore == nil {
		return WriteResult{}, &CapabilityError{"original local permission Git-ignore producer"}
	}
	if !patch.IsObject() {
		return WriteResult{}, errors.New("permission editing requires original object patch")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path, e := s.Path(source, "")
	if e != nil {
		return WriteResult{}, e
	}
	fail := func(e error) (WriteResult, error) {
		error := fmt.Errorf("Failed to read raw settings from %s: Error: %s", path, e)
		if s.options.Diagnostic != nil {
			s.options.Diagnostic(quoted("Error: " + error.Error()))
		}
		return WriteResult{Error: error}, nil
	}
	if e = s.options.FS.MkdirAll(filepath.Dir(path)); e != nil {
		return fail(e)
	}
	var existing *protocol.JSONObject
	// The uncached editing schema is tried first, just like Source. Existing
	// execution capability guards remain intact; editing preserves opaque data.
	parsed, parseError := s.readPermissionValidatedSource(source)
	if parseError == nil {
		existing = parsed
	}
	if existing == nil {
		content, e := readContent(s.options.FS, path)
		if e != nil && !os.IsNotExist(e) {
			return fail(e)
		}
		if e == nil {
			data := parseJSONContent(content)
			if data == nil || bytes.Equal(data, []byte("null")) {
				return WriteResult{Error: fmt.Errorf("Invalid JSON syntax in settings file at %s", path)}, nil
			}
			var o protocol.JSONObject
			if e = json.Unmarshal(data, &o); e != nil || !o.IsObject() {
				return WriteResult{}, &CapabilityError{"raw scalar/array permission editing target"}
			}
			existing = &o
		}
	}
	if existing == nil {
		o := protocol.NewJSONObject()
		existing = &o
	}
	// The original structured merge replaces arrays and preserves own-property
	// order. No execution/profile mutation is performed by this editing route.
	if e = mergeObjects(existing, patch.Clone()); e != nil {
		return WriteResult{}, e
	}
	data, _ := existing.MarshalJSON()
	var pretty bytes.Buffer
	if e = json.Indent(&pretty, protocol.StringifyValidatedJavaScriptJSON(data), "", "  "); e != nil {
		return fail(e)
	}
	pretty.WriteByte('\n')
	s.emit(Effect{Kind: "mark", Path: path})
	s.options.State.MarkInternalWrite(path)
	if e = s.writePhysical(path, pretty.Bytes()); e != nil {
		return fail(e)
	}
	s.emit(Effect{Kind: "reset"})
	s.options.State.ResetSettingsCache()
	if source == "localSettings" {
		if e = bindings.StartLocalIgnore(s.options.OriginalCWD, ".claude/settings.local.json"); e != nil && s.options.Diagnostic != nil {
			s.options.Diagnostic(quoted("Error: " + e.Error()))
		}
	}
	return WriteResult{}, nil
}
