package requestcontext

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestmapping"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolsearch"
	weburl "github.com/nlnwa/whatwg-url/url"
)

func checkProfile(input Context) error {
	if input.Host.Profile != DirectRelayProfile && input.Host.Profile != NativeDirectRelayProfile {
		return unsupported("undeclared request construction profile")
	}
	baseURL := input.Host.Env["ANTHROPIC_BASE_URL"]
	boundBedrock := input.Host.Profile == NativeDirectRelayProfile && input.Host.NativeBedrockBound && input.Host.NativeBedrockProof.Current() && input.Host.NativeBedrockCanonical != nil && envTrue(input.Host.Env["CLAUDE_CODE_USE_BEDROCK"])
	boundVertex := input.Host.Profile == NativeDirectRelayProfile && input.Host.NativeVertexBound && input.Host.NativeVertexProof.Current() && input.Host.NativeVertexCanonical != nil && envTrue(input.Host.Env["CLAUDE_CODE_USE_VERTEX"])
	boundClaude := input.Host.Profile == NativeDirectRelayProfile && input.Host.NativeClaudeSubscriber && input.Host.NativeClaudeProof.Current()
	boundCodex := input.Host.Profile == NativeDirectRelayProfile && (input.Host.NativeCodexOAuth != nil && input.Host.NativeCodexOAuth.HasAuth != nil && input.Host.NativeCodexOAuth.FreshTokens != nil || input.Host.NativeGrokOAuth != nil && input.Host.NativeGrokOAuth.HasAuth != nil && input.Host.NativeGrokOAuth.FreshTokens != nil)
	if baseURL == "" && (boundClaude || boundCodex || boundBedrock || boundVertex) {
		baseURL = "https://api.anthropic.com"
	}
	base, err := weburl.Parse(baseURL)
	if err != nil || base.Host() == "" || !boundClaude && !boundCodex && !boundBedrock && !boundVertex && (base.Hostname() == "api.anthropic.com" || base.Hostname() == "api-staging.anthropic.com") {
		return unsupported("first-party or unresolved provider profile")
	}
	for _, name := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_AZURE_OPENAI", "USER_TYPE"} {
		if name == "CLAUDE_CODE_USE_VERTEX" && boundVertex {
			continue
		}
		if name == "CLAUDE_CODE_USE_BEDROCK" && boundBedrock {
			continue
		}
		if input.Host.Env[name] != "" {
			return unsupported("provider/internal capability " + name)
		}
	}
	for name := range input.Host.Env {
		if !supportedHostEnvironment[name] && (input.Host.Profile != NativeDirectRelayProfile || !IsNativePolicyEnvironment(name) && !(boundBedrock && IsNativeBedrockEnvironment(name)) && !(boundVertex && IsNativeVertexEnvironment(name)) && !(input.Host.NativeGrokOAuth != nil && (name == "CC_HAHA_GROK_OAUTH_PROVIDER" || name == "GROK_OAUTH_FILE"))) {
			return unsupported("unported environment/config capability " + name)
		}
	}
	if input.Host.DeviceID == "" || input.Host.SessionID == "" {
		return unsupported("unresolved device/session metadata")
	}
	if input.Thinking.Type != "disabled" && input.Thinking.Type != "enabled" && input.Thinking.Type != "adaptive" {
		return unsupported("thinking mode")
	}
	return nil
}

var supportedHostEnvironment = map[string]bool{
	"ANTHROPIC_BASE_URL": true, "ANTHROPIC_API_KEY": true, "ANTHROPIC_AUTH_TOKEN": true, "ANTHROPIC_CUSTOM_HEADERS": true,
	"API_TIMEOUT_MS": true, "CLAUDE_CODE_ENTRYPOINT": true, "CLAUDE_AGENT_SDK_VERSION": true, "CLAUDE_AGENT_SDK_CLIENT_APP": true,
	"CLAUDE_CODE_CONTAINER_ID": true, "CLAUDE_CODE_REMOTE_SESSION_ID": true, "CLAUDE_CODE_ADDITIONAL_PROTECTION": true,
	"CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST": true, "CC_HAHA_LOCAL_ACCESS_TOKEN": true,
	"CLAUDE_CODE_EXTRA_BODY": true, "CLAUDE_CODE_EXTRA_METADATA": true, "CLAUDE_CODE_MAX_OUTPUT_TOKENS": true,
	"CLAUDE_CODE_PROVIDER_MAX_OUTPUT_TOKENS": true, "CLAUDE_CODE_DISABLE_THINKING": true, "CLAUDE_CODE_DISABLE_ADAPTIVE_THINKING": true,
	"CC_HAHA_SEND_DISABLED_THINKING": true, "DISABLE_PROMPT_CACHING": true, "CLAUDE_CODE_ATTRIBUTION_HEADER": true,
	"ANTHROPIC_BETAS": true, "CLAUDE_CODE_ALWAYS_ENABLE_EFFORT": true, "CLAUDE_CODE_EFFORT_LEVEL": true,
	"CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS": true,
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
func spread(destination *requestmapping.Value, raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	// Decode each quoted key through the existing UTF-16-safe storage boundary.
	// The ordered input is already an object; never sort or range over its map.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	_, _ = decoder.Token()
	for decoder.More() {
		start := decoder.InputOffset()
		_, _ = decoder.Token()
		key := raw[start:decoder.InputOffset()]
		key = key[bytes.IndexByte(key, '"'):]
		var value json.RawMessage
		_ = decoder.Decode(&value)
		singleton := append(append([]byte{'{'}, key...), []byte(":null}")...)
		var names storage.Object
		_ = json.Unmarshal(singleton, &names)
		child, err := requestmapping.ParseValue(value)
		if err != nil {
			return err
		}
		for name := range names {
			destination.SetProperty(name, child)
		}
	}
	return nil
}

// Prepare covers the explicit no-experiment direct-relay profile. Source
// performs this work before paramsFromContext and its creation retry loop.
// Unsupported input is returned before a transport request can be sent.
func Prepare(input Context, dependencies Dependencies) (*PreparedContext, error) {
	if err := checkProfile(input); err != nil {
		return nil, err
	}
	fallback, upper, err := modelLimits(input.Options.Model)
	if input.Host.Profile == NativeDirectRelayProfile {
		fallback, upper = nativeModelLimits(input.Options.Model)
		err = nil
	}
	if err != nil {
		return nil, err
	}
	definitions := make([]toolschema.Definition, 0, len(input.Tools))
	for i, original := range input.Tools {
		var definition toolschema.Definition
		var err error
		native := input.Host.Profile == NativeDirectRelayProfile && input.Host.NativeToolDefinitions != nil
		if native {
			if len(*input.Host.NativeToolDefinitions) != len(input.Tools) {
				return nil, unsupported("native executable declaration count")
			}
			definition = (*input.Host.NativeToolDefinitions)[i]
			if definition.Name != original.String("name") || definition.Kind != original.String("kind") {
				return nil, unsupported("native executable declaration identity")
			}
		} else {
			definition, err = toolschema.ParseDefinition(original)
		}
		if err != nil {
			return nil, unsupported(err.Error())
		}
		if toolschema.MatchesName(definition, "ToolSearch") && (!native || input.Host.NativeToolSearch == nil || input.Host.ToolSearchCatalogue == nil) || strings.HasPrefix(definition.Name, "mcp__") && !native {
			return nil, unsupported("tool search/MCP native definition")
		}
		definitions = append(definitions, definition)
	}
	searchPlan := toolsearch.RequestPlan{}
	if input.Host.NativeToolSearch != nil {
		catalogue := input.Host.ToolSearchCatalogue
		if input.Host.Profile != NativeDirectRelayProfile || catalogue == nil || len(catalogue.Tools) != len(definitions) {
			return nil, unsupported("actual owning native ToolSearch catalogue")
		}
		for i, tool := range catalogue.Tools {
			if stringValue(tool.Name) != definitions[i].Name {
				return nil, unsupported("native ToolSearch catalogue identity/order")
			}
		}
		searchPlan, err = input.Host.NativeToolSearch.Plan(context.Background(), input.Options.Model, catalogue.Tools, input.History, len(catalogue.PendingServers) > 0, catalogue.Gate)
		if err != nil {
			return nil, err
		}
		selected := []toolschema.Definition{}
		for _, index := range searchPlan.Included {
			selected = append(selected, definitions[index])
		}
		definitions = selected
	}
	schemas := append([]json.RawMessage{}, input.Options.ExtraToolSchemas...)
	if len(definitions) > 0 {
		if input.Host.ToolSchemas == nil || input.Host.ToolSchemaHost == nil {
			return nil, unsupported("unresolved native tool session/prompt inputs")
		}
		host := *input.Host.ToolSchemaHost
		host.BaseURL = input.Host.Env["ANTHROPIC_BASE_URL"]
		host.DisableExperimentalBetas = envTrue(input.Host.Env["CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS"])
		// The declared relay profile has no tool search. Source calls
		// toolToAPISchema without cacheControl here; cache markers are overlays
		// only for standalone callers of that function.
		host.ToolSearchDeltaEnabled = searchPlan.Delta
		rendered := []protocol.JSONObject{}
		if input.Host.NativeToolSearch == nil {
			rendered, err = input.Host.ToolSchemas.RenderBatch(definitions, toolschema.Options{Host: host, Model: input.Options.Model})
		} else {
			for _, definition := range definitions {
				deferLoading := false
				for _, name := range searchPlan.Deferred {
					if stringValue(name) == definition.Name {
						deferLoading = searchPlan.Enabled
						break
					}
				}
				var current []protocol.JSONObject
				current, err = input.Host.ToolSchemas.RenderBatch([]toolschema.Definition{definition}, toolschema.Options{Host: host, Model: input.Options.Model, DeferLoading: deferLoading})
				if err != nil {
					break
				}
				rendered = append(rendered, current...)
			}
		}
		if err != nil {
			return nil, unsupported(err.Error())
		}
		schemas = make([]json.RawMessage, 0, len(rendered)+len(input.Options.ExtraToolSchemas))
		for _, schema := range rendered {
			schemas = append(schemas, rawObject(schema))
		}
		schemas = append(schemas, input.Options.ExtraToolSchemas...)
	}
	optimistic := input.Host.ToolSearchCatalogue != nil && toolsearch.Optimistic(input.Host.ToolSearchCatalogue.Gate.Environment)
	normalized, err := normalizeHistoryForModelWithToolSearch(input.History, dependencies, definitions, input.Options.Model, input.Options.IsNonInteractiveSession, input.Host.PlanOptions, optimistic)
	if err != nil {
		return nil, err
	}
	if !searchPlan.Enabled {
		normalized = stripSearchFieldsAfterModelGate(normalized)
	}
	caching := !envTrue(input.Host.Env["DISABLE_PROMPT_CACHING"])
	if input.Options.EnablePromptCaching != nil {
		caching = *input.Options.EnablePromptCaching
	}
	system, err := buildSystem(input, normalized, caching)
	if err != nil {
		return nil, err
	}
	if searchPlan.Enabled {
		if searchPlan.Delta {
			return nil, unsupported("original deferred-tools delta attachment producer")
		}
		normalized, err = prependNativeDeferredTools(normalized, searchPlan.Deferred, dependencies)
		if err != nil {
			return nil, err
		}
	}
	var previous json.RawMessage
	for index := len(input.History) - 1; index >= 0; index-- {
		if input.History[index].String("type") == "assistant" && truthy(input.History[index].Get("requestId")) {
			previous = input.History[index].Get("requestId")
			break
		}
	}
	return &PreparedContext{toolSearchPlan: searchPlan, input: input, normalized: normalized, schemas: schemas, system: system, fallback: fallback, upper: upper, previous: previous}, nil
}

// Build preserves the existing single-attempt construction API.
func Build(input Context, dependencies Dependencies) (*Result, error) {
	prepared, err := Prepare(input, dependencies)
	if err != nil {
		return nil, err
	}
	return prepared.BuildAttempt(AttemptOptions{Model: input.Options.Model})
}

func numberTruthy(value *float64) bool {
	return value != nil && *value != 0 && !math.IsNaN(*value)
}

// NormalizedHistory exposes a read-only observation snapshot without building
// an attempt. Source prepares history even when the retry scheduler subsequently
// sees an already-aborted caller signal.
func (prepared *PreparedContext) NormalizedHistory() []protocol.JSONObject {
	if prepared == nil {
		return nil
	}
	result := make([]protocol.JSONObject, len(prepared.normalized))
	for index, envelope := range prepared.normalized {
		result[index] = envelope.Clone()
	}
	return result
}

// BuildAttempt rebuilds the retry-sensitive request fields without repeating
// schema generation or history repair. Successful stream consumption remains
// outside this API and outside the caller's creation retry scheduler.
func (prepared *PreparedContext) BuildAttempt(attempt AttemptOptions) (*Result, error) {
	if prepared == nil {
		return nil, unsupported("missing prepared original context")
	}
	input := prepared.input
	if numberTruthy(attempt.MaxTokensOverride) {
		input.Options.MaxOutputTokensOverride = attempt.MaxTokensOverride
	}
	normalized := prepared.normalized
	schemas := prepared.schemas
	system := prepared.system
	fallback, upper := prepared.fallback, prepared.upper
	caching := !envTrue(input.Host.Env["DISABLE_PROMPT_CACHING"])
	if input.Options.EnablePromptCaching != nil {
		caching = *input.Options.EnablePromptCaching
	}
	values := make([]*requestmapping.Value, 0, len(normalized))
	for _, envelope := range normalized {
		value, err := requestmapping.ParseValue(rawObject(envelope))
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	querySource := input.Options.QuerySource
	mapped, err := requestmapping.AddCacheBreakpoints(values, caching, requestmapping.Options{QuerySource: &querySource, SkipCacheWrite: input.Options.SkipCacheWrite, CacheControl: func(source *string) (json.RawMessage, error) { return cacheControl(input.Host, *source, "org"), nil }})
	if err != nil {
		return nil, err
	}
	messageJSON, err := json.Marshal(mapped)
	if err != nil {
		return nil, err
	}
	extra := objectEnvironment(input.Host.Env["CLAUDE_CODE_EXTRA_BODY"])
	output, err := parseObject(extra.Get("output_config"))
	if err != nil {
		return nil, unsupported("non-object output_config spread")
	}
	if !output.IsObject() {
		output = object()
	}
	betas := []string{}
	boundClaude := input.Host.NativeClaudeSubscriber && input.Host.NativeClaudeProof.Current()
	if boundClaude {
		var err error
		betas, err = DirectCountingBetas(input.Options.Model, input.Host.Env, protocol.NewJSONObject())
		if err != nil {
			return nil, err
		}
		// getAllModelBetas adds OAuth immediately after the non-Haiku agent beta.
		at := 0
		if len(betas) > 0 && betas[0] == "claude-code-20250219" {
			at = 1
		}
		betas = append(betas, "")
		copy(betas[at+1:], betas[at:])
		betas[at] = "oauth-2025-04-20"
	}
	if !boundClaude && strings.Contains(strings.ToLower(input.Options.Model), "[1m]") && !envTrue(input.Host.Env["CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS"]) {
		betas = append(betas, "context-1m-2025-08-07")
	}
	for _, raw := range strings.Split(input.Host.Env["ANTHROPIC_BETAS"], ",") {
		if value := trim(raw); value != "" {
			betas = appendUnique(betas, value)
		}
	}
	for _, value := range input.Host.SDKBetas {
		betas = appendUnique(betas, value)
	}
	if input.Host.NativeVertexBound {
		betas, err = nativeVertexRequestBetas(input)
		if err != nil {
			return nil, err
		}
	}
	if input.Host.NativeBedrockBound {
		searchBeta := ""
		if prepared.toolSearchPlan.Enabled {
			searchBeta = prepared.toolSearchPlan.Beta
		}
		betas, err = nativeBedrockRequestBetas(input, &extra, attempt.Model, searchBeta)
		if err != nil {
			return nil, err
		}
	}
	if prepared.toolSearchPlan.Enabled && toolsearch.Provider(input.Host.ToolSearchCatalogue.Gate.Environment) != "bedrock" {
		betas = appendUnique(betas, prepared.toolSearchPlan.Beta)
	}
	if input.Host.Profile == NativeDirectRelayProfile {
		betas = nativeConfigureEffort(input, &output, betas)
	} else if envTrue(input.Host.Env["CLAUDE_CODE_ALWAYS_ENABLE_EFFORT"]) && output.Get("effort") == nil {
		effort, numeric := appliedEffort(input)
		if !numeric && (!envTrue(input.Host.Env["CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS"]) || localEffortProxy(input.Host.Env["ANTHROPIC_BASE_URL"])) {
			output.Set("effort", quoted(effort))
			betas = append(betas, "effort-2025-11-24")
		}
	}
	if truthy(input.Options.OutputFormat) && output.Get("format") == nil {
		output.Set("format", input.Options.OutputFormat)
	}
	max := maxTokens(input, fallback, upper)
	thinking, hasThinking := resolveThinking(input, max, upper)
	if input.Host.Profile == NativeDirectRelayProfile {
		thinking, hasThinking = nativeResolveThinking(input, max, upper)
		if thinking != nil && stringValue(output.Get("effort")) != "" && !hasThinking && strings.Contains(strings.ToLower(input.Options.Model), "claude-opus-5") && (stringValue(output.Get("effort")) == "xhigh" || stringValue(output.Get("effort")) == "max") {
			output.Set("effort", quoted("high"))
		}
	}
	boundModel := canonicalModel(input.Options.Model)
	if input.Host.Profile == NativeDirectRelayProfile {
		boundModel = nativeCanonicalModel(input.Options.Model)
	}
	if boundModel == "claude-fable-5-1" && hasThinking && (input.Host.Profile != NativeDirectRelayProfile || stringValue(objectEnvironment(string(thinking)).Get("type")) == "adaptive") {
		betas = appendUnique(betas, "thinking-binding-controls-2026-08-01")
	}
	fast, latched := false, false
	if input.Host.NativeFastModeProof != nil {
		fast, latched, err = input.Host.NativeFastModeProof.Resolve(input.Options.Model, attempt.FastMode)
		if err != nil {
			return nil, err
		}
	} else if attempt.FastMode != nil && *attempt.FastMode {
		return nil, unsupported("active fast mode without actual native state")
	}
	if latched {
		betas = appendUnique(betas, "fast-mode-2026-02-01")
	}
	metadataJSON, err := metadata(input)
	if err != nil {
		return nil, err
	}
	params := object(field("model", quoted(modelContextSuffix.ReplaceAllString(input.Options.Model, ""))), field("messages", messageJSON), field("system", system), field("tools", rawArray(schemas)), field("tool_choice", input.Options.ToolChoice))
	if len(betas) > 0 {
		raw, _ := json.Marshal(betas)
		params.Set("betas", raw)
	}
	params.Set("metadata", metadataJSON)
	params.Set("max_tokens", rawNumber(max))
	params.Set("thinking", thinking)
	if !hasThinking {
		temperature := 1.0
		if input.Options.TemperatureOverride != nil {
			temperature = *input.Options.TemperatureOverride
		}
		params.Set("temperature", rawNumber(temperature))
	}
	if fast {
		params.Set("speed", quoted("fast"))
	}
	value, err := requestmapping.ParseValue(rawObject(params))
	if err != nil {
		return nil, err
	}
	if err := spread(value, rawObject(extra)); err != nil {
		return nil, err
	}
	if string(rawObject(output)) != "{}" {
		config, err := requestmapping.ParseValue(rawObject(output))
		if err != nil {
			return nil, err
		}
		value.SetProperty("output_config", config)
	}
	stream, _ := requestmapping.ParseValue(json.RawMessage("true"))
	value.SetProperty("stream", stream)
	budget := "default"
	if numberTruthy(input.Options.MaxOutputTokensOverride) || configuredProviderBudget(input.Host.Env) != nil || safePositive(jsNumber(input.Host.Env["CLAUDE_CODE_MAX_OUTPUT_TOKENS"])) || extra.Get("max_tokens") != nil {
		budget = "explicit"
	}
	var requestHeaders []provider.HeaderEntry
	if localBudgetProxy(input.Host.Env["ANTHROPIC_BASE_URL"]) {
		name := budget
		requestHeaders = append(requestHeaders, provider.HeaderEntry{Name: "x-cc-haha-output-budget-source", Values: []*string{&name}})
	}
	request, err := provider.PrepareBetaMessages(value, provider.BetaMessageOptions{Headers: requestHeaders, DeferCCH: input.Host.NativeCodexOAuth != nil || input.Host.NativeGrokOAuth != nil})
	if err != nil {
		return nil, err
	}
	return &Result{Params: value, PreparedRequest: request, NormalizedHistory: normalized, BudgetSource: budget, PreviousRequestID: prepared.previous}, nil
}

func localBudgetProxy(base string) bool {
	parsed, err := weburl.Parse(base)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	return (host == "localhost" || host == "127.0.0.1" || host == "[::1]") && (parsed.Pathname() == "/proxy" || strings.HasPrefix(parsed.Pathname(), "/proxy/"))
}
func localEffortProxy(base string) bool {
	parsed, err := weburl.Parse(base)
	if err != nil {
		return false
	}
	path := strings.TrimRight(parsed.Pathname(), "/")
	return (parsed.Hostname() == "localhost" || parsed.Hostname() == "127.0.0.1") && (path == "/proxy" || strings.HasPrefix(path, "/proxy/providers/"))
}
