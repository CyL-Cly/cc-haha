package apipresentation

import "testing"

func TestSDKRuntimeEntrypointRetainsPresentationGuards(t *testing.T) {
	fast := false
	env := originalDomainEnv()
	delete(env, "CLAUDE_CODE_DISABLE_FAST_MODE")
	for _, value := range []string{"sdk", "sdk-cli"} {
		env["CLAUDE_CODE_ENTRYPOINT"] = value
		if _, err := DomainFromOriginalEnvAndFastMode(env, &fast); err != nil {
			t.Fatal(value, err)
		}
	}
	for _, value := range []string{"", "cli", "unknown", "sdk-ts", "sdk-py"} {
		env["CLAUDE_CODE_ENTRYPOINT"] = value
		if _, err := DomainFromOriginalEnvAndFastMode(env, &fast); err == nil {
			t.Fatal("accepted unported runtime entrypoint", value)
		}
	}
	env["CLAUDE_CODE_ENTRYPOINT"] = "sdk"
	env["unknown"] = "1"
	if _, err := DomainFromOriginalEnvAndFastMode(env, &fast); err == nil {
		t.Fatal("entrypoint bypassed unknown environment guard")
	}
	delete(env, "unknown")
	env["ANTHROPIC_API_KEY"] = ""
	if _, err := DomainFromOriginalEnvAndFastMode(env, &fast); err == nil {
		t.Fatal("entrypoint bypassed original credentials guard")
	}
}
