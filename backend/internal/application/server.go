package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/NanmiCoder/cc-haha/backend/internal/access"
	"github.com/NanmiCoder/cc-haha/backend/internal/activitystatsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/agentsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/clilauncher"
	"github.com/NanmiCoder/cc-haha/backend/internal/computeruse"
	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/diagnosticsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/doctorapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/h5access"
	"github.com/NanmiCoder/cc-haha/backend/internal/marketapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/memoryapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/migrations"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/nativeprovidermodels"
	"github.com/NanmiCoder/cc-haha/backend/internal/nativeprovidertest"
	"github.com/NanmiCoder/cc-haha/backend/internal/opentargets"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/server"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerproxytransport"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionrewind"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/tracecapture"
	"github.com/NanmiCoder/cc-haha/backend/internal/uipreferences"
)

type ServerOptions struct {
	Environment                                              map[string]string
	HomeDir, ConfigDir, WorkDir, AppRoot, H5DistDir, Version string
	Port                                                     int
	Arguments                                                clibootstrap.PrintArguments
	Stderr                                                   io.Writer
	NativeFactory                                            NativeFactory
	// Replace only the native Keychain command in disposable transport tests.
	RunSecurity func(context.Context, securestorage.Command) ([]byte, error)
	// Overrides only the physical HTTP transport in isolated registration tests.
	FeishuRegistrationHTTPClient      *http.Client
	WecomRegistrationHTTPClient       *http.Client
	QQRegistrationHTTPClient          *http.Client
	WechatRegistrationHTTPClient      *http.Client
	WhatsAppRegistrationHTTPTransport http.RoundTripper
	ClaudeOAuthHTTPClient             *http.Client
}

// NewDesktopServer constructs real persistence and API services around the same
// application factory. No TypeScript subprocess or fixture Session is used.
func NewDesktopServer(o ServerOptions) (*server.Server, error) {
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	store := storage.New(storage.Options{})
	upgrader, err := migrations.New(migrations.Options{Store: store})
	if err != nil {
		return nil, err
	}
	env := func() map[string]string {
		snapshot := map[string]string{}
		for k, v := range o.Environment {
			snapshot[k] = v
		}
		return snapshot
	}
	shellEnvironment := newTerminalEnvironment(env())
	var nativeSettings *clibootstrap.NativeProcessSettings
	settingsOptions := settings.Options{Store: store, HomeDir: o.HomeDir, ConfigDir: o.ConfigDir, ProjectRoot: o.WorkDir, Environment: env, EnsureMigrated: upgrader.SettingsCallback, GitExecutable: "git", ShellEnvironment: shellEnvironment.Get, InvalidateCache: func() {
		if nativeSettings != nil {
			nativeSettings.InvalidateSettingsCache()
		}
	}}
	service, err := settings.New(settingsOptions)
	if err != nil {
		return nil, err
	}
	managed, err := settings.NewManaged(settingsOptions)
	if err != nil {
		return nil, err
	}
	preferences, err := uipreferences.New(uipreferences.Options{Store: store, Upgrader: upgrader, ConfigDir: func() string { return o.ConfigDir }})
	if err != nil {
		return nil, err
	}
	voiceService, err := newDesktopVoice(o, preferences, service)
	if err != nil {
		return nil, err
	}
	keepVoice := false
	defer func() {
		if !keepVoice {
			_ = voiceService.Close(context.Background())
		}
	}()

	h5, err := h5access.New(h5access.Options{Managed: managed, Environment: env, NetworkInterfaces: nativeInterfaces, ServerPort: func() int { return o.Port }})
	if err != nil {
		return nil, err
	}
	if o.Arguments.MaxTurns == 0 {
		o.Arguments.MaxTurns = int(^uint32(0) >> 1)
	}
	if o.Arguments.Sources.AllowedSettingSources == nil {
		o.Arguments.Sources = clibootstrap.DefaultState()
	}
	var stylesNative *clibootstrap.NativeBoundary
	nativeFactory := o.NativeFactory
	if o.RunSecurity != nil {
		baseFactory := nativeFactory
		nativeFactory = func(environment map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
			var native *clibootstrap.NativeBoundary
			var failure error
			if baseFactory != nil {
				native, failure = baseFactory(environment, cwd, platform)
			} else {
				native, failure = clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: environment, CWD: cwd, Platform: platform})
			}
			if failure == nil {
				native.RunSecurity = o.RunSecurity
			}
			return native, failure
		}
	}
	if nativeFactory != nil {
		stylesNative, err = nativeFactory(env(), o.WorkDir, runtime.GOOS)
	} else {
		stylesNative, err = clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env(), CWD: o.WorkDir, Platform: runtime.GOOS})
	}
	if err != nil {
		return nil, err
	}
	nativeSettings, err = clibootstrap.NewNativeProcessSettings(stylesNative, runtime.GOOS, o.Arguments.Sources, store)
	if err != nil {
		return nil, err
	}
	styles, err := nativeSettings.StyleLoader()
	if err != nil {
		_ = nativeSettings.Close(context.Background())
		return nil, err
	}
	skills, err := skillregistry.New(skillregistry.Options{
		Home: stylesNative.Home, ConfigDir: stylesNative.Environment["CLAUDE_CONFIG_DIR"],
		CWD: stylesNative.CWD, ProjectRoot: stylesNative.CWD,
		Environment: func(key string) string { return stylesNative.Environment[key] },
		Settings: func(context.Context) (protocol.JSONObject, error) {
			value, err := nativeSettings.InitialSettings()
			if value == nil {
				return protocol.NewJSONObject(), err
			}
			return value.Clone(), err
		},
		Plugins: nativeSettings,
	})
	if err != nil {
		_ = nativeSettings.Close(context.Background())
		return nil, err
	}
	slashCommands, err := newDesktopSlashCommands(stylesNative, nativeSettings, skills, o.Stderr)
	if err != nil {
		_ = nativeSettings.Close(context.Background())
		return nil, err
	}
	memory, err := memoryapi.New(memoryapi.Options{Home: stylesNative.Home, ConfigDir: stylesNative.Environment["CLAUDE_CONFIG_DIR"], CWD: stylesNative.CWD, Environment: func(key string) string { return stylesNative.Environment[key] }})
	if err != nil {
		_ = nativeSettings.Close(context.Background())
		return nil, err
	}
	openPlatform := runtime.GOOS
	if openPlatform == "windows" {
		openPlatform = "win32"
	}
	openTargets, err := opentargets.New(opentargets.Options{Home: stylesNative.Home, ConfigDir: stylesNative.Environment["CLAUDE_CONFIG_DIR"], CWD: stylesNative.CWD, Platform: openPlatform, TempDir: stylesNative.Temp, Environment: stylesNative.Environment})
	if err != nil {
		_ = nativeSettings.Close(context.Background())
		return nil, err
	}
	indexes, err := newDesktopLocalIndexes(o, stylesNative)
	if err != nil {
		_ = nativeSettings.Close(context.Background())
		return nil, err
	}
	keepIndexes := false
	defer func() {
		if !keepIndexes {
			_ = indexes.close(context.Background())
		}
	}()
	diagnostics, err := diagnosticsapi.New(diagnosticsapi.Options{HomeDir: o.HomeDir, ConfigDir: o.ConfigDir, Version: o.Version, LocalIndexStatus: func() (any, error) { return indexes.primary.Status(), nil }, RebuildLocalIndex: func(ctx context.Context) (any, error) { status, _ := indexes.primary.Rebuild(ctx); return status, nil }})
	if err != nil {
		_ = nativeSettings.Close(context.Background())
		return nil, err
	}
	trace, err := tracecapture.New(tracecapture.Options{Scope: o.ConfigDir, Store: store, Env: func(key string) string { return o.Environment[key] }, OnError: func(err error) {
		_, _ = fmt.Fprintf(o.Stderr, "[TraceCapture] %v\n", err)
	}})
	if err != nil {
		_ = nativeSettings.Close(context.Background())
		return nil, err
	}
	keepTrace := false
	defer func() {
		if !keepTrace {
			_ = trace.Close(context.Background())
		}
	}()
	activityStats, err := activitystatsapi.New(activitystatsapi.Options{HomeDir: o.HomeDir, ConfigDir: o.ConfigDir, Index: indexes})
	if err != nil {
		_ = nativeSettings.Close(context.Background())
		return nil, err
	}
	doctor, err := doctorapi.New(doctorapi.Options{HomeDir: o.HomeDir, ConfigDir: o.ConfigDir, ProjectRoot: o.WorkDir, UsesConfigDirOverride: o.Environment["CLAUDE_CONFIG_DIR"] != "", Diagnostics: diagnostics, AgentSkillsEnabled: func(ctx context.Context) bool { return len(skills.UserRoots(ctx)) > 1 }})
	if err != nil {
		_ = nativeSettings.Close(context.Background())
		return nil, err
	}
	computer, err := computeruse.New(computeruse.Options{HomeDir: o.HomeDir, ConfigDir: o.ConfigDir, WorkDir: o.WorkDir, AppRoot: o.AppRoot, Environment: env(), Store: store, Diagnostics: diagnostics})
	if err != nil {
		_ = nativeSettings.Close(context.Background())
		return nil, err
	}
	userSettings := func(ctx context.Context) (protocol.JSONObject, error) {
		value, err := service.GetUserSettings(ctx)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		data, err := json.Marshal(value)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		var result protocol.JSONObject
		err = json.Unmarshal(data, &result)
		return result, err
	}
	global, err := nativeSettings.SharedGlobalConfig()
	if err != nil {
		_ = nativeSettings.Close(context.Background())
		return nil, err
	}
	oauth, err := desktopoauth.New(desktopoauth.Options{Home: o.HomeDir, ConfigDir: o.ConfigDir, Environment: env, Store: store, Global: global, RunSecurity: o.RunSecurity, HTTPClient: o.ClaudeOAuthHTTPClient, Settings: userSettings})
	if err != nil {
		_ = nativeSettings.Close(context.Background())
		return nil, err
	}
	grokCatalog := func(ctx context.Context) ([]providerproxy.GrokModel, error) {
		return oauth.Grok().ModelCatalog(ctx, false)
	}
	openAICatalog := func(ctx context.Context) ([]providerproxy.OpenAIModel, error) {
		return oauth.OpenAI().ModelCatalog(ctx, false)
	}
	if err := nativeSettings.SetAgentMainLoopModelResolver(oauth.MainLoopModelForMetadata); err != nil {
		_ = oauth.Close()
		_ = nativeSettings.Close(context.Background())
		return nil, err
	}
	providerModels, err := nativeprovidermodels.New(nativeprovidermodels.Options{Environment: env, Settings: userSettings})
	if err != nil {
		_ = oauth.Close()
		_ = nativeSettings.Close(context.Background())
		return nil, err
	}
	providerTest, err := nativeprovidertest.New(nativeprovidertest.Options{Environment: env, Settings: userSettings, Version: o.Version, Provider: func(ctx context.Context, id string) (protocol.JSONObject, error) {
		providers, err := providerconfig.New(providerconfig.Options{Store: store, ConfigDir: o.ConfigDir, Settings: service, Managed: managed})
		if err != nil {
			return protocol.JSONObject{}, err
		}
		value, err := providers.Provider(ctx, id)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		data, err := json.Marshal(value)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		var result protocol.JSONObject
		err = json.Unmarshal(data, &result)
		return result, err
	}})
	if err != nil {
		_ = oauth.Close()
		_ = nativeSettings.Close(context.Background())
		return nil, err
	}
	recordedCommands, err := sessionrewind.NewRecordedCommandClassifier(runtime.GOOS, o.Environment["USER_TYPE"])
	if err != nil {
		_ = nativeSettings.Close(context.Background())
		return nil, err
	}
	base := Options{DesktopOAuthToken: oauth.EnsureFreshAccessToken, DesktopTeamsEnabled: service.GetAgentTeamsEnabled, SDKRecoverOfficialOAuth: func(ctx context.Context, rejected string) (*string, error) {
		doc, err := oauth.RecoverFromUnauthorized(ctx, rejected)
		if err != nil || doc == nil {
			return nil, err
		}
		token := doc.Record().String("accessToken")
		return &token, nil
	}, Trace: trace, ComputerUse: &clibootstrap.NativeComputerUseOptions{Service: computer, AppRoot: o.AppRoot}, Arguments: o.Arguments, Environment: env(), WorkDir: o.WorkDir, Version: o.Version, Platform: runtime.GOOS, Stderr: o.Stderr, Store: store, NativeFactory: nativeFactory, ProviderOptions: &providerconfig.Options{Store: store, ConfigDir: o.ConfigDir, Settings: service, Managed: managed, OpenAIModelCatalog: openAICatalog, GrokModelCatalog: grokCatalog, GrokRuntimeModelCatalog: oauth.Grok().RuntimeModelCatalog, ClaudeRuntimeModel: oauth.ResolveRuntimeModel, ServerPort: o.Port, ProxyReady: true}}
	adapters, err := newDesktopAdapters(o, store)
	if err != nil {
		_ = oauth.Close()
		_ = nativeSettings.Close(context.Background())
		return nil, err
	}
	registration, err := newDesktopIMRegistration(o, adapters)
	if err != nil {
		_ = oauth.Close()
		_ = nativeSettings.Close(context.Background())
		return nil, err
	}
	feishuRegistration, err := newDesktopFeishuRegistration(o)
	if err != nil {
		_ = oauth.Close()
		_ = nativeSettings.Close(context.Background())
		return nil, err
	}
	wecomRegistration, err := newDesktopWecomRegistration(o)
	if err != nil {
		_ = oauth.Close()
		_ = nativeSettings.Close(context.Background())
		return nil, err
	}

	qqRegistration, err := newDesktopQQRegistration(o.QQRegistrationHTTPClient, o.Environment)
	if err != nil {
		_ = oauth.Close()
		_ = nativeSettings.Close(context.Background())
		return nil, err
	}
	keepQQ := false
	defer func() {
		if !keepQQ {
			_ = qqRegistration.Close(context.Background())
		}
	}()

	wechatRegistration, err := newDesktopWechatRegistration(o.WechatRegistrationHTTPClient, o.Environment)
	if err != nil {
		_ = oauth.Close()
		_ = nativeSettings.Close(context.Background())
		return nil, err
	}
	keepWechat := false
	defer func() {
		if !keepWechat {
			_ = wechatRegistration.Close(context.Background())
		}
	}()

	var s *server.Server
	autoProviders, err := providerconfig.New(providerconfig.Options{Store: store, ConfigDir: o.ConfigDir, Settings: service, Managed: managed, ServerPort: o.Port, ProxyReady: true})
	if err != nil {
		return nil, err
	}
	proxyTLS, err := tlsconfig.NewState(os.ReadFile)
	if err != nil {
		return nil, err
	}
	providerProxy, err := providerproxytransport.New(providerproxytransport.Options{Provider: autoProviders.ProviderForProxy, Settings: userSettings, Environment: env, Version: o.Version, Trace: trace, TLS: proxyTLS})
	if err != nil {
		return nil, err
	}
	autoQuestions, err := newDesktopAutoQuestion(o, service, autoProviders, oauth, providerProxy)
	if err != nil {
		return nil, err
	}
	defer func() {
		if !keepVoice {
			_ = autoQuestions.Close(context.Background())
		}
	}()
	base.AutoQuestion = autoQuestions
	base.AutoQuestionContext = func(ctx context.Context, id, agent string) (json.RawMessage, error) {
		return s.ReadAutoQuestionContext(ctx, id, agent)
	}
	base.AutoQuestionProvider = func(ctx context.Context, id string) (json.RawMessage, error) {
		value, err := autoProviders.Provider(ctx, id)
		return raw(value), err
	}
	connectorRuntime := &desktopConnectorRuntime{native: stylesNative, settings: nativeSettings, editable: service, security: o.RunSecurity, version: o.Version, platform: openPlatform, reload: func(ctx context.Context, id string, required *protocol.JSONObject) error {
		return s.ReloadConnectorSessions(ctx, id, required)
	}}
	mcpRuntime := &desktopMCPRuntime{environment: env(), cwd: o.WorkDir, version: o.Version, factory: nativeFactory, settings: nativeSettings, shell: shellEnvironment, syncToggle: func(ctx context.Context, session, name string, config protocol.JSONObject, enabled bool) (protocol.JSONObject, error) {
		return s.SyncMCPToggle(ctx, session, name, config, enabled)
	}}
	agentRuntime := &desktopAgentRuntime{environment: env(), cwd: o.WorkDir, factory: nativeFactory, settings: nativeSettings}
	agents, err := agentsapi.New(agentsapi.Options{HomeDir: o.HomeDir, ConfigDir: o.ConfigDir, WorkDir: o.WorkDir, Load: agentRuntime.load, AvailableTools: agentRuntime.tools, Locked: agentRuntime.locked, Invalidate: agentRuntime.invalidate, UpdateOverride: service.UpdateBuiltInAgentOverride, Reload: func(ctx context.Context, id string) (protocol.JSONObject, error) {
		return s.ReloadSessionComponents(ctx, id)
	}})
	if err != nil {
		_ = computer.Close(context.Background())
		_ = oauth.Close()
		_ = nativeSettings.Close(context.Background())
		return nil, err
	}
	pluginRuntime := &desktopPluginRuntime{cwd: o.WorkDir, mcp: mcpRuntime, agents: agentRuntime, settings: service, reload: func(ctx context.Context, id string) (protocol.JSONObject, error) {
		return s.ReloadSessionComponents(ctx, id)
	}}
	cron, err := newDesktopScheduler(o, base, func() *server.Server { return s }, shellEnvironment, service, managed, store, oauth, diagnostics)
	if err != nil {
		pluginRuntime.close()
		_ = agentRuntime.close(context.Background())
		_ = mcpRuntime.close(context.Background())
		_ = connectorRuntime.close(context.Background())
		_ = computer.Close(context.Background())
		_ = oauth.Close()
		_ = nativeSettings.Close(context.Background())
		return nil, err
	}
	workflows, closeWorkflows, err := newDesktopWorkflows(o, nativeSettings, indexes.budget, func(ctx context.Context, id string) ([]protocol.JSONObject, error) {
		return s.WorkflowTaskNotifications(ctx, id)
	})
	if err != nil {
		_ = cron.Stop(context.Background())
		pluginRuntime.close()
		_ = agentRuntime.close(context.Background())
		_ = mcpRuntime.close(context.Background())
		_ = connectorRuntime.close(context.Background())
		_ = computer.Close(context.Background())
		_ = oauth.Close()
		_ = nativeSettings.Close(context.Background())
		return nil, err
	}
	keepWorkflows := false
	defer func() {
		if !keepWorkflows {
			_ = closeWorkflows(context.Background())
		}
	}()
	executable, _ := os.Executable()
	inspectionModelOptions := &modelaccounting.NativeCalculatorOptions{Environment: stylesNative.Environment, Settings: nativeSettings.InitialSettings, Global: global.Get}
	cliLauncher, err := clilauncher.New(clilauncher.Options{HomeDir: o.HomeDir, Executable: executable, Platform: runtime.GOOS, Environment: env})
	if err != nil {
		return nil, err
	}
	mentions, err := newDesktopCapabilityMentions(stylesNative, nativeSettings, mcpRuntime, autoProviders)
	if err != nil {
		return nil, err
	}
	whatsappRegistration, err := newDesktopWhatsAppRegistration(o)
	if err != nil {
		return nil, err
	}
	keepWhatsApp := false
	defer func() {
		if !keepWhatsApp {
			_ = whatsappRegistration.Close(context.Background())
		}
	}()
	market, err := marketapi.New(marketapi.Options{ConfigDir: o.ConfigDir, Environment: env, Settings: userSettings, Changed: nativeSettings.InvalidateSettingsCache})
	if err != nil {
		return nil, err
	}
	keepMarket := false
	defer func() {
		if !keepMarket {
			_ = market.Close(context.Background())
		}
	}()
	s, err = server.New(server.Options{Market: market, WhatsAppRegistration: whatsappRegistration, Adapters: adapters, IMRegistration: registration, FeishuRegistration: feishuRegistration, WecomRegistration: wecomRegistration, QQRegistration: qqRegistration, WechatRegistration: wechatRegistration, InspectionModelOptions: inspectionModelOptions, AutoQuestion: autoQuestions, Voice: voiceService, CLILauncher: cliLauncher, Workflows: workflows, WorkflowAgentRunState: workflows.GetAgentRunState, Trace: trace, Scheduler: cron, Search: indexes.search, HistoryBudget: indexes.budget, LocalIndex: indexes.primary, Plugins: pluginRuntime.get, Agents: agents, ActivityStats: activityStats, HahaOAuth: oauth, HahaOpenAIOAuth: oauth.OpenAI(), HahaGrokOAuth: oauth.Grok(), ProviderModels: providerModels, ProviderTest: providerTest, ProviderProxy: providerProxy, ProviderProxyPort: o.Port, ComputerUse: computer, Doctor: doctor, Diagnostics: diagnostics, MCP: mcpRuntime.get, Connectors: connectorRuntime.get, OpenTargets: openTargets, Memory: memory, Skills: skills, SkillMentions: mentions, SkillSlashCommands: slashCommands.List, PersistSession: nativeSettings.PersistSession, ClassifyRecordedCommand: recordedCommands.Classify, OnShutdown: func(ctx context.Context) error {
		pluginRuntime.close()
		return errors.Join(market.Close(ctx), autoQuestions.Close(ctx), voiceService.Close(ctx), cliLauncher.Close(ctx), cron.Stop(context.WithoutCancel(ctx)), closeWorkflows(ctx), trace.Close(context.WithoutCancel(ctx)), indexes.close(ctx), agentRuntime.close(ctx), mcpRuntime.close(ctx), connectorRuntime.close(ctx), computer.Close(ctx), oauth.Close(), nativeSettings.Close(ctx))
	}, RespectGitignore: nativeSettings.RespectGitignore, OutputStyles: styles, HomeDir: o.HomeDir, ConfigDir: o.ConfigDir, AppRoot: o.AppRoot, WorkDir: o.WorkDir, H5DistDir: o.H5DistDir, Version: o.Version, Environment: env, Store: store, Settings: service, Managed: managed, Preferences: preferences, H5: h5, TrustedRendererOrigin: o.Environment["CC_HAHA_TRUSTED_RENDERER_ORIGIN"], Authorization: access.Authorization{APIKey: o.Environment["ANTHROPIC_API_KEY"], LocalToken: o.Environment[access.LocalAccessTokenEnv], PetToken: o.Environment[access.PetAccessTokenEnv]}, Factory: DesktopFactory(base)})
	if err != nil {
		_ = cron.Stop(context.Background())
		_ = indexes.close(context.Background())
		_ = nativeSettings.Close(context.Background())
	} else {
		if err = newDesktopTeams(o, s, stylesNative, autoProviders); err != nil {
			_ = s.Close(context.Background())
			return nil, err
		}
		indexes.start()
		if err = cron.Start(context.Background()); err != nil {
			_ = s.Close(context.Background())
			return nil, err
		}
		keepMarket = true
		keepWhatsApp = true
		keepQQ = true
		keepWechat = true
		keepIndexes = true
		keepTrace = true
		keepWorkflows = true
		keepVoice = true
		cliLauncher.Start()
	}
	return s, err
}

func nativeInterfaces() []h5access.NetworkInterface {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	out := []h5access.NetworkInterface{}
	for _, network := range interfaces {
		addresses, e := network.Addrs()
		if e != nil {
			continue
		}
		entry := h5access.NetworkInterface{Name: network.Name, Addresses: []h5access.InterfaceAddress{}}
		for _, address := range addresses {
			ip, _, e := net.ParseCIDR(address.String())
			if e != nil {
				continue
			}
			family := "IPv6"
			if ip.To4() != nil {
				family = "IPv4"
			}
			entry.Addresses = append(entry.Addresses, h5access.InterfaceAddress{Address: ip.String(), Family: family, Internal: ip.IsLoopback()})
		}
		out = append(out, entry)
	}
	return out
}

func RunServer(ctx context.Context, args []string, environment map[string]string, cwd, version string, stderr io.Writer, nativeFactory NativeFactory) error {
	o := ServerOptions{Environment: environment, WorkDir: cwd, Version: version, Port: 3000, Stderr: stderr, NativeFactory: nativeFactory}
	security, err := SmokeSecurity(environment)
	if err != nil {
		return err
	}
	o.RunSecurity = security
	host := "127.0.0.1"
	for i := 0; i < len(args); i++ {
		if i+1 >= len(args) {
			return errors.New("server option requires a value: " + args[i])
		}
		key, value := args[i], args[i+1]
		i++
		switch key {
		case "--host":
			host = value
		case "--port":
			port, err := strconv.Atoi(value)
			if err != nil || port < 0 || port > 65535 {
				return errors.New("invalid server port")
			}
			o.Port = port
		case "--app-root":
			o.AppRoot = value
		case "--h5-dist":
			o.H5DistDir = value
		case "--system-prompt":
			o.Arguments.SystemPrompt = value
			o.Arguments.HasCustomSystemPrompt = true
		case "--model":
			o.Arguments.Model = value
		case "--tools":
			o.Arguments.Tools = []string{value}
		case "--permission-mode":
			o.Arguments.PermissionMode = value
		case "--thinking":
			switch value {
			case "disabled":
				o.Arguments.Thinking = requestcontext.Thinking{Type: "disabled"}
			case "adaptive", "enabled":
				o.Arguments.Thinking = requestcontext.Thinking{Type: "adaptive"}
			default:
				return errors.New("invalid thinking mode")
			}
		default:
			return errors.New("unknown server option: " + key)
		}
	}
	o.HomeDir = environment["HOME"]
	if runtime.GOOS == "windows" && environment["USERPROFILE"] != "" {
		o.HomeDir = environment["USERPROFILE"]
	}
	if o.HomeDir == "" {
		return errors.New("server requires a home directory")
	}
	o.ConfigDir = environment["CLAUDE_CONFIG_DIR"]
	if o.ConfigDir == "" {
		o.ConfigDir = filepath.Join(o.HomeDir, ".claude")
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(o.Port)))
	if err != nil {
		return err
	}
	defer listener.Close()
	o.Port = listener.Addr().(*net.TCPAddr).Port
	s, err := NewDesktopServer(o)
	if err != nil {
		return err
	}
	return s.Serve(ctx, listener)
}
