package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/analytics"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"regexp"
)

func (m *MainPrint) initializeNativeFileAnalytics(scope *cooperative.Scope) error {
	if !m.options.Native.NativeProcess {
		return nil
	}
	if parent := m.nativeHelperParent(); parent != nil {
		// Source's logger is process-global. Agent owners borrow its producer,
		// not a second exporter or a second failed-event directory.
		m.analyticsParent = parent
		return nil
	}
	refs := &ownerasync.NativeReferences{}
	binding, e := refs.Bind(m.Owner)
	if e != nil {
		return e
	}
	n := m.options.Native
	m.fileAnalytics, e = analytics.New(scope, analytics.Options{Owner: m.Owner, Binding: binding, ConfigDir: n.Environment["CLAUDE_CONFIG_DIR"], UUID: uuidMain, SessionID: func(*cooperative.Scope) string { return m.Controller.SessionID() }, Environment: func(*cooperative.Scope) map[string]string { return m.nativeOpenAIEnvironment() }, Config: m.nativeAnalyticsConfig, Random: randomNativeFloat, Record: m.nativeAnalyticsRecord, Prepare: m.nativeAnalyticsRequest, HTTP: m.nativeAnalyticsHTTP, Files: analytics.OSFiles{Check: func(path string) error { return n.check("telemetry", path) }}})
	if e != nil {
		return e
	}
	return m.fileAnalytics.RetryPrevious(scope)
}

func (m *MainPrint) nativeFileReporter() func(*cooperative.Scope, analytics.Event) error {
	if m.fileAnalytics == nil && m.analyticsParent == nil {
		return nil
	}
	return m.reportNativeFileEvent
}

func (m *MainPrint) closeNativeFileAnalytics() {
	if m.fileAnalytics == nil {
		return
	}
	_ = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error { return m.fileAnalytics.Shutdown(scope) })
}

func randomNativeFloat() float64 {
	// The same non-cryptographic purpose as Math.random; no model outcome enters
	// sampling. The entropy helper already belongs to the process boundary.
	raw := uuidMain()
	var seed uint64
	for _, r := range raw {
		seed = seed*131 + uint64(r)
	}
	return float64(seed>>11) / float64(uint64(1)<<53)
}

func (m *MainPrint) nativeAnalyticsConfig(scope *cooperative.Scope, name string) protocol.JSONObject {
	if !scope.BelongsTo(m.Owner) {
		return protocol.JSONObject{}
	}
	env := m.nativeOpenAIEnvironment()
	// This fork has no GrowthBook clientKey. External disk features are not a
	// live config producer; original ant override precedence still applies.
	if env["USER_TYPE"] != "ant" {
		return protocol.NewJSONObject()
	}
	var overrides protocol.JSONObject
	if json.Unmarshal([]byte(env["CLAUDE_INTERNAL_FC_OVERRIDES"]), &overrides) == nil && overrides.Get(name) != nil {
		var value protocol.JSONObject
		_ = json.Unmarshal(overrides.Get(name), &value)
		return value
	}
	global, e := m.Global.Get()
	if e != nil || global == nil {
		return protocol.NewJSONObject()
	}
	_ = json.Unmarshal(global.Get("growthBookOverrides"), &overrides)
	var value protocol.JSONObject
	_ = json.Unmarshal(overrides.Get(name), &value)
	return value
}

func (m *MainPrint) reportNativeFileEvent(scope *cooperative.Scope, event analytics.Event) error {
	if !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	if e := scope.Inline(cooperative.Read, func(*cooperative.Scope) error { return nil }); e != nil {
		return e
	}
	if event.Context == nil {
		attribution := protocol.NewJSONObject()
		if m.Host != nil {
			if model, e := m.Host.InferenceModel(scope); e == nil {
				attribution.Set("model", quoteMain(model))
			}
		}
		if m.options.AgentID != "" {
			attribution.Set("agentId", quoteMain(m.options.AgentID))
			attribution.Set("agentType", quoteMain("subagent"))
			if detached := m.options.DetachedAgent; detached != nil {
				attribution.Set("parentSessionId", quoteMain(detached.Bootstrap.SessionID))
				if detached.teammate != nil {
					attribution.Set("agentType", quoteMain("teammate"))
					attribution.Set("teamName", quoteMain(detached.teammate.TeamName))
				}
			}
		}
		event.Context = &attribution
	}
	if parent := m.analyticsParent; parent != nil {
		event.Metadata = event.Metadata.Clone()
		_, e := parent.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(next *cooperative.Scope) error { return parent.reportNativeFileEvent(next, event) })
		return e
	}
	if m.fileAnalytics == nil {
		return mainCap("native file analytics producer")
	}
	return m.fileAnalytics.Log(scope, event)
}

func (m *MainPrint) nativeAnalyticsRecord(scope *cooperative.Scope, event analytics.Event) (protocol.JSONObject, error) {
	if !scope.BelongsTo(m.Owner) {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	env := m.nativeOpenAIEnvironment()
	global, e := m.Global.Get()
	if e != nil {
		return protocol.JSONObject{}, e
	}
	model := m.options.Arguments.Model
	if m.Host != nil {
		if selected, e := m.Host.InferenceModel(scope); e == nil {
			model = selected
		}
	}
	packages := []string{}
	runtimes := []string{}
	for _, command := range []string{"npm", "yarn", "pnpm"} {
		if nativeAnalyticsExecutable(command, env) {
			packages = append(packages, command)
		}
	}
	for _, command := range []string{"bun", "deno", "node"} {
		if nativeAnalyticsExecutable(command, env) {
			runtimes = append(runtimes, command)
		}
	}
	platform := m.options.Platform
	if env["CLAUDE_CODE_HOST_PLATFORM"] != "" {
		platform = env["CLAUDE_CODE_HOST_PLATFORM"]
	}
	environmentContext := protocol.NewJSONObject(protocol.JSONField{Name: "platform", Value: quoteMain(platform)}, protocol.JSONField{Name: "platformRaw", Value: quoteMain(platform)}, protocol.JSONField{Name: "arch", Value: quoteMain(nativeAnalyticsArch())}, protocol.JSONField{Name: "terminal", Value: quoteMain(env["TERM_PROGRAM"])}, protocol.JSONField{Name: "packageManagers", Value: quoteMain(strings.Join(packages, ","))}, protocol.JSONField{Name: "runtimes", Value: quoteMain(strings.Join(runtimes, ","))}, protocol.JSONField{Name: "isRunningWithBun", Value: json.RawMessage("false")}, protocol.JSONField{Name: "version", Value: quoteMain(m.options.Version)}, protocol.JSONField{Name: "deploymentEnvironment", Value: quoteMain(nativeAnalyticsDeployment(env, m.options.Native))})
	if terminal := nativeAnalyticsTerminal(env, m.options.Interactive); terminal != nil {
		environmentContext.Set("terminal", quoteMain(*terminal))
	} else {
		environmentContext.Set("terminal", json.RawMessage("null"))
	}
	for _, pair := range []struct{ key, variable string }{{"isCi", "CI"}, {"isClaubbit", "CLAUBBIT"}, {"isClaudeCodeRemote", "CLAUDE_CODE_REMOTE"}, {"isGithubAction", "GITHUB_ACTIONS"}, {"isClaudeCodeAction", "CLAUDE_CODE_ACTION"}} {
		environmentContext.Set(pair.key, rawBoolMain(envTruthyNative(env[pair.variable])))
	}
	environmentContext.Set("isLocalAgentMode", rawBoolMain(env["CLAUDE_CODE_ENTRYPOINT"] == "local-agent"))
	environmentContext.Set("isConductor", rawBoolMain(env["CONDUCTOR_WORKSPACE_ID"] != ""))
	environmentContext.Set("isClaudeAiAuth", json.RawMessage("false"))
	user := protocol.NewJSONObject()
	if m.openAIAuth != nil {
		identity, e := m.openAIAuth.AnthropicModelIdentity(context.Background(), scope, !m.options.Interactive)
		if e != nil {
			return protocol.JSONObject{}, e
		}
		environmentContext.Set("isClaudeAiAuth", rawBoolMain(identity.Subscriber))
		if identity.Subscriber {
			var account protocol.JSONObject
			_ = json.Unmarshal(global.Get("oauthAccount"), &account)
			for _, pair := range []struct{ key, source string }{{"accountUuid", "accountUuid"}, {"organizationUuid", "organizationUuid"}, {"email", "emailAddress"}} {
				user.Set(pair.key, account.Get(pair.source))
			}
		}
	}
	for _, pair := range []struct{ key, variable string }{{"remoteEnvironmentType", "CLAUDE_CODE_REMOTE_ENVIRONMENT_TYPE"}, {"claudeCodeContainerId", "CLAUDE_CODE_CONTAINER_ID"}, {"claudeCodeRemoteSessionId", "CLAUDE_CODE_REMOTE_SESSION_ID"}, {"tags", "CLAUDE_CODE_TAGS"}, {"githubEventName", "GITHUB_EVENT_NAME"}, {"githubActionsRunnerEnvironment", "RUNNER_ENVIRONMENT"}, {"githubActionsRunnerOs", "RUNNER_OS"}} {
		if env[pair.variable] != "" {
			environmentContext.Set(pair.key, quoteMain(env[pair.variable]))
		}
	}
	if envTruthyNative(env["GITHUB_ACTIONS"]) {
		gh := protocol.NewJSONObject()
		for _, pair := range []struct{ key, variable string }{{"actorId", "GITHUB_ACTOR_ID"}, {"repositoryId", "GITHUB_REPOSITORY_ID"}, {"repositoryOwnerId", "GITHUB_REPOSITORY_OWNER_ID"}} {
			if env[pair.variable] != "" {
				gh.Set(pair.key, quoteMain(env[pair.variable]))
			}
		}
		raw, _ := gh.MarshalJSON()
		user.Set("githubActionsMetadata", raw)
	}
	rawContext, _ := environmentContext.MarshalJSON()
	core := protocol.NewJSONObject(protocol.JSONField{Name: "model", Value: quoteMain(model)}, protocol.JSONField{Name: "sessionId", Value: quoteMain(m.Controller.SessionID())}, protocol.JSONField{Name: "userType", Value: quoteMain(env["USER_TYPE"])}, protocol.JSONField{Name: "isInteractive", Value: quoteMain(map[bool]string{true: "true", false: "false"}[m.options.Interactive])}, protocol.JSONField{Name: "clientType", Value: quoteMain("cli")}, protocol.JSONField{Name: "envContext", Value: rawContext})
	if settings, e := m.Startup.GetInitialSettings(); e == nil {
		if betas, e := requestcontext.DirectCountingBetas(model, env, settings.Clone()); e == nil && len(betas) > 0 {
			if redacted, e := m.nativeThinkingSummaryBeta(scope, model); e == nil {
				betas = requestcontext.WithThinkingSummaryBeta(betas, redacted)
			}
			core.Set("betas", quoteMain(strings.Join(betas, ",")))
		}
	}
	if build := env["CLAUDE_CODE_LOCAL_BUILD_TIME"]; build != "" {
		environmentContext.Set("buildTime", quoteMain(build))
	}
	if base := regexp.MustCompile(`^\d+\.\d+\.\d+(?:-[a-z]+)?`).FindString(m.options.Version); base != "" {
		environmentContext.Set("versionBase", quoteMain(base))
	}
	rawContext, _ = environmentContext.MarshalJSON()
	core.Set("envContext", rawContext)
	for _, pair := range []struct{ key, variable string }{{"entrypoint", "CLAUDE_CODE_ENTRYPOINT"}, {"agentSdkVersion", "CLAUDE_AGENT_SDK_VERSION"}, {"sweBenchRunId", "SWE_BENCH_RUN_ID"}, {"sweBenchInstanceId", "SWE_BENCH_INSTANCE_ID"}, {"sweBenchTaskId", "SWE_BENCH_TASK_ID"}} {
		if env[pair.variable] != "" {
			core.Set(pair.key, quoteMain(env[pair.variable]))
		}
	}
	if m.options.AgentID != "" {
		core.Set("agentId", quoteMain(m.options.AgentID))
		core.Set("agentType", quoteMain("subagent"))
	}
	if event.Context != nil {
		core.Assign(*event.Context)
	}
	return analytics.Record(analytics.RecordInput{Event: event, ID: uuidMain(), UserID: global.String("userID"), Timestamp: m.options.Native.Now(), Core: core, User: user})
}

func rawBoolMain(value bool) json.RawMessage {
	if value {
		return json.RawMessage("true")
	}
	return json.RawMessage("false")
}
func nativeAnalyticsExecutable(name string, env map[string]string) bool {
	for _, directory := range filepath.SplitList(env["PATH"]) {
		if directory == "" {
			continue
		}
		candidate := filepath.Join(directory, name)
		if found, e := exec.LookPath(candidate); e == nil && found != "" {
			return true
		}
	}
	return false
}

func (m *MainPrint) nativeAnalyticsRequest(scope *cooperative.Scope, target string, body []byte, skipAuth bool) (analytics.Request, error) {
	if !scope.BelongsTo(m.Owner) {
		return analytics.Request{}, cooperative.ErrRestricted
	}
	env := m.nativeOpenAIEnvironment()
	headers := http.Header{}
	headers.Set("Content-Type", "application/json")
	headers.Set("User-Agent", "claude-code/"+m.options.Version)
	headers.Set("x-service-name", "claude-code")
	trust := !m.options.Interactive
	if !trust {
		trust, _ = m.nativeGoalTrust(scope)
	}
	if !skipAuth && trust && m.openAIAuth != nil {
		subscriber, e := m.openAIAuth.IsClaudeSubscriber(context.Background(), scope, !m.options.Interactive)
		if e != nil {
			return analytics.Request{}, e
		}
		if subscriber {
			tokens, e := m.openAIAuth.claudeTokensFor(context.Background(), env, envTruthyNative(env["CLAUDE_CODE_SIMPLE"]))
			if e != nil {
				return analytics.Request{}, e
			}
			if tokens != nil {
				var scopes []string
				_ = json.Unmarshal(tokens.Object.Get("scopes"), &scopes)
				profile := false
				for _, s := range scopes {
					profile = profile || s == "user:profile"
				}
				var expires float64
				_ = json.Unmarshal(tokens.Object.Get("expiresAt"), &expires)
				hasExpiry := tokens.Object.Get("expiresAt") != nil
				expired := hasExpiry && string(tokens.Object.Get("expiresAt")) != "null" && float64(m.options.Native.Now().UnixMilli()+5*60*1000) >= expires
				if profile && !expired && tokens.Object.String("accessToken") != "" {
					headers.Set("Authorization", "Bearer "+tokens.Object.String("accessToken"))
					headers.Set("anthropic-beta", "oauth-2025-04-20")
				}
			}
		} else {
			source, e := m.nativeAPIKeySource(scope)
			if e != nil {
				return analytics.Request{}, e
			}
			key := ""
			switch source {
			case "ANTHROPIC_API_KEY":
				key = env["ANTHROPIC_API_KEY"]
			case "apiKeyHelper":
				if m.apiKeyHelper != nil {
					if cached := m.apiKeyHelper.Cached(); cached != nil {
						key = *cached
					}
				}
			case "/login managed key":
				key = m.openAIAuth.legacyAPIKey
			}
			if key != "" {
				headers.Set("x-api-key", key)
			}
		}
	}
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 30 * time.Second}).DialContext, TLSHandshakeTimeout: 10 * time.Second, DisableKeepAlives: true}
	selection := provider.ResolveNativeProxySelection(target, env)
	if selection.ProxyURL != "" && !selection.Bypass {
		proxy, e := url.Parse(selection.ProxyURL)
		if e != nil {
			return analytics.Request{}, e
		}
		transport.Proxy = http.ProxyURL(proxy)
	}
	if strings.HasPrefix(target, "https:") {
		configuration, e := m.tls.VerifiedConfig(env)
		if e != nil {
			return analytics.Request{}, e
		}
		transport.TLSClientConfig = configuration
	}
	return analytics.Request{URL: target, Headers: headers, Body: append([]byte(nil), body...), Timeout: 10 * time.Second, Client: &http.Client{Transport: transport}}, nil
}

func (m *MainPrint) nativeAnalyticsHTTP(ctx context.Context, input analytics.Request) (analytics.Response, error) {
	if m.options.AnalyticsHTTP != nil {
		return m.options.AnalyticsHTTP(ctx, input)
	}
	ctx, cancel := context.WithTimeout(ctx, input.Timeout)
	defer cancel()
	request, e := http.NewRequestWithContext(ctx, "POST", input.URL, bytes.NewReader(input.Body))
	if e != nil {
		return analytics.Response{}, e
	}
	request.Header = input.Headers.Clone()
	response, e := input.Client.Do(request)
	if e != nil {
		return analytics.Response{}, e
	}
	defer response.Body.Close()
	defer input.Client.CloseIdleConnections()
	body, e := io.ReadAll(response.Body)
	return analytics.Response{Status: response.StatusCode, Headers: response.Header.Clone(), Body: body}, e
}
