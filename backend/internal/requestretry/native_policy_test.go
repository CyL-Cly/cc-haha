package requestretry

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestNativeModelPolicyReachesPhysicalRequestWithDirectBearerToken(t *testing.T) {
	server := startFixtureServer(t, "success", func(int) (int, string) { return 200, responseWire })
	input := standardInput()
	input.Context.Host.Profile = requestcontext.NativeDirectRelayProfile
	input.Context.Host.Env["ANTHROPIC_BASE_URL"] = server.origin
	delete(input.Context.Host.Env, "ANTHROPIC_API_KEY")
	input.Context.Host.Env["ANTHROPIC_AUTH_TOKEN"] = "fake-direct-bearer"
	input.Context.Host.Env["ANTHROPIC_DEFAULT_SONNET_MODEL"] = "deepseek-flash"
	input.Context.Host.Env["ANTHROPIC_DEFAULT_SONNET_MODEL_SUPPORTED_CAPABILITIES"] = "thinking,adaptive_thinking,effort,max_effort"
	input.Context.Thinking = requestcontext.Thinking{Type: "adaptive"}
	input.Context.Options.EffortValue = json.RawMessage(`"max"`)
	input.Context.Options.EffortValueOverridesEnv = true
	stream, err := Open(context.Background(), input, standardDeps())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	body := <-server.bodies
	if !strings.Contains(body, `"thinking":{"type":"adaptive"}`) || !strings.Contains(body, `"effort":"max"`) {
		t.Fatalf("native policy not consumed by physical request: %s", body)
	}
	headers := <-server.headers
	if headers.Get("Authorization") != "Bearer fake-direct-bearer" || headers.Get("X-Api-Key") != "" {
		t.Fatalf("native auth changed: %v", headers)
	}
}

func TestNativePolicyStillRejectsUnknownInputsBeforePhysicalSend(t *testing.T) {
	input := standardInput()
	input.Context.Host.Profile = requestcontext.NativeDirectRelayProfile
	input.Context.Thinking.Type = "adaptive"
	input.Context.Host.Env["UNIMPLEMENTED_POLICY"] = "true"
	if err := validated(input, standardDeps()); err == nil {
		t.Fatal("native profile ignored unknown policy")
	}
	delete(input.Context.Host.Env, "UNIMPLEMENTED_POLICY")
	input.Context.Thinking.Type = "invented"
	if err := validated(input, standardDeps()); err == nil {
		t.Fatal("native profile ignored invalid thinking")
	}
}
