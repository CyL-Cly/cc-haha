package provider

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// SourceRuntime freezes the SDK's wire identity from the accepted source build.
// A Go runtime version must not silently replace model-request metadata during
// migration. Packaging supplies the source runtime identity for its platform.
type SourceRuntime struct{ Platform, Arch, Version string }

const SourceCompatibilityVersion = "2.1.288"
const SourceSDKVersion = "0.128.0"

// DirectClientInput starts after provider selection and fallback credential
// lookup. This component never reads process env, settings, keychains, helper
// commands or OAuth. It covers direct Anthropic-compatible construction only.
type DirectClientInput struct {
	Env            map[string]string
	ExplicitAPIKey *string
	FallbackAPIKey *string
	SessionID      string
	Workload       string
	Runtime        SourceRuntime
	// Managed-proxy auth re-reads the final fetch environment; ordinary client
	// defaults and the first-party request-ID decision remain construction-time.
	FetchEnvironment func() map[string]string
	RequestID        func() string
}

type DirectClientOptions struct {
	APIKey, AuthToken *string
	BaseURL           string
	TimeoutMillis     float64
	DefaultHeaders    protocol.JSONObject
	runtime           SourceRuntime
	env               map[string]string
	fetchEnvironment  func() map[string]string
	requestID         func() string
	injectRequestID   bool
}

func stringPresent(value *string) bool { return value != nil && *value != "" }
func copyString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// ResolveAnthropicClientAPIKey preserves the source's JS truthiness and
// token-only guard. Fallback is already resolved by the host auth controller.
func ResolveAnthropicClientAPIKey(explicit *string, envAuthToken, envAPIKey string, fallback *string) *string {
	if envAuthToken != "" && !stringPresent(explicit) && envAPIKey == "" {
		return nil
	}
	if stringPresent(explicit) {
		return copyString(explicit)
	}
	return copyString(fallback)
}

func javascriptTrim(value string) string {
	return strings.TrimFunc(value, func(character rune) bool {
		return character >= '\t' && character <= '\r' || character == '\ufeff' || character == '\u2028' || character == '\u2029' || unicode.Is(unicode.Zs, character)
	})
}

func clientEnvTruthy(value string) bool {
	switch strings.ToLower(javascriptTrim(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func parseClientTimeout(value string) float64 {
	if value == "" {
		value = "600000"
	}
	value = javascriptTrim(value)
	end := 0
	if len(value) > 0 && (value[0] == '-' || value[0] == '+') {
		end++
	}
	firstDigit := end
	for end < len(value) && value[end] >= '0' && value[end] <= '9' {
		end++
	}
	if end == firstDigit {
		return math.NaN()
	}
	number, _ := strconv.ParseFloat(value[:end], 64)
	return number
}

func ResolveDirectClientOptions(input DirectClientInput) DirectClientOptions {
	env := input.Env
	options := DirectClientOptions{
		APIKey:  ResolveAnthropicClientAPIKey(input.ExplicitAPIKey, env["ANTHROPIC_AUTH_TOKEN"], env["ANTHROPIC_API_KEY"], input.FallbackAPIKey),
		BaseURL: env["ANTHROPIC_BASE_URL"], TimeoutMillis: parseClientTimeout(env["API_TIMEOUT_MS"]), runtime: input.Runtime,
		DefaultHeaders: protocol.NewJSONObject(),
		env:            copyClientEnvironment(env), fetchEnvironment: input.FetchEnvironment, requestID: input.RequestID, injectRequestID: injectFirstPartyRequestID(env),
	}
	if token, exists := env["ANTHROPIC_AUTH_TOKEN"]; exists {
		token = javascriptTrim(token)
		if token != "" {
			options.AuthToken = &token
		}
	}
	if options.BaseURL == "" {
		options.BaseURL = "https://api.anthropic.com"
	} else {
		options.BaseURL = NormalizeAnthropicBaseURL(options.BaseURL)
	}
	set := func(name, value string) {
		raw, _ := json.Marshal(value)
		options.DefaultHeaders.Set(name, raw)
	}
	userType := env["USER_TYPE"]
	if userType == "" {
		userType = "external"
	}
	// Internal desktop identity controls OAuth and traces; upstream wire
	// identity remains the CLI identity from the frozen reference version.
	agent := "claude-cli/" + SourceCompatibilityVersion + " (" + userType + ", cli"
	if version := env["CLAUDE_AGENT_SDK_VERSION"]; version != "" {
		agent += ", agent-sdk/" + version
	}
	if app := env["CLAUDE_AGENT_SDK_CLIENT_APP"]; app != "" {
		agent += ", client-app/" + app
	}
	if input.Workload != "" {
		agent += ", workload/" + input.Workload
	}
	// SDK 0.128 also parses this environment itself before merging the CLI's
	// validated defaults. Unlike the CLI parser it retains an empty name;
	// the real Headers constructor subsequently reports that malformed input.
	for _, line := range strings.Split(javascriptTrim(env["ANTHROPIC_CUSTOM_HEADERS"]), "\n") {
		name, value, found := strings.Cut(line, ":")
		name = javascriptTrim(name)
		if found && name != "__proto__" {
			set(name, javascriptTrim(value))
		}
	}
	set("x-app", "cli")
	set("User-Agent", agent+")")
	set("X-Claude-Code-Session-Id", input.SessionID)
	// The source first accumulates exact-case keys in an ordinary object. Its
	// __proto__ setter ignores the string value; later case variants retain
	// separate slots until SDK Headers applies its case-insensitive overwrite.
	custom := protocol.NewJSONObject()
	for _, line := range strings.Split(env["ANTHROPIC_CUSTOM_HEADERS"], "\n") {
		name, value, found := strings.Cut(line, ":")
		name = javascriptTrim(name)
		if !found || name == "" || name == "__proto__" {
			continue
		}
		raw, _ := json.Marshal(javascriptTrim(value))
		custom.Set(name, raw)
	}
	customJSON, _ := custom.MarshalJSON()
	for _, field := range readMessageFields(customJSON) {
		options.DefaultHeaders.Set(field.key, field.value)
	}
	for _, metadata := range [][2]string{
		{"x-claude-remote-container-id", env["CLAUDE_CODE_CONTAINER_ID"]},
		{"x-claude-remote-session-id", env["CLAUDE_CODE_REMOTE_SESSION_ID"]},
		{"x-client-app", env["CLAUDE_AGENT_SDK_CLIENT_APP"]},
	} {
		if metadata[1] != "" {
			set(metadata[0], metadata[1])
		}
	}
	if clientEnvTruthy(env["CLAUDE_CODE_ADDITIONAL_PROTECTION"]) {
		set("x-anthropic-additional-protection", "true")
	}
	if token := env["ANTHROPIC_AUTH_TOKEN"]; token != "" {
		set("Authorization", "Bearer "+token)
	}
	return options
}

func sourceOS(platform string) string {
	platform = strings.ToLower(platform)
	if strings.Contains(platform, "ios") {
		return "iOS"
	}
	switch platform {
	case "android":
		return "Android"
	case "darwin":
		return "MacOS"
	case "win32":
		return "Windows"
	case "freebsd":
		return "FreeBSD"
	case "openbsd":
		return "OpenBSD"
	case "linux":
		return "Linux"
	case "":
		return "Unknown"
	default:
		return "Other:" + platform
	}
}

func sourceArch(arch string) string {
	switch arch {
	case "x32", "arm", "arm64":
		return arch
	case "x86_64", "x64":
		return "x64"
	case "aarch64":
		return "arm64"
	case "":
		return "unknown"
	default:
		return "other:" + arch
	}
}

// StreamConfig applies SDK transport metadata and client defaults before body
// encoding and request-level overrides. Beta Messages copies the constructor
// timeout into the request options, so its validation and timeout header apply.
// Malformed Headers, proxy/TLS options and retries are outside this seam.
func (options DirectClientOptions) StreamConfig() AnthropicConfig {
	config := AnthropicConfig{BaseURL: options.BaseURL, DefaultHeaders: http.Header{}, FinalizeHeaders: options.finalFetchHeaders}
	requestMillis := options.TimeoutMillis
	config.TimeoutMillis = &requestMillis
	if options.APIKey != nil {
		config.APIKey, config.APIKeySet = *options.APIKey, true
	}
	if options.AuthToken != nil {
		config.AuthToken, config.AuthTokenSet = *options.AuthToken, true
	}
	millis := options.TimeoutMillis
	if math.IsNaN(millis) || millis < 1 || millis > math.MaxInt32 {
		config.Timeout = time.Millisecond
	} else {
		config.Timeout = time.Duration(millis) * time.Millisecond
	}
	for _, entry := range [][2]string{
		{"X-Stainless-Retry-Count", "0"},
		{"X-Stainless-Lang", "js"},
		{"X-Stainless-Package-Version", SourceSDKVersion},
		{"X-Stainless-OS", sourceOS(options.runtime.Platform)},
		{"X-Stainless-Arch", sourceArch(options.runtime.Arch)},
		{"X-Stainless-Runtime", "node"},
		{"X-Stainless-Runtime-Version", options.runtime.Version},
		{"anthropic-dangerous-direct-browser-access", "true"},
	} {
		config.DefaultHeaders.Set(entry[0], entry[1])
	}
	if millis != 0 && !math.IsNaN(millis) {
		seconds := math.Trunc(millis / 1000)
		value := strconv.FormatFloat(seconds, 'f', -1, 64)
		if seconds == 0 {
			value = "0"
		}
		config.DefaultHeaders.Set("X-Stainless-Timeout", value)
	}
	raw, _ := options.DefaultHeaders.MarshalJSON()
	for _, field := range readMessageFields(raw) {
		var value string
		_ = json.Unmarshal(field.value, &value)
		config.DefaultHeaders.Set(field.key, strings.Trim(value, "\t\r\n "))
		if strings.EqualFold(field.key, "x-stainless-retry-count") {
			copy := strings.Trim(value, "\t\r\n ")
			config.AuxiliaryRetryHeader = &copy
		}
	}
	return config
}
