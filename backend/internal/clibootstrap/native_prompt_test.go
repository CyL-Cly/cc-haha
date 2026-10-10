package clibootstrap

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"testing"
)

func TestNativeGlobalCacheUsesSourceProviderAndCanonicalHost(t *testing.T) {
	for _, tc := range []struct {
		env  map[string]string
		want bool
	}{
		{map[string]string{}, true},
		{map[string]string{"ANTHROPIC_BASE_URL": "https://API.ANTHROPIC.COM:443"}, true},
		{map[string]string{"ANTHROPIC_BASE_URL": "https://api.anthropic.com:999"}, false},
		{map[string]string{"ANTHROPIC_BASE_URL": "https://api.deepseek.com/anthropic"}, false},
		{map[string]string{"CLAUDE_CODE_USE_BEDROCK": "yes"}, false},
		{map[string]string{"CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS": "true"}, false},
		{map[string]string{"USER_TYPE": "ant", "ANTHROPIC_BASE_URL": "https://api-staging.anthropic.com"}, true},
	} {
		if got := nativeGlobalCacheScope(tc.env); got != tc.want {
			t.Fatalf("env=%v got=%v", tc.env, got)
		}
	}
}

func TestNativeThinkingConsumesPersistedSwitchAndOriginalTokenOverride(t *testing.T) {
	settings := protocol.NewJSONObject()
	settings.Set("alwaysThinkingEnabled", []byte("false"))
	if got := resolveNativeThinking(&settings, map[string]string{}); got.Type != "disabled" {
		t.Fatal(got)
	}
	got := resolveNativeThinking(&settings, map[string]string{"MAX_THINKING_TOKENS": "\ufeff +4096suffix"})
	if got.Type != "enabled" || got.BudgetTokens == nil || *got.BudgetTokens != 4096 {
		t.Fatal(got)
	}
	if got = resolveNativeThinking(nil, map[string]string{"MAX_THINKING_TOKENS": "0x20"}); got.Type != "disabled" {
		t.Fatal(got)
	}
	if got = resolveNativeThinking(nil, map[string]string{"MAX_THINKING_TOKENS": "-1"}); got.Type != "disabled" {
		t.Fatal(got)
	}
}

func TestNativeThinkingEnvironmentPrecedesLegacyFlag(t *testing.T) {
	legacy := 2048.0
	for _, tc := range []struct{ value, kind string }{{"", "enabled"}, {"bogus", "disabled"}, {"-1", "disabled"}, {"0", "disabled"}, {"1024", "enabled"}} {
		got := resolveNativeThinking(nil, map[string]string{"MAX_THINKING_TOKENS": tc.value}, &legacy)
		if got.Type != tc.kind {
			t.Fatalf("%q: %#v", tc.value, got)
		}
		if tc.value == "1024" && (got.BudgetTokens == nil || *got.BudgetTokens != 1024) {
			t.Fatal(got)
		}
	}
}
