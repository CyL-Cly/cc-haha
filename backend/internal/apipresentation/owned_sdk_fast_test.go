package apipresentation

import "testing"

func TestOwnedSDKGitResolvedFastDomainKeepsOriginalGuards(t *testing.T) {
	for _, disabled := range []string{"", "1", "0"} {
		for _, sample := range []string{"nil", "false", "true"} {
			t.Run(disabled+"-"+sample, func(t *testing.T) {
				env := originalDomainEnv()
				delete(env, "CLAUDE_CODE_DISABLE_FAST_MODE")
				if disabled != "" {
					env["CLAUDE_CODE_DISABLE_FAST_MODE"] = disabled
				}
				var fast *bool
				if sample != "nil" {
					value := sample == "true"
					fast = &value
				}
				_, e := DomainFromOriginalEnvAndFastMode(env, fast)
				want := disabled != "0" && sample != "true" && (disabled == "1" || sample == "false")
				if (e == nil) != want {
					t.Fatalf("admission %v want %v", e, want)
				}
			})
		}
	}
	fast := false
	for _, change := range []map[string]string{{"ANTHROPIC_API_KEY": ""}, {"ANTHROPIC_BASE_URL": ""}, {"NODE_OPTIONS": "--use-system-ca"}, {"unknown": "1"}} {
		env := originalDomainEnv()
		delete(env, "CLAUDE_CODE_DISABLE_FAST_MODE")
		for key, value := range change {
			env[key] = value
		}
		if _, e := DomainFromOriginalEnvAndFastMode(env, &fast); e == nil {
			t.Fatal("resolvedfalse bypassed auth/TLS/unknown guard", change)
		}
	}
}
