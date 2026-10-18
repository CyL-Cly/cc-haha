package taskstore

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func quote(v string) json.RawMessage {
	return protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(v)))
}
func raw(v any) json.RawMessage   { value, _ := json.Marshal(v); return value }
func emptyArray() json.RawMessage { return json.RawMessage("[]") }

// Source's regex replaces each UTF-16 code unit, including both surrogate
// halves of a non-BMP character, with a dash.
func SanitizePathComponent(v string) string { return sanitizeUnits(utf16.Encode([]rune(v))) }
func sanitizeUnits(units []uint16) string {
	var result strings.Builder
	for _, unit := range units {
		if unit >= 'a' && unit <= 'z' || unit >= 'A' && unit <= 'Z' || unit >= '0' && unit <= '9' || unit == '_' || unit == '-' {
			result.WriteByte(byte(unit))
		} else {
			result.WriteByte('-')
		}
	}
	return result.String()
}
func (s *Service) tasksDir(list string) string {
	return filepath.Join(s.configDirectory(), "tasks", SanitizePathComponent(list))
}
func (s *Service) taskPath(list string, id json.RawMessage) (string, error) {
	units, err := protocol.DecodeJavaScriptJSONString(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.tasksDir(list), sanitizeUnits(units)+".json"), nil
}
func isString(v json.RawMessage) bool {
	_, err := protocol.DecodeJavaScriptJSONString(v)
	return err == nil
}
func stringArray(v json.RawMessage) bool {
	v = bytes.TrimSpace(v)
	var values []json.RawMessage
	if len(v) == 0 || v[0] != '[' || json.Unmarshal(v, &values) != nil {
		return false
	}
	for _, value := range values {
		if !isString(value) {
			return false
		}
	}
	return true
}

func parseTask(value json.RawMessage, internal bool) *protocol.JSONObject {
	var input protocol.JSONObject
	if input.UnmarshalJSON(value) != nil || !input.IsObject() {
		return nil
	}
	status := input.String("status")
	if internal {
		switch status {
		case "open":
			status = "pending"
		case "resolved":
			status = "completed"
		case "planning", "implementing", "reviewing", "verifying":
			status = "in_progress"
		}
		input.Set("status", quote(status))
	}
	if status != "pending" && status != "in_progress" && status != "completed" {
		return nil
	}
	out := protocol.NewJSONObject()
	for _, name := range []string{"id", "subject", "description", "activeForm", "owner", "status", "blocks", "blockedBy", "metadata"} {
		value := input.Get(name)
		if value == nil && (name == "activeForm" || name == "owner" || name == "metadata") {
			continue
		}
		switch name {
		case "blocks", "blockedBy":
			if !stringArray(value) {
				return nil
			}
		case "metadata":
			var metadata protocol.JSONObject
			if metadata.UnmarshalJSON(value) != nil || !metadata.IsObject() {
				return nil
			}
			metadata.Delete("__proto__")
			value = raw(metadata)
		default:
			if !isString(value) {
				return nil
			}
		}
		out.Set(name, value)
	}
	return &out
}

// Source treats every failed or invalid task read as a missing task.
func (s *Service) Get(list string, id json.RawMessage) (*protocol.JSONObject, error) {
	path, err := s.taskPath(list, id)
	if err != nil {
		return nil, err
	}
	value, err := os.ReadFile(path)
	if err != nil {
		return nil, nil
	}
	return parseTask(value, s.options.UserType == "ant"), nil
}

func (s *Service) list(list string) ([]protocol.JSONObject, error) {
	files, err := os.ReadDir(s.tasksDir(list))
	if err != nil {
		return []protocol.JSONObject{}, nil
	}
	result := []protocol.JSONObject{}
	for _, file := range files {
		name := file.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		id := strings.Replace(name, ".json", "", 1)
		task, err := s.Get(list, quote(id))
		if err != nil {
			return nil, err
		}
		if task != nil {
			result = append(result, *task)
		}
	}
	return result, nil
}

func trim(v string) string {
	return strings.Trim(v, "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff")
}
func decimalPrefix(v string) float64 {
	v = trim(v)
	i := 0
	if len(v) > 0 && (v[0] == '-' || v[0] == '+') {
		i++
	}
	start := i
	for i < len(v) && v[i] >= '0' && v[i] <= '9' {
		i++
	}
	if start == i {
		return math.NaN()
	}
	value, _ := strconv.ParseFloat(v[:i], 64)
	return value
}
func numberText(v float64) string {
	if math.IsInf(v, 1) {
		return "Infinity"
	}
	if math.IsInf(v, -1) {
		return "-Infinity"
	}
	if math.IsNaN(v) {
		return "NaN"
	}
	return string(protocol.StringifyValidatedJavaScriptJSON(raw(v)))
}
func jsonEqual(a, b json.RawMessage) bool {
	if len(a) == 0 || len(b) == 0 {
		return len(a) == len(b)
	}
	return bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(a), protocol.StringifyValidatedJavaScriptJSON(b))
}
func (s *Service) writeTask(list string, id json.RawMessage, task protocol.JSONObject) error {
	path, err := s.taskPath(list, id)
	if err != nil {
		return err
	}
	canonical := protocol.StringifyValidatedJavaScriptJSON(raw(task))
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, canonical, "", "  "); err != nil {
		return err
	}
	if err := os.WriteFile(path, formatted.Bytes(), 0666); err != nil {
		return err
	}
	s.notify()
	return nil
}

func (s *Service) notify() {
	s.publishUpdates()
	if s.options.OnUpdate != nil {
		s.options.OnUpdate()
	}
}
