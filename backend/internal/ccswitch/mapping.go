package ccswitch

import (
	"encoding/json"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Entry struct {
	Candidate   protocol.JSONObject  `json:"candidate"`
	APIKey      json.RawMessage      `json:"apiKey"`
	CreateInput *protocol.JSONObject `json:"createInput"`
}
type Row struct {
	ID, Name, SettingsConfig, Meta, Notes json.RawMessage
	AppType                               string
	SortIndex, CreatedAt                  *float64
	IsCurrent                             bool
}

func envString(env protocol.JSONObject, key string) json.RawMessage { return trimmed(env.Get(key)) }
func credential(env protocol.JSONObject, key string) json.RawMessage {
	v := envString(env, key)
	switch lower(v) {
	case "(your api key)", "proxy-managed", "proxy_managed", "••••••••":
		return nil
	}
	return v
}
func normalizedBase(v json.RawMessage) string {
	return strings.ToLower(strings.TrimRight(text(trimmed(v)), "/"))
}
func matchPreset(base json.RawMessage, presets json.RawMessage) string {
	url := normalizedBase(base)
	if url == "" {
		return "custom"
	}
	for _, data := range array(presets) {
		p := obj(data)
		endpoints := []json.RawMessage{p.Get("baseUrl")}
		for _, ep := range array(p.Get("regionalEndpoints")) {
			endpoints = append(endpoints, obj(ep).Get("baseUrl"))
		}
		for _, ep := range endpoints {
			if normalizedBase(ep) != "" && normalizedBase(ep) == url {
				return p.String("id")
			}
		}
	}
	return "custom"
}
func modelMarker(v json.RawMessage) bool {
	s := lower(v)
	return strings.HasSuffix(s, "[1m]") || strings.HasSuffix(s, ":1m")
}
func stripModelMarker(v json.RawMessage) json.RawMessage {
	u := units(trimmed(v))
	s := strings.ToLower(stringRunes(u))
	if strings.HasSuffix(s, "[1m]") {
		u = u[:len(u)-4]
	}
	s = strings.ToLower(stringRunes(u))
	if strings.HasSuffix(s, ":1m") {
		u = u[:len(u)-3]
	}
	v = trimmed(protocol.QuoteJavaScriptUTF16(u))
	if v == nil {
		return str("")
	}
	return v
}
func stringRunes(u []uint16) string { return text(protocol.QuoteJavaScriptUTF16(u)) }
func maskKey(key json.RawMessage) json.RawMessage {
	u := units(key)
	if len(u) == 0 {
		return str("")
	}
	if len(u) < 8 {
		return str("…")
	}
	if len(u) < 16 {
		return appendText(str("…"), protocol.QuoteJavaScriptUTF16(u[len(u)-4:]))
	}
	return appendText(protocol.QuoteJavaScriptUTF16(u[:6]), str("…"), protocol.QuoteJavaScriptUTF16(u[len(u)-4:]))
}
func redact(notes, key json.RawMessage) json.RawMessage {
	x, y := units(notes), units(key)
	if len(x) == 0 || len(y) < 8 {
		return notes
	}
	out := []uint16{}
	replacement := units(str("[REDACTED]"))
	for i := 0; i < len(x); {
		match := i+len(y) <= len(x)
		if match {
			for j := range y {
				if x[i+j] != y[j] {
					match = false
					break
				}
			}
		}
		if match {
			out = append(out, replacement...)
			i += len(y)
		} else {
			out = append(out, x[i])
			i++
		}
	}
	return protocol.QuoteJavaScriptUTF16(out)
}
func ToEntry(row Row, current json.RawMessage, existing []protocol.JSONObject, presets json.RawMessage) Entry {
	settings := parseJSONLike(row.SettingsConfig)
	env := obj(settings.Get("env"))
	meta := parseJSONLike(row.Meta)
	name := trimmed(row.Name)
	if name == nil {
		name = row.ID
	}
	base := envString(env, "ANTHROPIC_BASE_URL")
	if base == nil {
		base = str("")
	}
	authField := strings.ToUpper(text(trimmed(meta.Get("apiKeyField"))))
	if authField != "ANTHROPIC_AUTH_TOKEN" && authField != "ANTHROPIC_API_KEY" {
		authField = ""
	}
	key := credential(env, authField)
	if key == nil {
		key = credential(env, "ANTHROPIC_AUTH_TOKEN")
	}
	if key == nil {
		key = credential(env, "ANTHROPIC_API_KEY")
	}
	if key == nil {
		key = str("")
	}
	auth := "auth_token"
	apiKey, token := env.Get("ANTHROPIC_API_KEY"), env.Get("ANTHROPIC_AUTH_TOKEN")
	if authField == "ANTHROPIC_API_KEY" {
		auth = "api_key"
	} else if authField == "ANTHROPIC_AUTH_TOKEN" {
		auth = "auth_token"
	} else if isString(apiKey) && isString(token) {
		if sameString(apiKey, str("dummy")) && sameString(token, str("dummy")) {
			auth = "dual_dummy"
		} else if len(units(apiKey)) == 0 && len(units(token)) > 0 {
			auth = "auth_token_empty_api_key"
		} else if len(units(apiKey)) > 0 && sameString(apiKey, token) {
			auth = "dual_same_token"
		} else if len(units(token)) == 0 && len(units(apiKey)) > 0 {
			auth = "api_key"
		}
	} else if isString(token) && len(units(token)) > 0 {
		auth = "auth_token"
	} else if isString(apiKey) && len(units(apiKey)) > 0 {
		auth = "api_key"
	}
	format := text(trimmed(meta.Get("apiFormat")))
	if format == "" {
		format = "anthropic"
	}
	validFormat := format == "anthropic" || format == "openai_chat" || format == "openai_responses"
	modelValues := map[string]json.RawMessage{}
	for _, slot := range []string{"main", "fable", "haiku", "sonnet", "opus"} {
		key := "ANTHROPIC_DEFAULT_" + strings.ToUpper(slot) + "_MODEL"
		if slot == "main" {
			key = "ANTHROPIC_MODEL"
		}
		modelValues[slot] = envString(env, key)
	}
	main := modelValues["main"]
	for _, slot := range []string{"sonnet", "haiku", "opus"} {
		if main == nil {
			main = modelValues[slot]
		}
	}
	if main == nil {
		main = str("")
	}
	models := record()
	support := record()
	for _, slot := range []string{"main", "fable", "haiku", "sonnet", "opus"} {
		v := modelValues[slot]
		if slot == "main" {
			v = main
		} else if slot != "fable" && v == nil {
			v = main
		}
		support.Set(slot, raw(v != nil && modelMarker(v)))
		if slot == "fable" && v == nil {
			continue
		}
		models.Set(slot, stripModelMarker(v))
	}
	apiFormat := format
	if !validFormat {
		apiFormat = "anthropic"
	}
	preset := matchPreset(base, presets)
	isCurrent := row.IsCurrent
	if row.AppType == "claude" && current != nil {
		isCurrent = sameString(current, row.ID)
	}
	skip := ""
	switch {
	case len(units(base)) == 0:
		skip = "no-base-url"
	case len(units(key)) == 0:
		skip = "no-api-key"
	case len(units(models.Get("main"))) == 0:
		skip = "no-model"
	case !validFormat:
		skip = "unsupported-format"
	case string(meta.Get("isFullUrl")) == "true":
		skip = "full-url-endpoint"
	}
	candidate := record(field("sourceId", appendText(str(row.AppType+":"), row.ID)), field("appType", row.AppType), protocol.JSONField{Name: "name", Value: name}, protocol.JSONField{Name: "baseUrl", Value: base}, protocol.JSONField{Name: "apiKeyPreview", Value: maskKey(key)}, field("hasApiKey", len(units(key)) > 0), field("models", models), field("model1mSupport", support), field("apiFormat", apiFormat), field("authStrategy", auth), field("presetId", preset), field("isCurrent", isCurrent), field("importable", skip == ""))
	if skip != "" {
		candidate.Set("skipReason", raw(skip))
	}
	for _, p := range existing {
		if normalizedBase(base) != "" && normalizedBase(p.Get("baseUrl")) == normalizedBase(base) && (lower(p.Get("name")) == lower(name) || (len(units(key)) > 0 && sameString(p.Get("apiKey"), key))) {
			candidate.Set("duplicate", raw(record(protocol.JSONField{Name: "id", Value: p.Get("id")}, protocol.JSONField{Name: "name", Value: p.Get("name")})))
			break
		}
	}
	if safe := redact(row.Notes, key); len(units(safe)) > 0 {
		candidate.Set("notes", safe)
	}
	entry := Entry{Candidate: candidate, APIKey: key}
	if skip != "" {
		return entry
	}
	input := record(field("presetId", preset), protocol.JSONField{Name: "name", Value: name}, protocol.JSONField{Name: "apiKey", Value: key}, field("authStrategy", auth), protocol.JSONField{Name: "baseUrl", Value: base}, field("apiFormat", format), field("runtimeKind", "anthropic_compatible"), field("models", models), field("model1mSupport", support))
	if n := contextWindow(env.Get("CLAUDE_CODE_AUTO_COMPACT_WINDOW")); n != nil {
		input.Set("autoCompactWindow", raw(*n))
	}
	windows := parseJSONLike(env.Get("CLAUDE_CODE_MODEL_CONTEXT_WINDOWS"))
	normalized := record()
	for _, p := range windows.OwnProperties() {
		key := trimmed(p.NameJSON)
		if n := contextWindow(p.Value); key != nil && n != nil && !sameString(key, str("__proto__")) {
			_ = normalized.SetPropertyJSON(key, raw(*n))
		}
	}
	if len(normalized.OwnProperties()) > 0 {
		input.Set("modelContextWindows", raw(normalized))
	}
	if env.Get("ENABLE_TOOL_SEARCH") != nil {
		input.Set("toolSearchEnabled", raw(boolEnv(env.Get("ENABLE_TOOL_SEARCH"), true)))
	}
	if env.Get("CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS") != nil {
		input.Set("disableExperimentalBetas", raw(boolEnv(env.Get("CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS"), false)))
	}
	if len(units(row.Notes)) > 0 {
		input.Set("notes", row.Notes)
	}
	entry.CreateInput = &input
	return entry
}
func fingerprint(entry Entry) string {
	if entry.CreateInput != nil {
		return string(raw([]any{"importable", entry.CreateInput}))
	}
	return string(raw([]any{"skipped", lower(entry.Candidate.Get("name")), normalizedBase(entry.Candidate.Get("baseUrl")), entry.APIKey, entry.Candidate.Get("skipReason")}))
}
func Dedupe(entries []Entry) []Entry {
	seen := map[string]bool{}
	for _, e := range entries {
		if e.Candidate.String("appType") == "claude" {
			seen[fingerprint(e)] = true
		}
	}
	out := []Entry{}
	for _, e := range entries {
		if e.Candidate.String("appType") == "claude" || !seen[fingerprint(e)] {
			out = append(out, e)
		}
	}
	return out
}
