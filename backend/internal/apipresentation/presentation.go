// Package apipresentation ports the terminal presentation of the declared
// original direct-environment-key creation profile. It never resolves credentials,
// logs telemetry, drives retries or presents an unsupported fault as a success.
package apipresentation

import (
	"encoding/json"
	"reflect"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
)

type CapabilityError struct{ Boundary string }

func (failure *CapabilityError) Error() string {
	return "API presentation capability: " + failure.Boundary
}
func unsupported(boundary string) error { return &CapabilityError{Boundary: boundary} }

// Domain cannot be supplied as a preclassified Source subscriber flag. It is
// independently derived from the caller's original environment snapshot.
type Domain struct{ direct bool }

func DomainFromOriginalEnv(env map[string]string) (Domain, error) {
	return DomainFromOriginalEnvAndFastMode(env, nil)
}

// DomainFromOriginalEnvAndFastMode receives the actual host AppState sample.
// The legacy nil profile still requires the original explicit disable flag.
func DomainFromOriginalEnvAndFastMode(env map[string]string, fast *bool) (Domain, error) {
	return domainFromOriginalEnv(env, fast, false, false)
}

// NativeDomainFromOriginalEnvAndFastMode admits controls whose actual request
// and transport producers already consume them. It preserves the finite domain.
func NativeDomainFromOriginalEnvAndFastMode(env map[string]string, fast *bool) (Domain, error) {
	return domainFromOriginalEnv(env, fast, true, false)
}

func NativeDomainFromOriginalHost(host requestcontext.Host, fast *bool) (Domain, error) {
	if domain, selected, err := nativeVertexDomain(host, fast); selected {
		return domain, err
	}
	if domain, selected, err := nativeBedrockDomain(host, fast); selected {
		return domain, err
	}
	if host.NativeCodexOAuth == nil && host.NativeGrokOAuth == nil {
		return domainFromOriginalEnv(host.Env, fast, true, host.Profile == requestcontext.NativeDirectRelayProfile && host.NativeFastModeProof.Current())
	}
	if host.Profile != requestcontext.NativeDirectRelayProfile || host.NativeCodexOAuth != nil && (host.NativeCodexOAuth.HasAuth == nil || host.NativeCodexOAuth.FreshTokens == nil) || host.NativeGrokOAuth != nil && (host.NativeGrokOAuth.HasAuth == nil || host.NativeGrokOAuth.FreshTokens == nil) {
		return Domain{}, unsupported("actual native OpenAI auth binding")
	}
	// These are original client controls, consumed by the OAuth fetch wrapper;
	// credential lookup still happens after preparation in actual GetClient.
	env := map[string]string{}
	for name, value := range host.Env {
		if host.NativeGrokOAuth != nil && (name == "CC_HAHA_GROK_OAUTH_PROVIDER" || name == "GROK_OAUTH_FILE") {
			continue
		}
		switch name {
		case "CC_HAHA_OPENAI_OAUTH_PROVIDER", "OPENAI_CODEX_OAUTH_FILE", "OPENAI_CODEX_MODEL", "OPENAI_CODEX_HAIKU_MODEL", "OPENAI_CODEX_SONNET_MODEL", "OPENAI_CODEX_OPUS_MODEL", "CC_HAHA_OPENAI_REASONING_EFFORT", "CC_HAHA_OPENAI_REQUEST_COMPRESSION", "CC_HAHA_OPENAI_FAST_MODE":
		default:
			env[name] = value
		}
	}
	if env["ANTHROPIC_BASE_URL"] != "" && (env["ANTHROPIC_API_KEY"] != "" || env["ANTHROPIC_AUTH_TOKEN"] != "") {
		return domainFromOriginalEnv(env, fast, true, host.Profile == requestcontext.NativeDirectRelayProfile && host.NativeFastModeProof.Current())
	}
	if fast != nil && *fast && !host.NativeFastModeProof.Current() {
		return Domain{}, unsupported("active resolved fast mode")
	}
	if env["CLAUDE_CODE_DISABLE_FAST_MODE"] != "1" && (env["CLAUDE_CODE_DISABLE_FAST_MODE"] != "" || fast == nil) {
		return Domain{}, unsupported("original fast mode configuration")
	}
	for name := range env {
		if requestcontext.IsNativePolicyEnvironment(name) || provider.IsNativeProxyEnvironment(name) || name == "ANTHROPIC_DEFAULT_HEADERS" || name == "NODE_EXTRA_CA_CERTS" || name == "NODE_OPTIONS" || name == "CLAUDE_CODE_ENTRYPOINT" || name == "CLAUDE_CODE_DISABLE_FAST_MODE" || name == "CLAUDE_CODE_MAX_RETRIES" || name == "CLAUDE_CODE_SIMPLE" {
			continue
		}
		return Domain{}, unsupported("original environment " + name)
	}
	if err := tlsconfig.ValidateOriginalTLSOptions(env); err != nil {
		return Domain{}, unsupported(err.Error())
	}
	return Domain{direct: true}, nil
}

func domainFromOriginalEnv(env map[string]string, fast *bool, native, boundFast bool) (Domain, error) {
	if fast != nil && *fast && !(native && boundFast) {
		return Domain{}, unsupported("active resolved fast mode")
	}
	for name := range env {
		if native && requestcontext.IsNativePolicyEnvironment(name) {
			continue
		}
		if native {
			switch name {
			case "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_CUSTOM_HEADERS", "ANTHROPIC_DEFAULT_HEADERS", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy":
				continue
			}
		}
		switch name {
		case "ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY", "API_TIMEOUT_MS", "CLAUDE_CODE_EXTRA_METADATA", "CLAUDE_CODE_MAX_RETRIES", "CLAUDE_CODE_DISABLE_FAST_MODE", "NODE_EXTRA_CA_CERTS", "NODE_OPTIONS":
		case "CLAUDE_CODE_ENTRYPOINT":
			if env[name] != "sdk" && env[name] != "sdk-cli" {
				return Domain{}, unsupported("original runtime entrypoint value")
			}
		case "CLAUDE_CODE_SIMPLE":
			if env[name] != "1" {
				return Domain{}, unsupported("original bare environment value")
			}
		default:
			return Domain{}, unsupported("original environment " + name)
		}
	}
	if (env["ANTHROPIC_API_KEY"] == "" && (!native || env["ANTHROPIC_AUTH_TOKEN"] == "")) || env["ANTHROPIC_BASE_URL"] == "" || (env["CLAUDE_CODE_DISABLE_FAST_MODE"] != "1" && (env["CLAUDE_CODE_DISABLE_FAST_MODE"] != "" || fast == nil)) {
		return Domain{}, unsupported("original direct-env-key fast-disabled configuration")
	}
	if err := tlsconfig.ValidateOriginalTLSOptions(env); err != nil {
		return Domain{}, unsupported(err.Error())
	}
	return Domain{direct: true}, nil
}

// These optional interfaces represent real original error fields. The provider's
// native TLSConnectionFault uses Code(); a host with raw UTF-16 code identity can
// expose ConnectionCodeJSON(). A non-Error cause must declare that boundary, since
// Source's extractor stops rather than traversing JSON-shaped objects.
type rawCode interface{ ConnectionCodeJSON() json.RawMessage }
type codeValue interface{ Code() string }
type sourceInstance interface{ SourceErrorInstance() bool }

func absent(failure error) bool {
	return failure == nil || reflect.ValueOf(failure).Kind() == reflect.Pointer && reflect.ValueOf(failure).IsNil()
}

func connectionCode(failure error) (json.RawMessage, error) {
	current := failure
	for depth := 0; depth < 5 && !absent(current); depth++ {
		if original, ok := current.(sourceInstance); ok && !original.SourceErrorInstance() {
			break
		}
		if original, ok := current.(rawCode); ok {
			raw := original.ConnectionCodeJSON()
			if _, err := protocol.DecodeJavaScriptJSONString(raw); err == nil {
				return raw, nil
			}
		} else if original, ok := current.(codeValue); ok {
			raw, _ := json.Marshal(original.Code())
			return raw, nil
		}
		var next error
		if original, ok := current.(interface{ Unwrap() error }); ok {
			next = original.Unwrap()
		}
		if !reflect.TypeOf(current).Comparable() {
			return nil, unsupported("noncomparable original cause identity")
		}
		if next == current {
			break
		}
		current = next
	}
	return nil, nil
}

func quote(text string) json.RawMessage { raw, _ := json.Marshal(text); return raw }
func interpolate(prefix string, value json.RawMessage, suffix string) json.RawMessage {
	units, _ := protocol.DecodeJavaScriptJSONString(value)
	result := append(utf16.Encode([]rune(prefix)), units...)
	return protocol.QuoteJavaScriptUTF16(append(result, utf16.Encode([]rune(suffix))...))
}

func connectionText(code json.RawMessage) json.RawMessage {
	if len(code) == 0 {
		return quote("API Error: Unable to connect to API. Check your internet connection")
	}
	units, _ := protocol.DecodeJavaScriptJSONString(code)
	name := string(utf16.Decode(units))
	switch name {
	case "ETIMEDOUT":
		return quote("API Error: Request timed out. Check your internet connection and proxy settings")
	case "UNABLE_TO_VERIFY_LEAF_SIGNATURE", "UNABLE_TO_GET_ISSUER_CERT", "UNABLE_TO_GET_ISSUER_CERT_LOCALLY":
		return quote("API Error: Unable to connect to API: SSL certificate verification failed. Check your proxy or corporate SSL certificates")
	case "CERT_HAS_EXPIRED":
		return quote("API Error: Unable to connect to API: SSL certificate has expired")
	case "CERT_REVOKED":
		return quote("API Error: Unable to connect to API: SSL certificate has been revoked")
	case "DEPTH_ZERO_SELF_SIGNED_CERT", "SELF_SIGNED_CERT_IN_CHAIN":
		return quote("API Error: Unable to connect to API: Self-signed certificate detected. Check your proxy or corporate SSL certificates")
	case "ERR_TLS_CERT_ALTNAME_INVALID", "HOSTNAME_MISMATCH":
		return quote("API Error: Unable to connect to API: SSL certificate hostname mismatch")
	case "CERT_NOT_YET_VALID":
		return quote("API Error: Unable to connect to API: SSL certificate is not yet valid")
	case "CERT_SIGNATURE_FAILURE", "CERT_REJECTED", "CERT_UNTRUSTED", "CERT_CHAIN_TOO_LONG", "PATH_LENGTH_EXCEEDED", "ERR_TLS_HANDSHAKE_TIMEOUT", "ERR_SSL_WRONG_VERSION_NUMBER", "ERR_SSL_DECRYPTION_FAILED_OR_BAD_RECORD_MAC":
		return interpolate("API Error: Unable to connect to API: SSL error (", code, ")")
	case "":
		return quote("API Error: Unable to connect to API. Check your internet connection")
	default:
		return interpolate("API Error: Unable to connect to API (", code, ")")
	}
}

// CreationFailure unwraps the actual retry wrapper exactly once. The caller owns
// abort/normal stop/fallback, logging, empty-buffer flush and resource finally.
// Capability checks happen before the one shared message Factory is advanced.
func CreationFailure(failure error, domain Domain, factory *messages.Factory) (*streamhistory.Envelope, error) {
	if !domain.direct || factory == nil || absent(failure) {
		return nil, unsupported("declared original domain, error and shared factory")
	}
	if retry, ok := failure.(*retrypolicy.CannotRetryError); ok {
		failure = retry.Original
	}
	sdk, ok := failure.(*provider.SDKStreamError)
	if !ok || sdk == nil || sdk.Status != nil || sdk.Payload != nil {
		return nil, unsupported("typed no-status SDK creation error")
	}
	var text json.RawMessage
	if sdk.Class == "APIConnectionTimeoutError" {
		text = quote("Request timed out")
	} else if sdk.Class == "APIConnectionError" {
		raw := sdk.MessageJSON
		if raw == nil {
			raw = quote(sdk.Message)
		}
		units, err := protocol.DecodeJavaScriptJSONString(raw)
		if err != nil {
			return nil, unsupported("original SDK message string")
		}
		for _, unit := range units {
			if unit > 127 {
				return nil, unsupported("custom Unicode connection message precedence")
			}
		}
		message := string(utf16.Decode(units))
		if strings.Contains(strings.ToLower(message), "timeout") {
			text = quote("Request timed out")
		} else if message != "Connection error." {
			return nil, unsupported("custom connection message precedence")
		} else {
			code, err := connectionCode(sdk)
			if err != nil {
				return nil, err
			}
			text = connectionText(code)
		}
	} else {
		return nil, unsupported("SDK class " + sdk.Class)
	}
	record, err := factory.CreateOrderedAssistantAPIError(storage.Object{"content": text, "error": quote("unknown")})
	if err != nil {
		return nil, err
	}
	var message protocol.JSONObject
	if err := json.Unmarshal(record.Get("message"), &message); err != nil {
		return nil, err
	}
	var blocks []protocol.JSONObject
	if err := json.Unmarshal(message.Get("content"), &blocks); err != nil {
		return nil, err
	}
	content := make([]*streamhistory.Block, 0, len(blocks))
	for _, block := range blocks {
		content = append(content, &streamhistory.Block{Object: block})
	}
	return &streamhistory.Envelope{Record: record, Message: message, Content: content, Usage: &streamhistory.Usage{Raw: message.Get("usage")}, UUID: record.String("uuid"), APIError: true}, nil
}

var _ error = (*CapabilityError)(nil)
