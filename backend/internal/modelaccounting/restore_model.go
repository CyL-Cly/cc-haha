package modelaccounting

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"math"
	"regexp"
	"strings"
)

type RestoredModelResolver struct {
	Environment      map[string]string
	Settings, Global func() (*protocol.JSONObject, error)
	SDKBetas         []string
}

func (r RestoredModelResolver) Canonical(model string) (string, error) {
	if r.Settings != nil {
		settings, e := r.Settings()
		if e != nil {
			return "", e
		}
		if value := settings.Get("modelOverrides"); storedTruthy(value) {
			var overrides protocol.JSONObject
			if overrides.UnmarshalJSON(value) != nil || !overrides.IsObject() {
				return "", unsupported("settings modelOverrides object")
			}
			for _, p := range overrides.OwnProperties() {
				var original string
				if json.Unmarshal(p.Value, &original) == nil && original == model {
					if json.Unmarshal(p.NameJSON, &model) != nil {
						return "", unsupported("model override canonical key")
					}
					break
				}
			}
		}
	}
	m := strings.ToLower(model)
	for _, p := range restoredCanonicalPairs {
		if strings.Contains(m, p[0]) {
			return p[1], nil
		}
	}
	if found := restoredCanonicalFallback.FindStringSubmatch(m); len(found) > 1 {
		return found[1], nil
	}
	return m, nil
}

var restoreBracketSuffix = regexp.MustCompile(`(?i)\[1m\]$`)
var restoreColonSuffix = regexp.MustCompile(`(?i):1m$`)
var restoreOneM = regexp.MustCompile(`(?i)\[1m\]`)

func restoreTextTrim(value string) string {
	return strings.TrimFunc(value, func(r rune) bool {
		return r == '\ufeff' || r == '\u0009' || r == '\u000b' || r == '\u000c' || r == '\u0020' || r == '\u00a0' || r == '\u1680' || r >= '\u2000' && r <= '\u200a' || r == '\u202f' || r == '\u205f' || r == '\u3000' || r == '\n' || r == '\r' || r == '\u2028' || r == '\u2029'
	})
}
func restoreModelKey(model string) string {
	return strings.ToLower(restoreColonSuffix.ReplaceAllString(restoreBracketSuffix.ReplaceAllString(restoreTextTrim(model), ""), ""))
}
func envTruthy(value string) bool {
	switch strings.ToLower(restoreTextTrim(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
func configuredRestoreWindow(model, rawValue string) float64 {
	var windows protocol.JSONObject
	if windows.UnmarshalJSON([]byte(rawValue)) != nil || !windows.IsObject() {
		return 0
	}
	normalized := protocol.NewJSONObject()
	for _, p := range windows.OwnProperties() {
		var key string
		var n float64
		if json.Unmarshal(p.NameJSON, &key) != nil || json.Unmarshal(p.Value, &n) != nil || math.Trunc(n) != n || n < 16000 || n > 10000000 {
			continue
		}
		normalized.Set(restoreModelKey(key), raw(n))
	}
	key := restoreModelKey(model)
	if n := num(normalized.Get(key)); n != 0 {
		return n
	}
	for _, p := range normalized.OwnProperties() {
		var suffix string
		_ = json.Unmarshal(p.NameJSON, &suffix)
		if strings.HasSuffix(key, "/"+suffix) || strings.HasSuffix(key, ":"+suffix) {
			return num(p.Value)
		}
	}
	return 0
}

// ConfiguredContextWindow exposes only the original environment lookup. It
// does not fall through to model tables or invoke the output-token resolver.
func ConfiguredContextWindow(model, rawValue string) (float64, bool) {
	window := configuredRestoreWindow(model, rawValue)
	return window, window > 0
}
func codexRestoreWindow(model string) float64 {
	switch strings.ToLower(restoreTextTrim(model)) {
	case "gpt-6-astra", "gpt-6", "gpt-6.1-sol", "gpt-6-sol", "gpt-6-luna", "gpt-5.5", "gpt-5.5-pro", "gpt-5.4-mini", "gpt-5.4-nano", "gpt-5.3-codex", "gpt-5.2", "gpt-5.2-codex", "gpt-5.1", "gpt-5.1-codex", "gpt-5.1-codex-max", "gpt-5.1-codex-mini", "gpt-5-codex", "gpt-5", "gpt-5-mini", "gpt-5-nano":
		return math.Floor(272000 * 0.95)
	case "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.6":
		return math.Floor(372000 * 0.95)
	case "gpt-5.4", "gpt-5.4-pro":
		return math.Floor(1000000 * 0.95)
	case "gpt-5.3-codex-spark":
		return math.Floor(128000 * 0.95)
	default:
		return 0
	}
}
func (r RestoredModelResolver) Resolve(model string) (float64, float64, error) {
	window, e := r.contextWindow(model)
	if e != nil {
		return 0, 0, e
	}
	canonical, e := r.Canonical(model)
	if e != nil {
		return 0, 0, e
	}
	output, e := requestcontext.RestoreDefaultOutputTokens(canonical)
	if e != nil {
		return 0, 0, e
	}
	return window, output, nil
}

// ContextWindow is the original context getter alone. Consumers such as Skill
// listings must not also invoke the separate output-token/canonical resolver.
func (r RestoredModelResolver) ContextWindow(model string) (float64, error) {
	return r.contextWindow(model)
}

// Supports1M is Source modelSupports1M: capability eligibility is distinct
// from the current configured window and from a provider's [1m] marker.
func (r RestoredModelResolver) Supports1M(model string) (bool, error) {
	if envTruthy(r.Environment["CLAUDE_CODE_DISABLE_1M_CONTEXT"]) {
		return false, nil
	}
	canonical, err := r.Canonical(model)
	if err != nil {
		return false, err
	}
	for _, part := range []string{"claude-fable-5", "claude-sonnet-5", "claude-opus-5", "claude-opus-4-8", "claude-opus-4-7", "claude-sonnet-4", "opus-4-6"} {
		if strings.Contains(canonical, part) {
			return true, nil
		}
	}
	return false, nil
}
func (r RestoredModelResolver) contextWindow(model string) (float64, error) {
	if key := restoreModelKey(model); key == "__proto__" || key == "constructor" {
		return 0, unsupported("Source model-context inherited prototype property")
	}
	if r.Environment["USER_TYPE"] == "ant" {
		return 0, unsupported("internal model capability cache/context/max-output resolver")
	}
	disabled := envTruthy(r.Environment["CLAUDE_CODE_DISABLE_1M_CONTEXT"])
	capWindow := func(n float64) float64 {
		if n > 200000 && disabled {
			return 200000
		}
		return n
	}
	if n := configuredRestoreWindow(model, r.Environment["CLAUDE_CODE_MODEL_CONTEXT_WINDOWS"]); n > 0 {
		return capWindow(n), nil
	}
	if !disabled && (restoreOneM.MatchString(model) || strings.HasSuffix(strings.ToLower(model), ":1m")) {
		return 1000000, nil
	}
	if n := restoredDirectWindows[restoreModelKey(model)]; n > 0 {
		return capWindow(n), nil
	}
	for _, p := range restoredWindowPatterns {
		if p.pattern.MatchString(restoreModelKey(model)) {
			return capWindow(p.window), nil
		}
	}
	if n := codexRestoreWindow(model); n > 0 {
		return n, nil
	}
	if !disabled {
		canonical, e := r.Canonical(model)
		if e != nil {
			return 0, e
		}
		for _, beta := range r.SDKBetas {
			if beta == "context-1m-2025-08-07" {
				for _, part := range []string{"claude-fable-5", "claude-sonnet-5", "claude-opus-5", "claude-opus-4-8", "claude-opus-4-7", "claude-sonnet-4", "opus-4-6"} {
					if strings.Contains(canonical, part) {
						return 1000000, nil
					}
				}
			}
		}
		if strings.Contains(canonical, "sonnet-4-6") && r.Global != nil {
			global, e := r.Global()
			if e != nil {
				return 0, e
			}
			cache := object(global.Get("clientDataCache"))
			var setting string
			_ = json.Unmarshal(cache.Get("coral_reef_sonnet"), &setting)
			if setting == "true" {
				return 1000000, nil
			}
		}
	}
	return 200000, nil
}
