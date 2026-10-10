package clibootstrap

import "testing"

func TestNativeAnalyticsSourceOptOutIsIndependentOfProviderManagedKeys(t *testing.T) {
	for _, tc := range []struct {
		env  map[string]string
		want bool
	}{
		{map[string]string{}, false},
		{map[string]string{"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "0"}, true},
		{map[string]string{"DISABLE_TELEMETRY": "false"}, true},
		{map[string]string{"NODE_ENV": "test"}, true},
		{map[string]string{"CLAUDE_CODE_USE_BEDROCK": "yes"}, true},
		{map[string]string{"CLAUDE_CODE_USE_VERTEX": "false"}, false},
		{map[string]string{"CLAUDE_CODE_USE_FOUNDRY": "1"}, true},
		{map[string]string{"CLAUDE_CODE_USE_AZURE_OPENAI": "true"}, true},
		{map[string]string{"CLAUDE_CODE_USE_BEDROCK": "\ufeffTRUE\ufeff"}, true},
		{map[string]string{"CLAUDE_CODE_USE_BEDROCK": "\u0085TRUE\u0085"}, false},
	} {
		if got := nativeAnalyticsDisabled(tc.env); got != tc.want {
			t.Fatalf("env=%v got=%v want=%v", tc.env, got, tc.want)
		}
	}
}
