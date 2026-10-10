package requestretry

import (
	"context"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestNativeNoProxyInputsReachActualLoopbackWithoutDiscardingOriginalEnv(t *testing.T) {
	for _, withProxy := range []bool{false, true} {
		t.Run(map[bool]string{false: "no-configured-proxy", true: "configured-proxy-bypassed"}[withProxy], func(t *testing.T) {
			server := startFixtureServer(t, "success", func(int) (int, string) { return 200, responseWire })
			input := standardInput()
			input.Context.Host.Profile = requestcontext.NativeDirectRelayProfile
			input.Context.Host.Env["ANTHROPIC_BASE_URL"] = server.origin
			input.Context.Host.Env["NO_PROXY"] = "wrong.invalid"
			input.Context.Host.Env["no_proxy"] = "localhost,127.0.0.1,::1"
			if withProxy {
				input.Context.Host.Env["HTTPS_PROXY"] = "http://127.0.0.1:1"
			}
			stream, e := Open(context.Background(), input, standardDeps())
			if e != nil {
				t.Fatal(e)
			}
			defer stream.Close()
			body := <-server.bodies
			if strings.Contains(body, "no_proxy") || !strings.Contains(body, `"model":"deepseek-flash"`) {
				t.Fatalf("transport control became body input: %s", body)
			}
			if input.Context.Host.Env["no_proxy"] != "localhost,127.0.0.1,::1" || input.Context.Host.Env["NO_PROXY"] != "wrong.invalid" {
				t.Fatal("caller original env changed")
			}
		})
	}
}

func TestNativeSelectedProxyRetainsTransportAndFiniteStillRejectsNoProxy(t *testing.T) {
	input := standardInput()
	input.Context.Host.Env["no_proxy"] = "127.0.0.1"
	if e := validated(input, standardDeps()); e == nil {
		t.Fatal("finite original env domain broadened")
	}
	input.Context.Host.Profile = requestcontext.NativeDirectRelayProfile
	input.Context.Host.Env["https_proxy"] = "http://127.0.0.1:1"
	input.Context.Host.Env["ANTHROPIC_BASE_URL"] = "http://unmatched.invalid"
	if e := validated(input, standardDeps()); e != nil {
		t.Fatalf("native selected proxy rejected: %v", e)
	}
	exchange, e := provider.NativeDirectHTTPExchangeFromEnvironment(input.Context.Host.Env["ANTHROPIC_BASE_URL"], input.Context.Host.Env)
	if e != nil || exchange == nil || exchange.ProxyURL != input.Context.Host.Env["https_proxy"] {
		t.Fatalf("selected proxy transport lost: %+v %v", exchange, e)
	}
}
