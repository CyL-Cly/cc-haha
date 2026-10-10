package apipresentation

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
)

func nativeBedrockDomain(host requestcontext.Host, fast *bool) (Domain, bool, error) {
	if !host.NativeBedrockBound {
		return Domain{}, false, nil
	}
	if host.Profile != requestcontext.NativeDirectRelayProfile || host.NativeBedrockCanonical == nil || !host.NativeBedrockProof.Current() {
		return Domain{}, true, unsupported("actual native Bedrock host binding")
	}
	if fast != nil && *fast {
		return Domain{}, true, unsupported("active resolved fast mode")
	}
	if host.Env["CLAUDE_CODE_DISABLE_FAST_MODE"] != "1" && (host.Env["CLAUDE_CODE_DISABLE_FAST_MODE"] != "" || fast == nil) {
		return Domain{}, true, unsupported("original fast mode configuration")
	}
	for name := range host.Env {
		if requestcontext.IsNativeBedrockEnvironment(name) || requestcontext.IsNativePolicyEnvironment(name) || provider.IsNativeProxyEnvironment(name) {
			continue
		}
		switch name {
		case "ANTHROPIC_DEFAULT_HEADERS", "NODE_EXTRA_CA_CERTS", "NODE_OPTIONS", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_DISABLE_FAST_MODE", "CLAUDE_CODE_MAX_RETRIES", "CLAUDE_CODE_SIMPLE":
			continue
		}
		return Domain{}, true, unsupported("original environment " + name)
	}
	if err := tlsconfig.ValidateOriginalTLSOptions(host.Env); err != nil {
		return Domain{}, true, unsupported(err.Error())
	}
	// Original isClaudeAISubscriber returns false before OAuth lookup for a
	// selected third-party provider, even without an Anthropic API credential.
	return Domain{direct: true}, true, nil
}
