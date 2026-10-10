package requestretry

import (
	"context"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestSDKRuntimeEntrypointPreservedThroughRealOpen(t *testing.T) {
	input, deps := standardInput(), standardDeps()
	server := startFixtureServer(t, "success", func(int) (int, string) { return 200, responseWire })
	input.Context.Host.Env["ANTHROPIC_BASE_URL"] = server.origin
	for _, entrypoint := range []string{"sdk", "sdk-cli"} {
		input.Context.Host.Env["CLAUDE_CODE_ENTRYPOINT"] = entrypoint
		stream, err := Open(context.Background(), input, deps)
		if err != nil {
			t.Fatal(entrypoint, err)
		}
		stream.Close()
		if input.Context.Host.Env["CLAUDE_CODE_ENTRYPOINT"] != entrypoint {
			t.Fatal("runtime input mutated")
		}
	}
	for _, value := range []string{"", "cli", "unknown", "sdk-ts", "sdk-py"} {
		input.Context.Host.Env["CLAUDE_CODE_ENTRYPOINT"] = value
		if err := validated(input, deps); err == nil {
			t.Fatal("unported entrypoint admitted", value)
		}
	}
	input.Context.Host.Env["CLAUDE_CODE_ENTRYPOINT"] = "sdk"
	input.Context.Options.QuerySource = "repl_main_thread"
	if err := validated(input, deps); err == nil {
		t.Fatal("unported query source admitted")
	}
	input.Context.Options.QuerySource = "sdk"
	input.Context.Host.Env["unknown"] = "1"
	if err := validated(input, deps); err == nil {
		t.Fatal("SDK runtime bypassed unknown guard")
	}
}

func TestNativeCLIEntrypointReachesActualDirectRequest(t *testing.T) {
	input, deps := standardInput(), standardDeps()
	server := startFixtureServer(t, "success", func(int) (int, string) { return 200, responseWire })
	input.Context.Host.Env["ANTHROPIC_BASE_URL"] = server.origin
	input.Context.Host.Profile = requestcontext.NativeDirectRelayProfile
	input.Context.Host.Env["CLAUDE_CODE_ENTRYPOINT"] = "cli"
	stream, err := Open(context.Background(), input, deps)
	if err != nil {
		t.Fatal("native CLI rejected before HTTP", err)
	}
	stream.Close()
	if input.Context.Host.Env["CLAUDE_CODE_ENTRYPOINT"] != "cli" {
		t.Fatal("CLI identity mutated")
	}
	input.Context.Host.Profile = requestcontext.DirectRelayProfile
	if validated(input, deps) == nil {
		t.Fatal("finite profile was broadened")
	}
}

func TestNativeMCPEntrypointRequiresActualWebProducer(t *testing.T) {
	input, deps := standardInput(), standardDeps()
	input.Context.Host.Profile = requestcontext.NativeDirectRelayProfile
	input.Context.Host.Env["CLAUDE_CODE_ENTRYPOINT"] = "mcp"
	for _, source := range []string{"sdk", "web_fetch_apply", "web_search_tool", "repl_main_thread"} {
		input.Context.Options.QuerySource = source
		deps.NativeWebSource = ""
		if validated(input, deps) == nil {
			t.Fatal("MCP entrypoint admitted without actual web producer", source)
		}
		deps.NativeWebSource = source
		err := validated(input, deps)
		if source == "web_fetch_apply" || source == "web_search_tool" {
			if err != nil {
				t.Fatal("actual MCP web producer rejected", source, err)
			}
		} else if err == nil {
			t.Fatal("MCP entrypoint widened unrelated query source", source)
		}
	}
	input.Context.Options.QuerySource = "web_search_tool"
	deps.NativeWebSource = "web_search_tool"
	input.Context.Host.Profile = requestcontext.DirectRelayProfile
	if validated(input, deps) == nil {
		t.Fatal("MCP entrypoint widened finite profile")
	}
}

func TestNativeMCPEntrypointOnlyNativeWorkflowAgentSource(t *testing.T) {
	input, deps := standardInput(), standardDeps()
	input.Context.Host.Profile = requestcontext.NativeDirectRelayProfile
	input.Context.Host.Env["CLAUDE_CODE_ENTRYPOINT"] = "mcp"
	input.Context.Options.QuerySource = "workflow_agent"
	if e := validated(input, deps); e != nil {
		t.Fatal("actual native workflow child rejected", e)
	}
	input.Context.Options.QuerySource = "sdk"
	if validated(input, deps) == nil {
		t.Fatal("MCP workflow widened unrelated child")
	}
	input.Context.Options.QuerySource = "workflow_agent"
	input.Context.Host.Profile = requestcontext.DirectRelayProfile
	if validated(input, deps) == nil {
		t.Fatal("MCP workflow widened finite child")
	}
}
