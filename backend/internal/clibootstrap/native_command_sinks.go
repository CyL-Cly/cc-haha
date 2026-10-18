package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/analytics"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/errorlog"
	"github.com/NanmiCoder/cc-haha/backend/internal/nativemodule"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
)

type nativeCommandSinksNamespace struct {
	command *MainCommand
	binding *nativeCommandInitBinding
}

func (namespace *nativeCommandSinksNamespace) check(scope *cooperative.Scope) error {
	if namespace == nil || namespace.command == nil || namespace.binding == nil {
		return cooperative.ErrRestricted
	}
	m := namespace.command
	if !m.ownsNativeCommandFactory() || namespace.binding != m.nativeCommandInit || !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	if err := scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil }); err != nil {
		return err
	}
	m.nativeAPIPreconnect.life.mu.Lock()
	closed := m.nativeAPIPreconnect.life.closed
	m.nativeAPIPreconnect.life.mu.Unlock()
	if closed {
		return cooperative.ErrClosed
	}
	return nil
}

// BeginNativeCommandSinks is the actual Commander continuation after await
// init(). A leaf Run does not acquire it, nor does constructing a command.
func (m *MainCommand) BeginNativeCommandSinks(ctx context.Context) error {
	if !m.ownsNativeCommandFactory() {
		return cooperative.ErrRestricted
	}
	return m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if !m.nativeAPIPreconnect.fired {
			return cooperative.ErrRestricted
		}
		if m.nativeSinksModule == nil {
			namespace := &nativeCommandSinksNamespace{m, m.nativeCommandInit}
			entry, err := nativemodule.NewPrelinked(m.Owner, scope, "utils/sinks", namespace, func(next *cooperative.Scope, key string, value *nativeCommandSinksNamespace) error {
				if key != "utils/sinks" || value != namespace {
					return cooperative.ErrRestricted
				}
				return namespace.check(next)
			})
			if err != nil {
				return err
			}
			m.nativeSinksModule = entry
		}
		pending, err := m.nativeSinksModule.Import(scope)
		if err != nil {
			return err
		}
		namespace, err := pending.Await(scope)
		if err != nil {
			return err
		}
		return namespace.initialize(scope)
	})
}

func (m *MainCommand) nativeCommandEnvironment() map[string]string {
	env := map[string]string{}
	if m.Settings == nil || m.Settings.environment == nil {
		for key, value := range m.Native.Environment {
			env[key] = value
		}
		return env
	}
	for _, property := range m.Settings.environment.Snapshot().OwnProperties() {
		var name, value string
		if json.Unmarshal(property.NameJSON, &name) == nil && json.Unmarshal(property.Value, &value) == nil {
			env[name] = value
		}
	}
	return env
}

func (namespace *nativeCommandSinksNamespace) initialize(scope *cooperative.Scope) error {
	if err := namespace.check(scope); err != nil {
		return err
	}
	m := namespace.command
	if m.commandErrorSink == nil {
		sink, err := errorlog.NewAndAttachNativeSink(errorlog.NativeSinkOptions{Files: nativeErrorLogFiles{m.Native}, Now: m.Native.Now,
			Arguments: m.commandArguments, Stderr: func(text string) { _, _ = io.WriteString(m.Error, text) },
			Metadata: func() errorlog.Metadata {
				return errorlog.Metadata{Environment: m.nativeCommandEnvironment(), CWD: m.Native.CWD, Home: m.Native.Home, ConfigDir: m.Native.ConfigHomeDirectory(), Platform: m.credentialPlatform, SessionID: m.commandSessionID, Version: m.version}
			},
		}, m.commandErrors)
		m.commandErrorSink = sink
		if err != nil {
			return err
		}
	} else if err := m.commandErrorSink.Debug("Error log sink initialized", "DEBUG"); err != nil {
		return err
	}
	if m.commandAnalytics == nil {
		binding, err := m.nativeInitRefs.Bind(m.Owner)
		if err != nil {
			return err
		}
		service, err := analytics.New(scope, analytics.Options{Owner: m.Owner, Binding: binding, ConfigDir: m.Native.ConfigHomeDirectory(), UUID: uuidMain,
			SessionID: func(*cooperative.Scope) string { return m.commandSessionID }, Environment: func(*cooperative.Scope) map[string]string { return m.nativeCommandEnvironment() },
			Config: m.nativeCommandAnalyticsConfig, Random: randomNativeFloat, Record: m.nativeCommandAnalyticsRecord, BeginRecord: m.beginNativeCommandAnalyticsRecord, Prepare: m.nativeCommandAnalyticsRequest,
			HTTP: m.nativeCommandAnalyticsHTTP, Files: analytics.OSFiles{Check: func(path string) error { return m.Native.check("command telemetry", path) }}})
		if err != nil {
			return err
		}
		m.commandAnalytics = service
		if err := service.RetryPrevious(scope); err != nil {
			return err
		}
		m.commandAnalyticsSink = &analytics.PublicSink{Log: service.Log, LogAsync: func(next *cooperative.Scope, event analytics.Event) (*ownerasync.OwnerFuture[struct{}], error) {
			result, err := ownerasync.NewOwnerFuture[struct{}](m.Owner)
			if err != nil {
				return nil, err
			}
			if err = service.Log(next, event); err != nil {
				return result, result.Reject(next, err)
			}
			return result, result.Resolve(next, struct{}{})
		}}
	}
	return m.commandEvents.Attach(scope, m.commandAnalyticsSink)
}

func (m *MainCommand) nativeCommandAnalyticsConfig(scope *cooperative.Scope, name string) protocol.JSONObject {
	if !scope.BelongsTo(m.Owner) || m.nativeCommandEnvironment()["USER_TYPE"] != "ant" {
		return protocol.NewJSONObject()
	}
	var overrides protocol.JSONObject
	if json.Unmarshal([]byte(m.nativeCommandEnvironment()["CLAUDE_INTERNAL_FC_OVERRIDES"]), &overrides) == nil && overrides.Get(name) != nil {
		var value protocol.JSONObject
		_ = json.Unmarshal(overrides.Get(name), &value)
		return value
	}
	global, err := m.Global.Get()
	if err != nil || global == nil {
		return protocol.NewJSONObject()
	}
	_ = json.Unmarshal(global.Get("growthBookOverrides"), &overrides)
	var value protocol.JSONObject
	_ = json.Unmarshal(overrides.Get(name), &value)
	return value
}

func (m *MainCommand) nativeCommandAnalyticsRecord(scope *cooperative.Scope, event analytics.Event) (protocol.JSONObject, error) {
	prefix, err := m.nativeCommandAnalyticsPrefix(scope, event)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	return m.nativeCommandAnalyticsRecordWithPrefix(scope, event, prefix)
}

func (m *MainCommand) nativeCommandAnalyticsRecordWithPrefix(scope *cooperative.Scope, event analytics.Event, prefix nativeAnalyticsMetadataPrefix) (protocol.JSONObject, error) {
	if !scope.BelongsTo(m.Owner) {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	env := m.nativeCommandEnvironment()
	_, tokens, _, enabled, err := m.cliAuthIdentity(context.Background(), scope, false)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	subscriber, err := openaiauth.ClaudeSubscriber(enabled, tokens)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	global, err := m.Global.Get()
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if global == nil {
		empty := protocol.NewJSONObject()
		global = &empty
	}
	packages, runtimes := []string{}, []string{}
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
	platform := m.credentialPlatform
	if env["CLAUDE_CODE_HOST_PLATFORM"] != "" {
		platform = env["CLAUDE_CODE_HOST_PLATFORM"]
	}
	environment := protocol.NewJSONObject(protocol.JSONField{Name: "platform", Value: quoteMain(platform)}, protocol.JSONField{Name: "platformRaw", Value: quoteMain(platform)}, protocol.JSONField{Name: "arch", Value: quoteMain(nativeAnalyticsArch())}, protocol.JSONField{Name: "terminal", Value: json.RawMessage("null")}, protocol.JSONField{Name: "packageManagers", Value: quoteMain(strings.Join(packages, ","))}, protocol.JSONField{Name: "runtimes", Value: quoteMain(strings.Join(runtimes, ","))}, protocol.JSONField{Name: "isRunningWithBun", Value: rawBoolMain(false)}, protocol.JSONField{Name: "version", Value: quoteMain(m.version)}, protocol.JSONField{Name: "deploymentEnvironment", Value: quoteMain(nativeAnalyticsDeployment(env, m.Native))})
	if terminal := nativeAnalyticsTerminal(env, false); terminal != nil {
		environment.Set("terminal", quoteMain(*terminal))
	}
	for _, pair := range []struct{ key, variable string }{{"isCi", "CI"}, {"isClaubbit", "CLAUBBIT"}, {"isClaudeCodeRemote", "CLAUDE_CODE_REMOTE"}, {"isGithubAction", "GITHUB_ACTIONS"}, {"isClaudeCodeAction", "CLAUDE_CODE_ACTION"}} {
		environment.Set(pair.key, rawBoolMain(envTruthyNative(env[pair.variable])))
	}
	environment.Set("isLocalAgentMode", rawBoolMain(env["CLAUDE_CODE_ENTRYPOINT"] == "local-agent"))
	environment.Set("isConductor", rawBoolMain(env["CONDUCTOR_WORKSPACE_ID"] != ""))
	environment.Set("isClaudeAiAuth", rawBoolMain(subscriber))
	if build := env["CLAUDE_CODE_LOCAL_BUILD_TIME"]; build != "" {
		environment.Set("buildTime", quoteMain(build))
	}
	if version := regexp.MustCompile(`^\d+\.\d+\.\d+(?:-[a-z]+)?`).FindString(m.version); version != "" {
		environment.Set("versionBase", quoteMain(version))
	}
	for _, pair := range []struct{ key, variable string }{{"remoteEnvironmentType", "CLAUDE_CODE_REMOTE_ENVIRONMENT_TYPE"}, {"claudeCodeContainerId", "CLAUDE_CODE_CONTAINER_ID"}, {"claudeCodeRemoteSessionId", "CLAUDE_CODE_REMOTE_SESSION_ID"}, {"tags", "CLAUDE_CODE_TAGS"}, {"githubEventName", "GITHUB_EVENT_NAME"}, {"githubActionsRunnerEnvironment", "RUNNER_ENVIRONMENT"}, {"githubActionsRunnerOs", "RUNNER_OS"}} {
		if env[pair.variable] != "" {
			environment.Set(pair.key, quoteMain(env[pair.variable]))
		}
	}
	core := protocol.NewJSONObject(protocol.JSONField{Name: "model", Value: prefix.model}, protocol.JSONField{Name: "sessionId", Value: quoteMain(m.commandSessionID)}, protocol.JSONField{Name: "userType", Value: quoteMain(env["USER_TYPE"])}, protocol.JSONField{Name: "isInteractive", Value: quoteMain("false")}, protocol.JSONField{Name: "clientType", Value: quoteMain("cli")}, protocol.JSONField{Name: "entrypoint", Value: quoteMain(env["CLAUDE_CODE_ENTRYPOINT"])})
	if prefix.betas != nil {
		core.Set("betas", prefix.betas)
	}
	for _, pair := range []struct{ key, variable string }{{"agentSdkVersion", "CLAUDE_AGENT_SDK_VERSION"}, {"sweBenchRunId", "SWE_BENCH_RUN_ID"}, {"sweBenchInstanceId", "SWE_BENCH_INSTANCE_ID"}, {"sweBenchTaskId", "SWE_BENCH_TASK_ID"}} {
		if env[pair.variable] != "" {
			core.Set(pair.key, quoteMain(env[pair.variable]))
		}
	}
	user := protocol.NewJSONObject()
	if subscriber {
		var account protocol.JSONObject
		_ = json.Unmarshal(global.Get("oauthAccount"), &account)
		for _, pair := range []struct{ key, source string }{{"accountUuid", "accountUuid"}, {"organizationUuid", "organizationUuid"}, {"email", "emailAddress"}} {
			user.Set(pair.key, account.Get(pair.source))
		}
	}
	if envTruthyNative(env["GITHUB_ACTIONS"]) {
		github := protocol.NewJSONObject()
		for _, pair := range []struct{ key, variable string }{{"actorId", "GITHUB_ACTOR_ID"}, {"repositoryId", "GITHUB_REPOSITORY_ID"}, {"repositoryOwnerId", "GITHUB_REPOSITORY_OWNER_ID"}} {
			if env[pair.variable] != "" {
				github.Set(pair.key, quoteMain(env[pair.variable]))
			}
		}
		raw, _ := github.MarshalJSON()
		user.Set("githubActionsMetadata", raw)
	}
	raw, _ := environment.MarshalJSON()
	core.Set("envContext", raw)
	if event.Context != nil {
		core.Assign(*event.Context)
	}
	device, err := m.Global.GetOrCreateUserID()
	if err != nil {
		return protocol.JSONObject{}, err
	}
	record, err := analytics.Record(analytics.RecordInput{Event: event, ID: uuidMain(), Timestamp: m.Native.Now(), Core: core, User: user})
	if err != nil {
		return protocol.JSONObject{}, err
	}
	var data protocol.JSONObject
	if err := json.Unmarshal(record.Get("event_data"), &data); err != nil {
		return protocol.JSONObject{}, err
	}
	data.Set("device_id", device)
	raw, err = data.MarshalJSON()
	record.Set("event_data", raw)
	return record, err
}

func (m *MainCommand) nativeCommandAnalyticsRequest(scope *cooperative.Scope, target string, body []byte, skipAuth bool) (analytics.Request, error) {
	if !scope.BelongsTo(m.Owner) {
		return analytics.Request{}, cooperative.ErrRestricted
	}
	env := m.nativeCommandEnvironment()
	headers := http.Header{}
	headers.Set("Content-Type", "application/json")
	headers.Set("User-Agent", "claude-code/"+m.version)
	headers.Set("x-service-name", "claude-code")
	if !skipAuth {
		identity, tokens, _, enabled, err := m.cliAuthIdentity(context.Background(), scope, false)
		if err != nil {
			return analytics.Request{}, err
		}
		subscriber, err := openaiauth.ClaudeSubscriber(enabled, tokens)
		if err != nil {
			return analytics.Request{}, err
		}
		if subscriber && tokens != nil {
			var scopes []string
			_ = json.Unmarshal(tokens.Get("scopes"), &scopes)
			profile := false
			for _, value := range scopes {
				profile = profile || value == "user:profile"
			}
			var expires float64
			_ = json.Unmarshal(tokens.Get("expiresAt"), &expires)
			expired := tokens.Get("expiresAt") != nil && string(tokens.Get("expiresAt")) != "null" && float64(m.Native.Now().UnixMilli()+5*60*1000) >= expires
			var token string
			_ = json.Unmarshal(tokens.Get("accessToken"), &token)
			if profile && !expired && token != "" {
				headers.Set("Authorization", "Bearer "+token)
				headers.Set("anthropic-beta", "oauth-2025-04-20")
			}
		} else if identity.APIKeySource == "ANTHROPIC_API_KEY" {
			headers.Set("x-api-key", env["ANTHROPIC_API_KEY"])
		} else if identity.APIKeySource == "/login managed key" {
			key, _, err := m.nativeCommandLegacyAPIKey(context.Background(), scope)
			if err != nil {
				return analytics.Request{}, err
			}
			if key != "" {
				headers.Set("x-api-key", key)
			}
		}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableKeepAlives = true
	selection := provider.ResolveNativeProxySelection(target, env)
	transport.Proxy = nil
	if selection.ProxyURL != "" && !selection.Bypass {
		proxy, err := url.Parse(selection.ProxyURL)
		if err != nil {
			return analytics.Request{}, err
		}
		transport.Proxy = http.ProxyURL(proxy)
	}
	if strings.HasPrefix(target, "https:") {
		if m.nativeInitTLS == nil {
			var err error
			m.nativeInitTLS, err = tlsconfig.NewState(func(path string) ([]byte, error) { return m.Native.readFile(m.Native.nativePhysicalPath(path)) })
			if err != nil {
				return analytics.Request{}, err
			}
		}
		configuration, err := m.nativeInitTLS.VerifiedConfig(env)
		if err != nil {
			return analytics.Request{}, err
		}
		transport.TLSClientConfig = configuration
	}
	return analytics.Request{URL: target, Headers: headers, Body: append([]byte(nil), body...), Timeout: 10 * time.Second, Client: &http.Client{Transport: transport}}, nil
}

func (m *MainCommand) nativeCommandAnalyticsHTTP(ctx context.Context, input analytics.Request) (analytics.Response, error) {
	if m.commandAnalyticsHTTP != nil {
		return m.commandAnalyticsHTTP(ctx, input)
	}
	ctx, cancel := context.WithTimeout(ctx, input.Timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, input.URL, bytes.NewReader(input.Body))
	if err != nil {
		return analytics.Response{}, err
	}
	request.Header = input.Headers.Clone()
	response, err := input.Client.Do(request)
	if err != nil {
		return analytics.Response{}, err
	}
	defer response.Body.Close()
	defer input.Client.CloseIdleConnections()
	body, err := io.ReadAll(response.Body)
	return analytics.Response{Status: response.StatusCode, Headers: response.Header.Clone(), Body: body}, err
}

func (m *MainCommand) closeNativeCommandSinks() {
	if m.Owner == nil {
		return
	}
	_ = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if m.commandEvents != nil {
			_ = m.commandEvents.Close(scope)
		}
		if m.commandAnalytics != nil {
			return m.commandAnalytics.Abort(scope)
		}
		return nil
	})
	if m.commandErrorSink != nil {
		m.commandErrorSink.Close()
	}
}
