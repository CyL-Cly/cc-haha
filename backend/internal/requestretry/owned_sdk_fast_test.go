package requestretry

import (
	"context"
	"strings"
	"testing"
)

func TestOwnedSDKGitResolvedFastDefaultsFromHostWithoutEnvRewrite(t *testing.T) {
	input, deps := standardInput(), standardDeps()
	delete(input.Context.Host.Env, "CLAUDE_CODE_DISABLE_FAST_MODE")
	delete(input.Context.Host.Env, "CLAUDE_CODE_MAX_RETRIES")
	if e := validated(input, deps); e == nil {
		t.Fatal("unknown fast state admitted")
	}
	value := false
	input.ResolvedFastMode = &value
	if e := validated(input, deps); e != nil {
		t.Fatal(e)
	}
	server := startFixtureServer(t, "success", func(int) (int, string) { return 200, responseWire })
	input.Context.Host.Env["ANTHROPIC_BASE_URL"] = server.origin
	stream, e := Open(context.Background(), input, deps)
	if e != nil {
		t.Fatal(e)
	}
	defer stream.Close()
	if _, ok := input.Context.Host.Env["CLAUDE_CODE_DISABLE_FAST_MODE"]; ok {
		t.Fatal("disable flag invented")
	}
	if _, ok := input.Context.Host.Env["CLAUDE_CODE_MAX_RETRIES"]; ok {
		t.Fatal("retry flag invented")
	}
	value = true
	if e := validated(input, deps); e == nil || !strings.Contains(e.Error(), "active resolved") {
		t.Fatal(e)
	}
	value = false
	input.Context.Host.Env["CLAUDE_CODE_DISABLE_FAST_MODE"] = "0"
	if e := validated(input, deps); e == nil {
		t.Fatal("active original fast environment admitted")
	}
}
