package modelaccounting

import "regexp"

// Ported from frozen Source static declarations, not a runtime capture.
var restoredDirectWindows = map[string]float64{
	"claude-fable-5-1":            1000000,
	"claude-fable-5":              1000000,
	"claude-opus-5-5":             1000000,
	"claude-opus-5":               1000000,
	"claude-opus-4-8":             1000000,
	"claude-opus-4-7":             1000000,
	"claude-sonnet-5-5":           1000000,
	"claude-sonnet-5":             1000000,
	"claude-sonnet-4-6":           200000,
	"claude-haiku-4-5":            200000,
	"deepseek-v4-pro":             1000000,
	"deepseek-ai/deepseek-v4-pro": 1000000,
	"deepseek-v4-flash":           1000000,
	"deepseek-chat":               1000000,
	"deepseek-reasoner":           1000000,
	"k3":                          262144,
	"kimi-for-coding":             262144,
	"kimi-for-coding-highspeed":   262144,
	"kimi-k2.7-code":              262144,
	"kimi-k2.7-code-highspeed":    262144,
	"kimi-k2.6":                   262144,
	"kimi-k2.5":                   262144,
	"kimi-k2-0905-preview":        262144,
	"kimi-k2-turbo-preview":       262144,
	"kimi-k2-thinking":            262144,
	"kimi-k2-thinking-turbo":      262144,
	"minimax-m3":                  1000000,
	"minimax-m2.7":                204800,
	"minimax-m2.7-highspeed":      204800,
	"qwen/qwen3.6-27b":            262144,
	"qwen3.6:27b":                 262144,
	"glm-5.3":                     1000000,
	"glm-5.3-flash":               1000000,
	"glm-5.2":                     1000000,
	"glm-5.1":                     200000,
	"glm-5":                       200000,
	"glm-5-turbo":                 200000,
	"glm-4.7":                     200000,
	"glm-4.6":                     200000,
	"glm-4.5":                     128000,
	"glm-4.5-air":                 128000,
}
var restoredWindowPatterns = initializeRestoredWindowPatterns()

func initializeRestoredWindowPatterns() []struct {
	pattern *regexp.Regexp
	window  float64
} {
	return []struct {
		pattern *regexp.Regexp
		window  float64
	}{
		{regexp.MustCompile("(?i)^anthropic\\/claude-opus-5\\b"), 1000000},
		{regexp.MustCompile("(?i)^anthropic\\/claude-fable-5\\b"), 1000000},
		{regexp.MustCompile("(?i)^anthropic\\/claude-opus-4\\.8\\b"), 1000000},
		{regexp.MustCompile("(?i)^anthropic\\/claude-sonnet-5\\b"), 1000000},
		{regexp.MustCompile("(?i)^anthropic\\/claude-opus-4\\.7\\b"), 1000000},
		{regexp.MustCompile("(?i)^anthropic\\/claude-sonnet-4\\.6\\b"), 200000},
		{regexp.MustCompile("(?i)^anthropic\\/claude-haiku-4\\.5\\b"), 200000},
		{regexp.MustCompile("(?i)^openai\\/gpt-4\\.1\\b"), 1047576},
		{regexp.MustCompile("(?i)^openai\\/gpt-5(?:[.-]\\d+)?\\b"), 400000},
		{regexp.MustCompile("(?i)^google\\/gemini-(?:2\\.0|2\\.5|3)"), 1048576},
		{regexp.MustCompile("(?i)^gemini-(?:2\\.0|2\\.5|3)"), 1048576},
		{regexp.MustCompile("(?i)^zai-org\\/glm-5\\.3(?:-flash)?\\b"), 1000000},
		{regexp.MustCompile("(?i)^zai-org\\/glm-5\\.2\\b"), 1000000},
		{regexp.MustCompile("(?i)^(?:qwen\\/)?qwen3\\.7-(?:max|plus)(?:[-.][\\w.-]+)?\\b"), 1000000},
		{regexp.MustCompile("(?i)^(?:qwen\\/)?qwen3\\.6-(?:plus|flash)(?:[-.][\\w.-]+)?\\b"), 1000000},
		{regexp.MustCompile("(?i)^(?:qwen\\/)?qwen3\\.6-max-preview\\b"), 262144},
		{regexp.MustCompile("(?i)^(?:qwen\\/)?qwen3\\.5-(?:plus|flash)(?:[-.][\\w.-]+)?\\b"), 1000000},
		{regexp.MustCompile("(?i)^(?:qwen\\/)?qwen3-max(?:[-.][\\w.-]+)?\\b"), 262144},
		{regexp.MustCompile("(?i)^(?:qwen\\/)?qwen3-coder-plus(?:[-.][\\w.-]+)?\\b"), 1000000},
		{regexp.MustCompile("(?i)^(?:qwen\\/)?qwen3-coder-next(?:[-.][\\w.-]+)?\\b"), 262144},
		{regexp.MustCompile("(?i)qwen-long"), 10000000},
	}
}

var restoredCanonicalPairs = [][2]string{
	{"claude-fable-5-1", "claude-fable-5-1"},
	{"claude-fable-5", "claude-fable-5"},
	{"claude-opus-5-5", "claude-opus-5-5"},
	{"claude-opus-5", "claude-opus-5"},
	{"claude-opus-4-8", "claude-opus-4-8"},
	{"claude-opus-4-7", "claude-opus-4-7"},
	{"claude-opus-4-5", "claude-opus-4-5"},
	{"claude-opus-4-1", "claude-opus-4-1"},
	{"claude-opus-4", "claude-opus-4"},
	{"claude-sonnet-5-5", "claude-sonnet-5-5"},
	{"claude-sonnet-5", "claude-sonnet-5"},
	{"claude-sonnet-4-6", "claude-sonnet-4-6"},
	{"claude-sonnet-4-5", "claude-sonnet-4-5"},
	{"claude-sonnet-4", "claude-sonnet-4"},
	{"claude-haiku-4-5", "claude-haiku-4-5"},
	{"claude-3-7-sonnet", "claude-3-7-sonnet"},
	{"claude-3-5-sonnet", "claude-3-5-sonnet"},
	{"claude-3-5-haiku", "claude-3-5-haiku"},
	{"claude-3-opus", "claude-3-opus"},
	{"claude-3-sonnet", "claude-3-sonnet"},
	{"claude-3-haiku", "claude-3-haiku"},
}
var restoredCanonicalFallback = initializeRestoredCanonicalFallback()

func initializeRestoredCanonicalFallback() *regexp.Regexp {
	return regexp.MustCompile(`(claude-(\d+-\d+-)?\w+)`)
}
