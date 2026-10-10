package cliglobalconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"strconv"
	"strings"
	"unicode/utf16"
)

// This is the Source factory's literal recipe, not an observed response.
// Fresh objects include the two own undefined slots; Set later must preserve
// those positions even though JSON.stringify initially omits their values.
func DefaultFactory() *protocol.JSONObject {
	fields := []protocol.JSONField{{Name: "numStartups", Value: []byte("0")}, {Name: "installMethod"}, {Name: "autoUpdates"}}
	fields = append(fields, protocol.JSONField{Name: "theme", Value: []byte(`"dark"`)}, protocol.JSONField{Name: "preferredNotifChannel", Value: []byte(`"auto"`)}, protocol.JSONField{Name: "verbose", Value: []byte("false")}, protocol.JSONField{Name: "editorMode", Value: []byte(`"normal"`)})
	for _, field := range []struct{ name, value string }{{"autoCompactEnabled", "true"}, {"showTurnDuration", "true"}, {"hasSeenTasksHint", "false"}, {"hasUsedStash", "false"}, {"hasUsedBackgroundTask", "false"}, {"queuedCommandUpHintCount", "0"}, {"diffTool", `"auto"`}, {"customApiKeyResponses", `{"approved":[],"rejected":[]}`}, {"env", "{}"}, {"tipsHistory", "{}"}, {"memoryUsageCount", "0"}, {"promptQueueUseCount", "0"}, {"btwUseCount", "0"}, {"todoFeatureEnabled", "true"}, {"showExpandedTodos", "false"}, {"messageIdleNotifThresholdMs", "60000"}, {"autoConnectIde", "false"}, {"autoInstallIdeExtension", "true"}, {"fileCheckpointingEnabled", "true"}, {"terminalProgressBarEnabled", "true"}, {"cachedStatsigGates", "{}"}, {"cachedDynamicConfigs", "{}"}, {"cachedGrowthBookFeatures", "{}"}, {"respectGitignore", "true"}, {"copyFullResponse", "false"}} {
		fields = append(fields, protocol.JSONField{Name: field.name, Value: []byte(field.value)})
	}
	object := protocol.NewJSONObject(fields...)
	return &object
}
func jsTruthy(raw json.RawMessage) bool {
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
	return number != 0
}
func rawString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}
func migrateConfigFields(config *protocol.JSONObject) {
	if config.Get("installMethod") != nil {
		return
	}
	method := "unknown"
	updates := config.Get("autoUpdates")
	if updates == nil || bytes.Equal(bytes.TrimSpace(updates), []byte("null")) {
		updates = []byte("true")
	}
	switch rawString(config.Get("autoUpdaterStatus")) {
	case "migrated":
		method = "local"
	case "installed":
		method = "native"
	case "disabled":
		updates = []byte("false")
	case "enabled", "no_permissions", "not_configured":
		method = "global"
	}
	quoted, _ := json.Marshal(method)
	config.Set("installMethod", quoted)
	config.Set("autoUpdates", updates)
}
func parseSpread(raw []byte, watch bool) (*protocol.JSONObject, error) {
	decoded := string(storage.DecodeUTF8(raw))
	decoded = strings.TrimPrefix(decoded, "\ufeff")
	if message, invalid := provider.PinnedJavaScriptJSONSyntaxMessage(decoded); invalid {
		return nil, &ConfigParseError{MessageJSON: message, DefaultConfig: DefaultFactory()}
	}
	raw = []byte(strings.TrimSpace(decoded))
	if !json.Valid(raw) {
		return nil, errors.New("unsupported pinned JSON input")
	}
	config := DefaultFactory()
	if watch && raw[0] != '{' && raw[0] != '[' {
		return nil, errors.New("watcher requires non-null object")
	}
	spread, err := enumerableProperties(raw)
	if err != nil {
		return nil, err
	}
	config.Assign(spread)
	return config, nil
}

// The JSON-only equivalent of object spread/Object.entries. Strings enumerate
// UTF-16 code units; arrays enumerate their existing index slots; other JSON
// primitives contribute no enumerable properties.
func enumerableProperties(raw json.RawMessage) (protocol.JSONObject, error) {
	raw = bytes.TrimSpace(raw)
	spread := protocol.NewJSONObject()
	if len(raw) == 0 {
		return spread, nil
	}
	switch raw[0] {
	case '{':
		if err := spread.UnmarshalJSON(raw); err != nil {
			return spread, err
		}
	case '[':
		var values []json.RawMessage
		_ = json.Unmarshal(raw, &values)
		for index, value := range values {
			spread.Set(strconv.Itoa(index), value)
		}
	case '"':
		units, _ := protocol.DecodeJavaScriptJSONString(raw)
		for index, unit := range units {
			spread.Set(strconv.Itoa(index), protocol.QuoteJavaScriptUTF16([]uint16{unit}))
		}
	}
	return spread, nil
}
func filterDefaults(config *protocol.JSONObject) ([]byte, error) {
	raw, err := config.MarshalJSON()
	if err != nil {
		return nil, err
	}
	defaults := DefaultFactory()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	_, _ = decoder.Token()
	output := []byte{'{'}
	for decoder.More() {
		start := decoder.InputOffset()
		token, _ := decoder.Token()
		key := raw[start:decoder.InputOffset()]
		key = key[bytes.IndexByte(key, '"'):]
		var value json.RawMessage
		_ = decoder.Decode(&value)
		// lodash pickBy delegates to baseSet, which deliberately rejects these
		// three path keys. Preserve the actual persistence behavior, including
		// the separate write-through cache that still retains those fields.
		if token == "__proto__" || token == "constructor" || token == "prototype" {
			continue
		}
		defaultValue := defaults.Get(token.(string))
		left := protocol.StringifyValidatedJavaScriptJSON(value)
		var right []byte
		if defaultValue != nil {
			right = protocol.StringifyValidatedJavaScriptJSON(defaultValue)
		}
		if bytes.Equal(left, right) {
			continue
		}
		if len(output) > 1 {
			output = append(output, ',')
		}
		output = append(output, key...)
		output = append(output, ':')
		output = append(output, left...)
	}
	output = append(output, '}')
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, output, "", "  "); err != nil {
		return nil, err
	}
	return pretty.Bytes(), nil
}
func cleanProjectHistory(raw json.RawMessage) (json.RawMessage, error) {
	if !jsTruthy(raw) {
		return raw, nil
	}
	projects, err := enumerableProperties(raw)
	if err != nil {
		return nil, err
	}
	needsCleaning := false
	source, _ := projects.MarshalJSON()
	decoder := json.NewDecoder(bytes.NewReader(source))
	_, _ = decoder.Token()
	out := []byte{'{'}
	for decoder.More() {
		start := decoder.InputOffset()
		token, _ := decoder.Token()
		key := source[start:decoder.InputOffset()]
		key = key[bytes.IndexByte(key, '"'):]
		var value json.RawMessage
		_ = decoder.Decode(&value)
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, &ProjectHistoryTypeError{}
		}
		var project protocol.JSONObject
		if project.UnmarshalJSON(value) == nil && project.IsObject() && project.Get("history") != nil {
			needsCleaning = true
			project.Delete("history")
			value, _ = project.MarshalJSON()
		}
		// cleanedProjects is an ordinary {} in Source. Assignment to
		// __proto__ changes its prototype instead of creating an own field.
		if token == "__proto__" {
			continue
		}
		if len(out) > 1 {
			out = append(out, ',')
		}
		out = append(out, key...)
		out = append(out, ':')
		out = append(out, value...)
	}
	if !needsCleaning {
		return raw, nil
	}
	return append(out, '}'), nil
}

type ProjectHistoryTypeError struct{}

func (*ProjectHistoryTypeError) Error() string {
	return "null is not an object (evaluating 'legacy.history')"
}
func (*ProjectHistoryTypeError) ErrorName() string { return "TypeError" }

func lessUTF16(left, right string) bool {
	a, b := utf16.Encode([]rune(left)), utf16.Encode([]rune(right))
	for index := 0; index < len(a) && index < len(b); index++ {
		if a[index] != b[index] {
			return a[index] < b[index]
		}
	}
	return len(a) < len(b)
}
