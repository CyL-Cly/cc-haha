package provider

import (
	"strings"
	"testing"
)

func TestCurrentWireIdentityPreservesInternalDesktopEntrypoint(t *testing.T) {
	env := map[string]string{"CLAUDE_CODE_ENTRYPOINT": "claude-desktop", "CLAUDE_AGENT_SDK_VERSION": "fixture", "CLAUDE_AGENT_SDK_CLIENT_APP": "host"}
	options := ResolveDirectClientOptions(DirectClientInput{Env: env, Workload: "autoQuestion"})
	config := options.StreamConfig()
	ua := config.DefaultHeaders.Get("User-Agent")
	if !strings.Contains(ua, "claude-cli/2.1.288 (external, cli, agent-sdk/fixture, client-app/host, workload/autoQuestion)") || env["CLAUDE_CODE_ENTRYPOINT"] != "claude-desktop" || config.DefaultHeaders.Get("X-Stainless-Package-Version") != "0.128.0" {
		t.Fatal(ua, config.DefaultHeaders, env)
	}
}
