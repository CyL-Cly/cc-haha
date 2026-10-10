package sdkhost

import "testing"

func TestOwnedSDKGitOriginalEnvironmentViewsDoNotInventFlags(t *testing.T) {
	original := map[string]string{"CLAUDE_CODE_SIMPLE": "1", "ANTHROPIC_API_KEY": "fake", "ANTHROPIC_BASE_URL": "http://127.0.0.1:1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "CLAUDE_CODE_DISABLE_GIT_INSTRUCTIONS": "1", "CLAUDE_CODE_SKIP_REMOTE_PREFETCH": "1", "CLAUDE_CODE_RESUME_INTERRUPTED_TURN": "false"}
	view, e := originalRequestEnvironment(original)
	if e != nil {
		t.Fatal(e)
	}
	if len(view) != 3 || len(original) != 8 {
		t.Fatalf("raw/view fields %v/%v", original, view)
	}
	if _, ok := view["CLAUDE_CODE_DISABLE_FAST_MODE"]; ok {
		t.Fatal("fast override invented")
	}
	view["ANTHROPIC_API_KEY"] = "changed"
	if original["ANTHROPIC_API_KEY"] != "fake" {
		t.Fatal("request view mutates host")
	}
	for _, key := range []string{"CLAUDE_CODE_DISABLE_AUTO_MEMORY", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "CLAUDE_CODE_DISABLE_GIT_INSTRUCTIONS", "CLAUDE_CODE_SKIP_REMOTE_PREFETCH", "CLAUDE_CODE_RESUME_INTERRUPTED_TURN"} {
		copy := map[string]string{}
		for k, v := range original {
			copy[k] = v
		}
		copy[key] = "active"
		if _, e := originalRequestEnvironment(copy); e == nil {
			t.Fatalf("unproved %s admitted", key)
		}
	}
	original["unknown"] = "1"
	if _, e := originalRequestEnvironment(original); e == nil {
		t.Fatal("unknown environment dropped")
	}
}
