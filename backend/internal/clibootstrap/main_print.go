package clibootstrap

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/analytics"
	"github.com/NanmiCoder/cc-haha/backend/internal/authhelper"
	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliargs"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/cloudauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/diagnostics"
	"github.com/NanmiCoder/cc-haha/backend/internal/environmentwatch"
	"github.com/NanmiCoder/cc-haha/backend/internal/errorlog"
	"github.com/NanmiCoder/cc-haha/backend/internal/filehistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/gitbranch"
	"github.com/NanmiCoder/cc-haha/backend/internal/grokauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/imagegeneration"
	"github.com/NanmiCoder/cc-haha/backend/internal/installationdiagnostics"
	"github.com/NanmiCoder/cc-haha/backend/internal/lspruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcptools"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/pdfread"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginoptions"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/querypreflight"
	"github.com/NanmiCoder/cc-haha/backend/internal/remoteio"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/resumestate"
	"github.com/NanmiCoder/cc-haha/backend/internal/runtimecleanup"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionactivity"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionenvironment"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionplans"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionresume"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/settingswatch"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/systemprompt"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamworker"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolsearch"
	"github.com/NanmiCoder/cc-haha/backend/internal/tracecapture"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/webfetch"
	"github.com/NanmiCoder/cc-haha/backend/internal/websearch"
	"github.com/NanmiCoder/cc-haha/backend/internal/windowshell"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflow"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
)

type PrintArguments struct {
	IDE                                            bool
	FromPR                                         json.RawMessage
	Prefill                                        string
	RewindFiles                                    string
	Init, InitOnly, Maintenance                    bool
	MaxBudgetUSD, TaskBudget                       *float64
	FallbackModel, Workload                        string
	EnableAuthStatus                               bool
	SDKURL                                         string
	Remote                                         *string
	Teleport                                       string
	TeleportWithoutValue                           bool
	FileSpecs                                      []string
	DeepLinkOrigin                                 bool
	DeepLinkRepo                                   string
	DeepLinkLastFetch                              *float64
	Continue                                       bool
	SystemPromptFile, AppendSystemPromptFile, Name string
	EnableAutoMode                                 bool
	Agent                                          string
	AgentsJSON                                     string
	Betas                                          []string
	MCPConfigs                                     []string
	StrictMCPConfig                                bool
	DisableSlashCommands                           bool
	PermissionPromptToolName                       string
	ResumeWithoutValue                             bool
	// Source debug.ts reads raw argv, independent of Commander boolean coercion.
	DebugArguments                         []string
	DebugFormatted                         bool
	IncludeHookEvents                      bool
	JSONSchema                             string
	TeammateMode                           string
	PluginDirectories                      []string
	Chrome                                 *bool
	Worktree                               *worktree.Startup
	ComputerUse                            *bool
	Model, SystemPrompt, SessionID, Resume string
	HasCustomSystemPrompt                  bool
	Tools, AllowedTools, DisallowedTools   []string
	MaxTurns                               int
	Replay                                 bool
	ForkSession                            bool
	ResumeAt, AppendSystemPrompt           string
	SessionPersistenceDisabled             bool
	Sources                                State
	PermissionMode                         string
	DangerouslySkipPermissions             bool
	AllowDangerouslySkipPermissions        bool
	AdditionalDirectories                  []string
	EffortLevel                            string
	Thinking                               requestcontext.Thinking
	MaxThinkingTokens                      *float64
}

func ParsePrintArguments(args []string, version string) (PrintArguments, error) {
	p := cliargs.Parse(args, cliargs.Config{Version: version})
	if p.Parsed == nil {
		return PrintArguments{}, mainCap("CLI argument parsing before public print handler: " + p.Stderr)
	}
	if len(p.Parsed.Args) > 0 {
		return PrintArguments{}, mainCap("normal print positional command producer")
	}
	o := p.Parsed.Options
	if p.Parsed.Path != "claude" && p.Parsed.Path != "" {
		return PrintArguments{}, mainCap("CLI subcommand handler " + p.Parsed.Path)
	}
	// The Commander adapter handles syntax and coercion first. This finite normal
	// handler refuses every public option whose business producer is not migrated.
	admitted := map[string]bool{"--print": true, "-p": true, "--verbose": true, "--input-format": true, "--output-format": true, "--include-partial-messages": true, "--replay-user-messages": true, "--tools": true, "--allowedTools": true, "--allowed-tools": true, "--dangerously-skip-permissions": true, "--model": true, "--thinking": true, "--system-prompt": true, "--max-turns": true, "--setting-sources": true, "--session-id": true, "--resume": true, "-r": true}
	admitted["--include-hook-events"] = true
	admitted["--json-schema"] = true
	for _, key := range []string{"--debug", "-d", "-d2e", "--debug-file", "--debug-to-stderr"} {
		admitted[key] = true
	}
	for _, s := range args {
		if len(s) > 0 && s[0] == '-' && !admitted[s] && !strings.HasPrefix(s, "--debug=") && !strings.HasPrefix(s, "--debug-file=") {
			return PrintArguments{}, mainCap("normal print option business " + s)
		}
	}
	str := func(k string) string { v, _ := o[k].(string); return v }
	b := func(k string) bool { v, _ := o[k].(bool); return v }
	if !b("print") || !b("verbose") || str("inputFormat") != "stream-json" || str("outputFormat") != "stream-json" || !b("includePartialMessages") || !b("replayUserMessages") || !b("dangerouslySkipPermissions") || str("thinking") != "disabled" || str("systemPrompt") == "" {
		return PrintArguments{}, mainCap("normal print formats/permission/thinking/system policy")
	}
	list := func(k string) []string {
		out := []string{}
		switch v := o[k].(type) {
		case []any:
			for _, x := range v {
				s, ok := x.(string)
				if !ok {
					return nil
				}
				out = append(out, s)
			}
		case []string:
			out = append(out, v...)
		}
		return out
	}
	tools, allowed := list("tools"), list("allowedTools")
	if len(tools) != 1 || (tools[0] != "Write" && tools[0] != "") || len(allowed) != 1 || allowed[0] != "Write" {
		return PrintArguments{}, mainCap("normal print public tool selection")
	}
	turns, _ := o["maxTurns"].(float64)
	if turns < 1 || turns > math.MaxInt32 || math.Trunc(turns) != turns {
		return PrintArguments{}, mainCap("public max-turns policy")
	}
	sid, resume := str("sessionId"), str("resume")
	if sid != "" && resume != "" {
		return PrintArguments{}, mainCap("simultaneous public session-id/resume")
	}
	if sid == "" && resume == "" {
		return PrintArguments{}, mainCap("public fresh randomly selected SID profile")
	}
	sources := Prepare(args, Options{})
	if sources.ExitCode != 0 || sources.State.FlagSettingsPath != nil {
		return PrintArguments{}, mainCap("public settings pre-action profile")
	}
	return PrintArguments{DebugArguments: append([]string{}, args...), DebugFormatted: true, IncludeHookEvents: b("includeHookEvents"), JSONSchema: str("jsonSchema"), Model: str("model"), SystemPrompt: str("systemPrompt"), SessionID: sid, Resume: resume, ResumeWithoutValue: b("resume"), Tools: tools, AllowedTools: allowed, MaxTurns: int(turns), Replay: true, Sources: sources.State}, nil
}
func quoteMain(s string) json.RawMessage { b, _ := json.Marshal(s); return b }
func uuidMain() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return hex.EncodeToString(b[:4]) + "-" + hex.EncodeToString(b[4:6]) + "-" + hex.EncodeToString(b[6:8]) + "-" + hex.EncodeToString(b[8:10]) + "-" + hex.EncodeToString(b[10:])
}

type MainPrintOptions struct {
	EditTerminalFile func(context.Context, string, string, string, map[string]string) (string, error)
	// These optional primitives and observers retain the ordinary NativeMainPrint
	// graph in disposable cross-runtime fixtures. Nil uses the production UUID,
	// runtime, and unobserved Controller/SDK boundaries.
	UUIDPrimitive         func() string
	SDKRuntime            *provider.SourceRuntime
	OnControllerPhase     func(string)
	OnSDKPhase            func(string)
	OnSDKUUIDLookup       func(json.RawMessage, bool)
	OnSDKRecord           func()
	ObserveSDKRequest     func(string, http.Header, json.RawMessage)
	ObserveSDKFetch       func(tracecapture.FetchInput)
	ObserveSDKNativeFetch func(provider.OwnerNativeEvent)
	WrapSDKASK            func(*sdkhost.OwnerIterator[[]byte]) (*sdkhost.OwnerIterator[[]byte], error)

	thinkingSummaryState              *nativeThinkingSummaryState
	readCountProfiles                 *nativeBedrockProfileState
	CloudAuthStatus                   *cloudauth.AuthStatusManager
	SessionIngressAuth                *remoteio.IngressAuth
	NativeInProcessAutoContinueDelays []time.Duration
	OrdinarySubagents                 *SubagentConcurrency
	StartSandboxNetworkPermission     func(context.Context, *cooperative.Scope, queryloop.RegisteredTool, *protocol.JSONObject, queryloop.JSONToolCallContext) (*ownerasync.OwnerFuture[bool], error)
	NativeDesktopNetworkBound         bool
	PublicPrintLoadErrors             bool
	TeamPaneSetup                     func(context.Context, *cooperative.Scope, teamspawn.PaneSetupPrompt) (string, error)
	Interactive                       bool
	KeybindingCustomizationEnabled    func() bool
	OnLeaderInboxReady                func() error
	OnInteractiveTeamTurn             func(bool) error
	OnEnvironmentHookMessage          func(string, bool)
	OnLocalInputMessages              func([]protocol.JSONObject) error
	OpenTerminalMessageSelector       func(tui.MessageSelectorSnapshot) error
	NativeModelFastConfig             *desktopoauth.Config
	OnNativeFastModeNotification      func(protocol.JSONObject) error
	HookEvents                        bool
	TeamWorker                        *teamworker.Start
	PaneTeammate                      *teamspawn.PaneIdentity
	RequestTeammateShutdown           func(context.Context) error
	ForegroundShellProgress           bool
	DetachedAgent                     *NativeDetachedAgentOptions
	Trace                             *tracecapture.Service
	ComputerUse                       *NativeComputerUseOptions
	OnComputerUseNotification         func(context.Context, string, string)
	OnCommandsChanged                 func([]sdkhost.ControlCommand) error
	OnTerminalEditorModeChanged       func(string) error
	OnTerminalSpinner                 func(tui.TerminalSpinnerProgress) error
	ObserveModelFailure               func(error)
	OnPermissionModeChanged           func(context.Context, string) error
	AgentID                           string
	OnQueryYield                      func(*cooperative.Scope, *queryloop.Yield) error
	OnCanonicalTranscriptMutation     func(string) error
	QuerySource                       string
	AgentContext                      *modelaccounting.AgentContextBinding
	OnlyToolNames                     *[]string
	AgentEffort                       json.RawMessage
	AgentEffortOverridesEnv           bool
	Arguments                         PrintArguments
	Native                            *NativeBoundary
	NativeShells                      *windowshell.Resolver
	PID                               int
	Version, Platform                 string
	Input                             io.Reader
	Output                            io.Writer
	Error                             io.Writer
	OnOutput                          func([]byte) error
	ResolvePermission                 func(context.Context, *cooperative.Scope, queryloop.RegisteredTool, *protocol.JSONObject, queryloop.JSONToolCallContext) (queryloop.ToolPermission, error)
	AnalyticsHTTP                     func(context.Context, analytics.Request) (analytics.Response, error) // isolated HTTP primitive in maintainer evidence
}
type MainPrint struct {
	remoteManaged                              *nativeRemoteManagedPolicy
	removeRemoteManagedCacheListener           func()
	nativeSDKAgentSummaryEnabled               *atomic.Bool
	nativeCommandSourcesRuntime                nativeCommandSourcesRuntime
	nativeQueryCatalog                         nativeQueryCatalog
	nativeSDKSettings                          nativeSDKConfigSettings
	nativeRemoteAgentLife                      nativeRemoteAgentLifetime
	nativeTerminalStatusLine                   nativeTerminalStatusLineRuntime
	nativeExternalEditor                       *string
	nativeTerminalSetupTerminal                string
	nativeTerminalSetupPresentation            nativeTerminalSetupPresentation
	nativeTerminalTmuxControlMode              *bool
	nativeTerminalTaskList                     nativeTerminalTaskListRuntime
	chromeInstallation                         *nativeChromeInstallation
	chromeCacheScan                            *nativeChromeInstallation
	startupFiles                               *nativeStartupFiles
	doctorPackages                             *installationdiagnostics.Packages
	remoteIO                                   *remoteio.Service
	nativeIngress                              *remoteio.Ingress
	nativeIngressLife                          nativeRemoteLifetime
	nativeAPIPreconnect                        nativeAPIPreconnectState
	nativeResumeParsedSID                      string
	nativeResumeIsURL                          bool
	nativeUltraplan, nativeModeChangeUltraplan bool
	nativeSDKBetas                             []string
	nativeDynamicMCP                           protocol.JSONObject
	ideIntegration                             *nativeIDERuntime
	idePicker                                  *nativeIDEPending
	ideOnboarding                              *nativeIDEPending
	ideUI                                      *nativeIDEUI
	nativeAutoModeFlag                         bool
	nativeAutoConsent                          *NativeAutoConsent
	commandBinding                             *ownerasync.NativeBinding
	sandbox                                    *nativeSandboxRuntime
	fileAnalytics                              *analytics.Service
	analyticsParent                            *MainPrint
	nativeMigrations                           *nativeMigrationRuntime
	nativeMigrationEvents                      *analytics.PublicFacade
	insightsHistory                            *sessionhistory.Loader
	teamWorker                                 *nativeTeamWorkerState
	teamState                                  *nativeTeamState
	nativeTerminalTeamFooter                   *nativeTerminalTeamFooter
	nativeTerminalIDEDiffs                     nativeTerminalIDEDiffRuntime
	teamLeader                                 *nativeTeamLeaderState
	nativeInProcess                            *nativeInProcessResumeState
	fileHistory                                *filehistory.Service
	options                                    MainPrintOptions
	processStartMillis                         float64
	nativeModelSelector                        *modelselection.Selector
	nativePermissionStartup                    *sdkpermissions.NativeStartup
	nativePermissionInitial                    sdkpermissions.NativeStartupResult
	webFetch                                   *webfetch.Service
	userImages                                 imagegeneration.UserImages
	grokImages                                 *grokauth.Service
	nativeImageHTTP                            *cooperative.NativeImageHTTP
	webSearch                                  *websearch.Service
	modelPicker                                *nativeModelPickerState
	nativeTerminalUpdate                       *nativeTerminalUpdateState
	nativeTerminalMCPAuthLife                  nativeTerminalMCPAuthLifetime
	nativeRemoteBackend                        *nativeRemoteBackendState
	nativeTerminalPRIsGit                      *bool
	ultracode                                  *nativeUltracodeState
	projectConfigKeys                          map[string]string
	Owner                                      *cooperative.Owner
	Global                                     *cliglobalconfig.Service
	Startup                                    *clistartupenv.Service
	Controller                                 *sessioncontroller.Controller
	Host                                       *sdkhost.Host
	Registry                                   *sdkhost.PrintRegistry
	Cleanup                                    *runtimecleanup.Registry
	Worker                                     *sessionstate.State
	nativeSDKStateEvents                       []protocol.JSONObject
	Activity                                   *sessionactivity.Tracker
	Git                                        *gitbranch.Service
	watch                                      *gitbranch.NativePollWatch
	settings                                   *clisettings.Service
	settingState                               *clisettings.State
	environment                                *clistartupenv.Environment
	factory                                    *messages.Factory
	state                                      *bootstrapstate.State
	hook                                       *queryhooks.Service
	hookEnvironment                            *sessionenvironment.Service
	asyncHooks                                 *queryhooks.AsyncRegistry
	asyncHooksBorrowed                         bool
	environmentWatcher                         *environmentwatch.Watcher
	settingsWatcher                            *settingswatch.Watcher
	standaloneProxy                            *nativeStandaloneProxy
	tls                                        *tlsconfig.State
	openAIAuth                                 *NativeOpenAIAuth
	apiKeyHelper                               *authhelper.Cache
	apiKeyHelperBorrowed                       bool
	awsCredentials                             *authhelper.CloudCache
	awsSSOState                                *cloudauth.SSOState
	awsINIState                                *cloudauth.AWSFileCache
	gcpCredentials                             *authhelper.CloudCache
	cloudCredentialsBorrowed                   bool
	diagnostics                                *diagnostics.Process
	errorLog                                   *errorlog.Service
	errorLogSink                               *errorlog.NativeSink
	closed                                     bool
	outputMu                                   sync.Mutex
	outputs                                    [][]byte
	wake                                       chan struct{}
	finish                                     chan struct{}
	outputDone                                 chan struct{}
	outputError                                error
	shuttingDown                               bool
	sessionEndRan                              bool
	initialized                                bool
	modelCommandRuntime                        *nativeModelCommandRuntime
	prompt                                     *systemprompt.Service
	styles                                     *systemprompt.StyleLoader
	userContext                                *usercontext.Service
	systemContext                              *ownerasync.OwnerFuture[protocol.JSONObject]
	nativePromptLife                           nativePromptRuntime
	nativeTokenCountLife                       *nativeTokenCountLifetime
	autoDream                                  *nativeAutoDreamRuntime
	memoryPaths                                *usercontext.MemoryPaths
	mainThreadAgent                            *nativeMainThreadAgentState
	agentChild                                 bool
	nativeAgentPresetLife                      nativeAgentPresetLife
	nativeLSP                                  *lspruntime.Manager
	nativeLSPLife                              nativeLSPLifetime
	detachedConstructed                        bool
	agents                                     *agentcatalog.Service
	agentHosts                                 []*sdkhost.Host
	taskStore                                  *taskstore.Service
	nativeSettings                             *NativeProcessSettings
	toolSearch                                 *toolsearch.Service
	skillCommands                              *skillcommands.Service
	workflowDefinitions                        *workflow.Service
	nativeSynthetic                            *sdkhost.NativeSyntheticOutput
	nativeJSONSchema                           *sdkhost.NativeJSONSchema
	secure                                     *securestorage.Store
	pluginOptions                              *pluginoptions.Service
	nativePluginInstallation                   *nativePluginInstallation
	mcpConfigurations                          *mcpconfig.Service
	claudeAIMCP                                *CLIClaudeAIMCP
	claudeAIMCPBorrowed                        bool
	sdkMCPState                                *sdkhost.NativeSDKMCPState
	sdkMCPControlTail                          *sdkhost.OwnerFuture[struct{}]
	mcpRuntime                                 *mcpruntime.Service
	mcpBorrowed                                bool
	mcpTools                                   *mcptools.Service
	computerUse                                *nativeComputerUse
	computerUseSkillGate                       nativeComputerUseSkillGate
	skillWatcher                               *nativeSkillWatcher
	plans                                      *sessionplans.Service
	nativeTools                                *builtintools.Service
	nativeHints                                *nativeHintRuntime
	nativeFileCache                            *builtinwrite.FileStateCache
	nativePDFReader                            *pdfread.Reader
	nativePDFLife                              nativePDFLifetime
	nativeReadCountLife                        nativePDFLifetime
	worktreeState                              *nativeWorktreeState
	dynamicSkillDirTriggers                    []string
	skillListing                               *nativeSkillListingState
	nativeLocalCommands                        *nativeLocalCommandState
	terminalExportStartup                      *nativeTerminalExportStartup
	nativeGoals                                *nativeGoalRuntime
	ownsTrace                                  bool
	taskOutputDirectory                        string
}

// NewMainPrint constructs the actual one-owner production graph. The only
// fixture substitution permitted here is NativeBoundary's external primitives.
func NewMainPrint(o MainPrintOptions) (*MainPrint, error) {
	n := o.Native
	if n == nil || (!n.ExternalAbsent && !n.NativeProcess) || o.PID <= 0 || o.Version == "" || o.Input == nil || o.Output == nil || o.Error == nil {
		return nil, mainCap("resolved public native process bindings")
	}
	if !n.NativeProcess && o.Platform != "darwin" {
		return nil, mainCap("public normal startup native platform")
	}
	if o.TeamWorker != nil {
		start := *o.TeamWorker
		start.AgentDefinition = start.AgentDefinition.Clone()
		if start.Tools != nil {
			tools := append([]string{}, (*start.Tools)...)
			start.Tools = &tools
		}
		o.TeamWorker = &start
		n.Environment = teamworker.Environment(n.Environment, start)
	}
	if e := n.TransportOrigin(); e != nil {
		return nil, e
	}
	var startupError error
	o, startupError = resolveNativeCLIStartupPromptFiles(o)
	if startupError != nil {
		return nil, startupError
	}
	if existing := n.Environment["CLAUDE_CODE_ENTRYPOINT"]; existing != "" && existing != "sdk-cli" && !n.NativeProcess {
		return nil, mainCap("existing runtime entrypoint " + existing)
	}
	// Source initializeEntrypoint preserves an existing nonempty value.
	if n.Environment["CLAUDE_CODE_ENTRYPOINT"] == "" {
		n.Environment["CLAUDE_CODE_ENTRYPOINT"] = "sdk-cli"
	}
	n.Environment["COREPACK_ENABLE_AUTO_PIN"] = "0"
	n.Environment["NoDefaultCurrentDirectoryInExePath"] = "1"
	processStartMillis := n.NowMillis()
	if o.OrdinarySubagents == nil {
		o.OrdinarySubagents = &SubagentConcurrency{}
	}
	if o.readCountProfiles == nil {
		o.readCountProfiles = &nativeBedrockProfileState{values: map[string]*nativeBedrockProfileLookup{}}
	}
	if o.thinkingSummaryState == nil {
		o.thinkingSummaryState = &nativeThinkingSummaryState{interactive: o.Interactive, values: map[string]bool{}}
	}
	m := &MainPrint{options: o, processStartMillis: processStartMillis, Owner: cooperative.New(), wake: make(chan struct{}, 1), finish: make(chan struct{}), outputDone: make(chan struct{})}
	m.nativeSDKAgentSummaryEnabled = &atomic.Bool{}
	if o.DetachedAgent != nil && o.DetachedAgent.authParent != nil {
		m.nativeSDKAgentSummaryEnabled = o.DetachedAgent.authParent.nativeSDKAgentSummaryEnabled
	}
	m.nativeTerminalSetupTerminal = nativeTerminalSetupInitialTerminal(n)
	m.nativeTerminalSetupPresentation = nativeTerminalSetupInitialPresentation(o)
	if m.options.CloudAuthStatus == nil {
		m.options.CloudAuthStatus = cloudauth.NewAuthStatusManager()
	}
	if m.options.SessionIngressAuth == nil {
		m.options.SessionIngressAuth = remoteio.NewIngressAuthWithDirectory(n.Environment, n.CCRCredentialDirectory)
	}
	m.agentChild = o.DetachedAgent != nil
	m.mainThreadAgent = &nativeMainThreadAgentState{requestedModel: o.Arguments.Model, requestedPrompt: o.Arguments.SystemPrompt}
	m.detachedConstructed = o.DetachedAgent != nil
	fail := func(e error) (*MainPrint, error) {
		if m.remoteManaged != nil && m.remoteManaged.owner == m.Owner {
			m.remoteManaged.close()
		}
		m.nativeMigrations.close()
		m.closeNativeFileAnalytics()
		m.BeginNativePromptClose()
		_ = m.joinNativePrompt(context.Background())
		m.joinNativeChromeInstallation()
		m.closeCLIStartupFiles()
		m.CloseNativeTerminalStatusLine()
		m.closeNativeErrorLog()
		if m.ownsTrace && m.options.Trace != nil {
			_ = m.options.Trace.Close(context.Background())
		}
		m.closeNativeComputerUse()
		if m.mcpRuntime != nil && !m.mcpBorrowed {
			_ = m.mcpRuntime.Close(context.Background())
		}
		if m.claudeAIMCP != nil && !m.claudeAIMCPBorrowed {
			m.claudeAIMCP.Close()
		}
		m.joinNativeCommandSources()
		m.Owner.Close()
		if m.nativeSettings != nil {
			_ = m.nativeSettings.Close(context.Background())
		} else if m.Global != nil {
			m.Global.Close()
		}
		return nil, e
	}
	diag, e := diagnostics.NewProcess(diagnostics.Options{Environment: func(k string) string { return n.Environment[k] }, Now: n.Now, NowMillis: func() int64 { return int64(n.NowMillis()) }, PID: o.PID, Platform: o.Platform, Files: mainDiagnosticFiles{n: n}})
	if e != nil {
		return fail(e)
	}
	sid := o.Arguments.SessionID
	if sid == "" {
		sid = uuidMain()
	}
	m.diagnostics = diag
	m.state, e = bootstrapstate.New(m.Owner, sid, n.CWD, func() (string, error) { return m.mainUUID(), nil })
	if e != nil {
		return fail(e)
	}
	m.factory, e = messages.NewFactory(m.mainUUID, n.Now)
	if e != nil {
		return fail(e)
	}
	m.settingState, e = clisettings.NewState(n.NowMillis)
	if e != nil {
		return fail(e)
	}
	config := n.ConfigHomeDirectory()
	if n.NativeProcess && m.options.Trace == nil {
		traceScope := config
		if traceScope == "" {
			traceScope = filepath.Join(n.Home, ".claude")
		}
		if !filepath.IsAbs(traceScope) {
			traceScope = filepath.Join(n.CWD, traceScope)
		}
		m.options.Trace, e = tracecapture.New(tracecapture.Options{Scope: traceScope, Env: func(k string) string { return n.Environment[k] }, EntryPoint: n.Environment["CLAUDE_CODE_ENTRYPOINT"], Now: n.Now, UUID: uuidMain, OnError: func(err error) { _, _ = fmt.Fprintf(o.Error, "[TraceCapture] %v\n", err) }})
		if e != nil {
			return fail(e)
		}
		m.ownsTrace = true
	}
	settingsProfile := clisettings.ExternalReleaseProfile
	if n.NativeProcess {
		settingsProfile = clisettings.NativeProcessProfile
	}
	settingsOptions := clisettings.Options{State: m.settingState, ConfigHome: config, OriginalCWD: n.CWD, Profile: settingsProfile, Now: n.NowMillis, PID: o.PID, FS: mainSettingsFiles{n: n}}
	if n.NativeProcess {
		settingsOptions.NativeUserSettingsPath = func() (string, error) { return n.nativeUserSettingsPath(n.CWD, m.environment) }
	}
	m.settings, e = clisettings.NewService(settingsOptions)
	if e != nil {
		return fail(e)
	}
	m.Global, e = cliglobalconfig.New(cliglobalconfig.Options{Home: n.Home, ConfigDir: originalGlobalConfigDirectory(n, config), Now: n.Now, NowMillis: n.NowMillis, PID: o.PID, FS: mainGlobalFiles{n: n}, Diagnostics: diag, Stderr: func(s string) { io.WriteString(o.Error, s) }})
	if e != nil {
		return fail(e)
	}
	env := protocol.NewJSONObject()
	for k, v := range n.Environment {
		env.Set(k, quoteMain(v))
	}
	m.environment, e = clistartupenv.NewEnvironment(env)
	if e != nil {
		return fail(e)
	}
	n.bindProcessEnvironment(m.environment)
	m.initializeNativeAPIKeyHelper()
	m.initializeNativeCloudSettings()
	m.tls, e = tlsconfig.NewState(n.readFile)
	if e != nil {
		return fail(e)
	}
	if e = m.startCLIStartupFiles(sid); e != nil {
		return fail(e)
	}

	startupHost := clistartupenv.Host{Profile: clistartupenv.EmptyPolicyReleaseProfile, RemoteCacheAbsent: n.ExternalAbsent, MDMCacheAbsent: n.ExternalAbsent, HKCUCacheAbsent: n.ExternalAbsent, PolicyFilesAbsent: n.ExternalAbsent, PluginBaseAbsent: n.ExternalAbsent, AllowedSources: o.Arguments.Sources.AllowedSettingSources}
	if n.NativeProcess {
		pluginStore := storage.New(storage.Options{})
		plugins, err := newNativePluginBindings(n, o.Platform, m.settings, m.settingState, pluginStore, func() (*protocol.JSONObject, error) { return m.Startup.GetInitialSettings() }, o.Arguments.AdditionalDirectories, o.Arguments.PluginDirectories)
		if err != nil {
			return fail(err)
		}
		m.nativeSettings = &NativeProcessSettings{n: n, platform: o.Platform, sources: o.Arguments.Sources, environment: m.environment, global: m.Global, settingsState: m.settingState, settings: m.settings, plugins: plugins, store: pluginStore, diagnostics: diag, pluginAdditional: append([]string{}, o.Arguments.AdditionalDirectories...), pluginInline: append([]string{}, o.Arguments.PluginDirectories...)}
		startupHost.Profile = clistartupenv.ResolvedPolicyReleaseProfile
		if o.Arguments.Remote != nil || o.Arguments.Teleport != "" || o.Arguments.TeleportWithoutValue {
			// Only the actual native CCR CLI producer admits the original
			// ant-only remote backend gate. Ordinary internal host profiles
			// retain their explicit unmigrated-schema guard.
			startupHost.Profile = clistartupenv.NativeRemoteCCRPolicyReleaseProfile
		}
		startupHost.ResolvePolicy = n.ResolvePolicy
		startupHost.PrepareNativePolicy = m.prepareRemoteManagedPolicy
		startupHost.PluginBaseAbsent = false
		startupHost.FlagPath = o.Arguments.Sources.FlagSettingsPath
	}
	startupOptions := clistartupenv.Options{Global: m.Global, Diagnostics: diag, Settings: m.settings, SettingsState: m.settingState, Environment: m.environment, ConfigHome: config, FS: mainSettingsFiles{n: n}, NowMillis: n.NowMillis, Host: startupHost}
	if m.nativeSettings != nil {
		startupOptions.Plugins = m.nativeSettings.plugins.loader
		startupOptions.MergeCcHahaEnvironment = m.nativeMergeCcHahaEnvironment
		startupOptions.NativeCcHahaConfigDirectory = m.nativeCcHahaConfigDirectory
	}
	m.Startup, e = clistartupenv.New(startupOptions)
	if e != nil {
		return fail(e)
	}
	if m.nativeSettings != nil {
		m.Startup.CaptureSpawnEnvironment()
		m.nativeSettings.startup = m.Startup
		securePlatform := o.Platform
		if securePlatform == "windows" {
			securePlatform = "win32"
		}
		m.secure, e = securestorage.New(securestorage.Options{Home: n.Home, ConfigDir: config, ConfigDirExplicit: n.ConfigDirExplicit, Platform: securePlatform, Environment: n.Environment, RunSecurity: n.RunSecurity, NativePlaintextLocation: m.nativePlaintextLocation, NativeKeychainServiceName: m.nativeKeychainServiceName, NativeKeychainUser: m.nativeKeychainUser, NativeSecurityContext: m.nativeSecurityContext})
		if e != nil {
			return fail(e)
		}
		m.openAIAuth, e = m.NewNativeOpenAIAuth(m.nativeOpenAIRefresh)
		if e != nil {
			return fail(e)
		}
		if e = m.attachRemoteManagedPolicy(); e != nil {
			return fail(e)
		}
		// The normal init caller enables config and applies trusted settings
		// before plugin/action setup. Keep that transport environment separate
		// from the later post-trust merged environment used by model requests.
		if e = m.Global.Enable(); e != nil {
			return fail(e)
		}
		if !m.agentChild {
			if e = m.Startup.ApplySafeConfigEnvironmentVariables(); e != nil {
				return fail(e)
			}
			if e = m.Startup.ApplyExtraCACertsFromConfig(); e != nil {
				return fail(e)
			}
			m.nativeAPIPreconnect.environment = m.nativeOpenAIEnvironment()
			if e = m.Global.RecordFirstStartTime(); e != nil {
				return fail(e)
			}
		}
		if _, e = m.nativeSettings.LoadPlugins(context.Background(), "", false); e != nil {
			return fail(e)
		}
		m.pluginOptions, e = m.nativeSettings.PluginOptions(m.secure)
		if e != nil {
			return fail(e)
		}

	}
	if e = m.initializeNativeMigrations(); e != nil {
		return fail(e)
	}
	// Native init has already applied its safe environment. Plugin loading may
	// have invalidated the merged cache before the action's full settings read.
	initialSettings, settingsError := m.Startup.GetInitialSettings()
	if settingsError != nil {
		return fail(settingsError)
	}
	if n.NativeProcess && o.Arguments.Thinking.Type == "" {
		o.Arguments.Thinking = resolveNativeThinking(initialSettings, n.Environment, o.Arguments.MaxThinkingTokens)
		m.options.Arguments.Thinking = o.Arguments.Thinking
	}
	if !n.NativeProcess {
		if e = checkPrintAgentSettings(m.Startup); e != nil {
			return fail(e)
		}
	}
	if e = m.Global.Enable(); e != nil {
		return fail(e)
	}
	if n.NativeProcess && !m.agentChild {
		if e = m.Startup.ApplyConfigEnvironmentVariables(); e != nil {
			return fail(e)
		}
		n.Environment = m.nativeOpenAIEnvironment()
	}
	if n.NativeProcess || o.PublicPrintLoadErrors {
		if e = m.initializeNativeErrorLog(); e != nil {
			return fail(e)
		}
	}
	_, e = m.Global.Get()
	if e != nil {
		return fail(e)
	}
	if n.NativeProcess {
		e = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			var err error
			o.Arguments.Model, m.nativeModelSelector, err = m.resolveNativeModel(context.Background(), scope, initialSettings, o.Arguments.Model)
			if err == nil {
				err = m.resolveNativeFallbackModel()
			}
			return err
		})
		if e != nil {
			return fail(e)
		}
		m.options.Arguments.Model = o.Arguments.Model
	}
	if e = m.configureNativeTeamWorker(initialSettings); e != nil {
		return fail(e)
	}
	o = m.options
	if _, e = m.Global.GetOrCreateUserID(); e != nil {
		return fail(e)
	}
	if e = m.prepareNativePermissions(initialSettings); e != nil {
		return fail(e)
	}
	private := mainResumeFiles{n: n}
	loader, e := sessionhistory.New(sessionhistory.Options{Files: private, Stat: func(p string) (int64, error) {
		i, e := n.stat(p)
		if e != nil {
			return 0, e
		}
		return i.Size(), nil
	}, ReadFile: n.readFile, DisablePrecompactSkip: func() bool { return false }, LeafPrune: func() bool { return true }, ParseDate: sessionprojection.ParseDateUTC, Compare: sessionhistory.CompareStrings, OnDiagnostic: func(sessionhistory.Diagnostic) {}})
	if e != nil {
		return fail(e)
	}
	m.insightsHistory = loader
	resumeOnError := func(err error) {
		if o.PublicPrintLoadErrors {
			m.errorLog.Log(err)
		} else {
			_, _ = io.WriteString(o.Error, err.Error()+"\n")
		}
	}
	reducer, e := sessionresume.New(sessionresume.Options{Factory: m.factory, TranscriptClassifier: true, RelativePath: func(raw json.RawMessage) (json.RawMessage, error) {
		return sessionresume.RelativePath(n.CWD, n.CWD, raw)
	}, OnDiagnostic: func(sessionresume.Diagnostic) {}, OnError: resumeOnError})
	if e != nil {
		return fail(e)
	}
	hookProfile := queryhooks.EmptyExternalProfile
	if n.NativeProcess {
		hookProfile = queryhooks.ResolvedSettingsProfile
		if d := o.DetachedAgent; d != nil && d.preset != nil {
			m.hookEnvironment = d.preset.parent.hookEnvironment
		}
		if m.hookEnvironment == nil {
			m.hookEnvironment, e = sessionenvironment.New(config, o.Platform)
			if e != nil {
				return fail(e)
			}
		}
	}
	if e = m.initializeNativeAsyncHooks(); e != nil {
		return fail(e)
	}
	hookOptions := queryhooks.Options{MergedSettings: m.nativeSDKInitialSettings, Profile: hookProfile, Settings: m.settings, SettingsState: m.settingState, OriginalEnv: map[string]string{}, MainSessionID: sid, PolicyAbsent: n.ExternalAbsent, FlagAbsent: o.Arguments.Sources.FlagSettingsPath == nil, PluginBaseAbsent: startupHost.PluginBaseAbsent, UUID: uuidMain}
	if n.NativeProcess {
		if value, exists := n.Environment["CLAUDE_CODE_SIMPLE"]; exists {
			hookOptions.OriginalEnv["CLAUDE_CODE_SIMPLE"] = value
		}
	}
	hookOptions.Commands = m.nativeHookCommandBindings()
	hookOptions.Prompts = m.nativePromptHookBindings()
	hookOptions.Agents = m.nativeAgentHookBindings()
	hookOptions.HTTP = m.nativeHTTPHookBindings()
	hookOptions.UpdateWatchPaths = m.updateNativeEnvironmentWatchPaths
	hookOptions.ForceSyncSessionStart = o.Arguments.InitOnly
	hookOptions.SessionStartBase = m.nativeSessionStartBase
	hookOptions.SessionStartMetadata = m.nativeSessionStartMetadata
	hookOptions.CurrentSessionID = func() string {
		if m.Controller == nil {
			return sid
		}
		return m.Controller.SessionID()
	}
	if n.NativeProcess {
		hookOptions.PolicySettings = func() (*protocol.JSONObject, error) { return m.Startup.GetSettingsForSource("policySettings") }
	}
	if m.nativeSettings != nil {
		hookOptions.PluginHooks = m.nativePluginHooks
	}
	m.hook, e = queryhooks.New(hookOptions)
	if e != nil {
		return fail(e)
	}
	if o.Arguments.Worktree != nil {
		if e = m.setupNativeWorktree(context.Background()); e != nil {
			return fail(e)
		}
		n = m.options.Native
		initialSettings, e = m.Startup.GetInitialSettings()
		if e != nil {
			return fail(e)
		}
		if e = m.prepareNativePermissions(initialSettings); e != nil {
			return fail(e)
		}
		hookOptions.Settings = m.settings
		hookOptions.SettingsState = m.settingState
		hookOptions.MergedSettings = m.nativeSDKInitialSettings
		m.hook, e = queryhooks.New(hookOptions)
		if e != nil {
			return fail(e)
		}
	}
	if n.NativeProcess {
		if e = m.newNativeAgentCatalog(); e != nil {
			return fail(e)
		}
		if e = m.initializeNativeMainThreadAgent(); e != nil {
			return fail(e)
		}
		o = m.options
		if e = m.initializeNativeCLIBetas(); e != nil {
			return fail(e)
		}
		if e = m.newNativeMCP(); e != nil {
			return fail(e)
		}
		o = m.options
	}
	memory := &usercontext.ExplicitUserMemory{Profile: usercontext.NormalEmptyMemoryProfile, Files: mainSettingsFiles{n: n}, ConfigHome: config, Normal: &usercontext.NormalMemoryDiscovery{CWD: n.CWD, ManagedRoot: n.ManagedRoot, EnabledSources: o.Arguments.Sources.EnabledSettingSources, AutoMemoryEnabled: true, ReadDir: n.readDir}}
	var memoryPaths *usercontext.MemoryPaths
	if n.NativeProcess {
		memory, memoryPaths, e = m.nativeMemory()
		if e != nil {
			return fail(e)
		}
	}
	uc, e := usercontext.New(usercontext.Options{AdditionalDirectories: m.nativeInstructionDirectories(), Environment: m.environment, Settings: m.settings, SettingsState: m.settingState, Now: n.Now, NowMillis: n.NowMillis, Diagnostics: diag, Profile: usercontext.ExternalProfile, Memory: memory})
	if e != nil {
		return fail(e)
	}
	if e = m.newNativeComputerUse(); e != nil {
		return fail(e)
	}
	m.Cleanup, e = runtimecleanup.New(m.Owner)
	if e != nil {
		return fail(e)
	}
	m.Activity, e = sessionactivity.NewCallbackNull(m.Owner, m.Cleanup, diag)
	if e != nil {
		return fail(e)
	}
	m.Worker, e = sessionstate.New(m.Owner, m.nativeSessionStateOptions())
	if e != nil {
		return fail(e)
	}
	m.watch = &gitbranch.NativePollWatch{Owner: m.Owner, Stat: n.stat}
	m.Git, e = gitbranch.New(gitbranch.Options{Owner: m.Owner, Diagnostics: diag, CWD: m.workingDirectory, Now: func() int64 { return int64(n.NowMillis()) }, StatSync: n.stat, Stat: n.stat, ReadFile: n.readFile, WatchFileOwned: m.watch.WatchOwned, ScrollDraining: func() bool { return false }, NodeEnv: n.Environment["NODE_ENV"], RegisterCleanupOwned: func(scope *cooperative.Scope, c func(*cooperative.Scope) error) error {
		_, e := m.Cleanup.Register(scope, runtimecleanup.NewCallback(func(next *cooperative.Scope) (*runtimecleanup.Future, error) { return nil, c(next) }))
		return e
	}})
	if e != nil {
		return fail(e)
	}
	if e = m.newNativeWorkflowDefinitions(); e != nil {
		return fail(e)
	}
	registryType := n.Environment["USER_TYPE"]
	if n.NativeProcess && registryType == "" {
		registryType = "external"
	}
	registryOptions := sdkhost.PrintRegistryOptions{DisableSlashCommands: o.Arguments.DisableSlashCommands, Interactive: o.Interactive, CWD: n.CWD, Home: n.Home, Stat: n.stat, ReadDir: n.readDir, Environment: func(k string) string { return n.Environment[k] }, EntryPoint: "sdk-cli", UserType: registryType}
	remoteCatalog, e := m.nativeRemoteCatalogEnabled(context.Background())
	if e != nil {
		return fail(e)
	}
	if m.nativeSettings != nil {
		m.skillCommands, e = m.nativeSettings.SkillCommands(NativeSkillOptions{AdditionalDirectories: append([]string{}, m.nativePermissionInitial.InstructionDirectories...)})
		if e != nil {
			return fail(e)
		}
		definitions, plugins, err := m.nativeCommandDefinitions(context.Background())
		if err != nil {
			return fail(err)
		}
		registryOptions.NativePrompts = &definitions
		registryOptions.NativeWorkflowCommands = &definitions
		registryOptions.Plugins = plugins
	}
	if m.agents != nil && !remoteCatalog {
		var names []string
		if e = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			definitions, err := m.agents.List(scope)
			if err != nil {
				return err
			}
			names = []string{}
			for _, definition := range definitions.Active {
				names = append(names, definition.Name)
			}
			return nil
		}); e != nil {
			return fail(e)
		}
		registryOptions.NativeAgents = &names
	}
	if remoteCatalog {
		names := []string{}
		registryOptions.NativeAgents = &names
		m.Registry, e = sdkhost.NewNativeRemotePrintRegistry(registryOptions)
	} else {
		m.Registry, e = sdkhost.NewPrintRegistry(registryOptions)
	}
	if e != nil {
		return fail(e)
	}
	var promptResolver func(context.Context, []protocol.JSONObject, string) ([]json.RawMessage, error)
	m.userContext = uc
	m.memoryPaths = memoryPaths
	if e = m.prepareNativeAutoDream(); e != nil {
		return fail(e)
	}
	if n.NativeProcess {
		m.prompt, e = m.nativeSystemPrompt(uc, memoryPaths)
		if e != nil {
			return fail(e)
		}
		if o.Arguments.SystemPrompt == "" && !o.Arguments.HasCustomSystemPrompt {
			promptResolver = m.prompt.ResolveForModel
		}
	}
	var beginPrompt func(*cooperative.Scope, []protocol.JSONObject, string) (*ownerasync.OwnerFuture[[]json.RawMessage], error)
	var beginUser func(*cooperative.Scope) (*ownerasync.OwnerFuture[*protocol.JSONObject], error)
	if n.NativeProcess {
		beginPrompt, beginUser = m.beginNativeDefaultPrompt, m.beginNativeUserContext
	}
	branch, e := sessioncontroller.NewOwnedGitRecordBranch(m.state, m.Git)
	if e != nil {
		return fail(e)
	}
	onError := func(e error) { io.WriteString(o.Error, e.Error()+"\n") }
	controllerOptions, e := m.nativeDetachedControllerOptions(sessioncontroller.Options{NativeRemoteFrontend: remoteCatalog, NativeRemoteCLI: !o.Arguments.Continue && (o.Arguments.Teleport != "" || o.Arguments.TeleportWithoutValue || o.Interactive && o.Arguments.Remote != nil), TeamContext: m.nativeTeamContext(), AgentOmitClaudeMd: m.nativeTeamOmitClaudeMd(), NativeWorktreeResume: m.nativeRestoreWorktreeForResume, ResolveSystemPromptForModel: promptResolver, BeginNativeDefaultPrompt: beginPrompt, BeginNativeUserContext: beginUser, PublicPrint: true, NativeInteractiveQueue: o.ForegroundShellProgress && o.AgentID == "", NativeTools: n.NativeProcess, Diagnostics: diag, StartupHooks: m.hook, UserContext: uc, RestoreCosts: m.restoreProjectCosts, NowMillis: n.NowMillis, PublicResumeStartup: m.Startup, Bootstrap: m.state, AsyncPersistence: true, RecordBranchProfile: sessioncontroller.OwnedGitRecordProfile, BeginOwnedRecordBranch: branch, ResumeOptions: resumestate.Options{NativeProjectPaths: n.NativeProcess, NativeSkillBindings: func() bool { return n.NativeProcess && m.skillCommands != nil && m.nativeSettings != nil }, PublicSID: true, StartupHooks: m.hook, ConfigDir: config, CWD: n.CWD, OriginalCWD: n.CWD, CurrentSessionID: sid, NonInteractive: true, Environment: n.Environment, Files: private, Loader: loader, Reducer: reducer, ParseDate: sessionprojection.ParseDateUTC, Stat: func(p string) (resumestate.Stat, error) {
		i, e := n.stat(p)
		if e != nil {
			return resumestate.Stat{}, e
		}
		birth := float64(0)
		if n.NativeProcess {
			birth = nativeResumeBirthMillis(p, i)
		}
		return resumestate.Stat{Size: i.Size(), Modified: nativeResumeDateMillis(i.ModTime()), Birth: birth}, nil
	}, Now: func() int64 { return n.Now().UnixMilli() }, UUID: m.mainUUID, OnDiagnostic: func(sessionresume.Diagnostic) {}, OnError: resumeOnError, OnPhase: func(string) {}, PolicyPaths: []string{filepath.Join(n.ManagedRoot, "managed-settings.json")}, SettingsState: m.settingState}, WriterOptions: sessioncontroller.WriterOptions{SessionPersistenceDisabled: o.Arguments.SessionPersistenceDisabled, ConfigDir: config, CWD: n.CWD, SessionID: sid, Version: o.Version, Environment: n.Environment, SettingsState: m.settingState, Files: mainTranscriptFiles{n: n, committed: o.OnCanonicalTranscriptMutation}, OnError: onError, Schedule: sessioncontroller.RealSchedule, Now: n.Now}, Factory: m.factory, UUID: m.mainUUID, Now: n.Now, OnPhase: m.controllerPhase, OnSDKUUIDLookup: o.OnSDKUUIDLookup, OnSDKRecord: o.OnSDKRecord, ReplayUserMessages: o.Arguments.Replay, BaseToolsCLI: o.Arguments.Tools, CustomSystemPrompt: quoteMain(o.Arguments.SystemPrompt)})
	if e != nil {
		return fail(e)
	}
	m.Controller, e = sessioncontroller.New(controllerOptions)
	if e != nil {
		return fail(e)
	}
	if m.worktreeState != nil && m.worktreeState.startupMetadata != nil {
		metadata := protocol.NewJSONObject(protocol.JSONField{Name: "worktreeSession", Value: m.worktreeState.startupMetadata})
		m.Controller.Writer().RestoreMetadata(metadata)
	}
	return m, nil
}
func (m *MainPrint) enqueue(b []byte) error {
	if m.options.OnOutput != nil {
		return m.options.OnOutput(append([]byte(nil), b...))
	}
	m.outputMu.Lock()
	defer m.outputMu.Unlock()
	if m.outputError != nil {
		return m.outputError
	}
	m.outputs = append(m.outputs, append([]byte(nil), b...))
	select {
	case m.wake <- struct{}{}:
	default:
	}
	return nil
}
func (m *MainPrint) drain() {
	defer close(m.outputDone)
	index := 0
	finishing := false
	for {
		m.outputMu.Lock()
		var b []byte
		if index < len(m.outputs) {
			b = append([]byte(nil), m.outputs[index]...)
			index++
		}
		m.outputMu.Unlock()
		if b != nil {
			line := b
			if len(line) == 0 || line[len(line)-1] != '\n' {
				line = append(line, '\n')
			}
			_, e := m.options.Output.Write(line)
			if e != nil {
				m.outputMu.Lock()
				m.outputError = e
				m.outputMu.Unlock()
			}
			continue
		}
		if finishing {
			return
		}
		select {
		case <-m.wake:
		case <-m.finish:
			finishing = true
		}
	}
}

// initializeOnOwner constructs the sole SDK host and activates the original
// Controller. CLI streaming and long-lived desktop sessions share this method.
func (m *MainPrint) initializeOnOwner(scope *cooperative.Scope) error {
	if m.initialized {
		return nil
	}
	if m.options.Native.NativeProcess {
		if _, _, e := m.nativePromptProducer(scope); e != nil {
			return e
		}
	}
	if e := m.initializeNativeHTTPPool(scope); e != nil {
		return e
	}
	if e := m.initializeTerminalExportStartup(scope); e != nil {
		return e
	}
	if e := m.initializeNativeSandbox(scope); e != nil {
		return e
	}
	if e := m.initializeDetachedAgent(scope); e != nil {
		return e
	}
	if e := m.initializeWorkflowSynthetic(scope); e != nil {
		return e
	}
	if e := m.initializeNativeJSONSchema(scope); e != nil {
		return e
	}
	if e := m.newNativePlans(); e != nil {
		return e
	}
	n := m.options.Native
	o := m.options
	if !m.agentChild {
		if e := m.state.SetOriginalCWD(scope, n.CWD); e != nil {
			return e
		}
	}
	if e := m.state.SetCWD(scope, n.CWD); e != nil {
		return e
	}
	binding, e := m.Controller.BorrowNativeCopyBinding(m.Owner)
	if e != nil {
		return e
	}
	if e = m.bindNativeRemotePersistence(scope); e != nil {
		return e
	}
	if e = m.initializeNativeFileHistory(scope); e != nil {
		return e
	}
	if e = m.initializeMCPTools(scope, binding); e != nil {
		return e
	}
	if e = m.initializeNativeLSPOnOwner(scope, binding); e != nil {
		return e
	}
	if e = m.registerNativeComputerUse(scope, binding); e != nil {
		return e
	}
	if e = m.initializeNativeFileAnalytics(scope); e != nil {
		return e
	}
	var fileSkills func(context.Context, *cooperative.Scope, string) (*ownerasync.OwnerFuture[struct{}], error)
	if n.NativeProcess {
		fileSkills = m.nativeFileSkills
	}
	cache, e := builtinwrite.NewFileStateCache(m.Owner, 100, 25*1024*1024)
	if e != nil {
		return e
	}
	m.nativeFileCache = cache
	write, e := builtinwrite.New(builtinwrite.Options{LSP: m.nativeLSP, ReportEvent: m.nativeFileReporter(), FileHistoryTrack: m.nativeTrackFileHistory, FileSkills: fileSkills, Owner: m.Owner, NativeBinding: binding, Cache: cache, Home: n.Home, CWD: n.CWD, PID: o.PID, NowMillis: n.NowMillis, Environment: func(k string) string { return n.Environment[k] }, PermissionContext: func(s *cooperative.Scope) (protocol.JSONObject, error) {
		return m.nativeToolPermissionContext(s)
	}, PermissionRoots: m.nativeFilePermissionRoots(), Host: builtinwrite.Host{Platform: o.Platform}, HostState: func(s *cooperative.Scope) (builtinwrite.Host, error) {
		if !n.NativeProcess || !m.hook.HasNativeCommandBindings(m.Owner) {
			if e := m.hook.EmptyEvent("PreToolUse", m.Controller.SessionID()); e != nil {
				return builtinwrite.Host{}, e
			}
		}
		if !n.ExternalAbsent && !n.NativeProcess {
			return builtinwrite.Host{}, mainCap("native tool dependency snapshot")
		}
		return builtinwrite.Host{Platform: o.Platform, NonInteractive: !o.Interactive, ConditionalSkillsPresent: m.skillCommands != nil && m.skillCommands.HasConditional(), AnalyticsEnabled: !nativeAnalyticsDisabled(n.Environment), LSPEnabled: m.nativeLSP.Enabled()}, nil
	}, Files: mainWriteFiles{mainSettingsFiles{n: n}}})
	if e != nil {
		return e
	}
	var tools *builtintools.Service
	if n.NativeProcess {
		shells, shellError := m.nativeShellResolverOnOwner(scope)
		if shellError != nil {
			return shellError
		}
		m.nativeImageHTTP, e = cooperative.NewNativeImageHTTP(scope, m.Owner)
		if e != nil {
			return e
		}
		if m.taskStore == nil {
			m.taskStore, e = taskstore.New(taskstore.Options{ConfigDir: n.ConfigHomeDirectory(), NativeConfigDirectory: m.nativeTaskConfigDirectory, UserType: n.Environment["USER_TYPE"], PID: o.PID, Now: n.Now})
			if e != nil {
				return e
			}
		}
		webFetch, e := m.nativeWebFetchBindings()
		if e != nil {
			return e
		}
		webSearch, e := m.nativeWebSearchBindings()
		if e != nil {
			return e
		}
		worktreeBindings, e := m.nativeWorktreeBindings()
		if e != nil {
			return e
		}
		toolOptions := builtintools.Options{LSP: m.nativeLSP, CountTokensOnOwner: m.nativeReadTokenCount, ShellHints: m.nativeShellHintsBindings(), Shells: shells, ReportEvent: m.nativeFileReporter(), Images: m.nativeImageBindings(), BashCwd: m.nativeBashCwdBindings(), BashSandbox: m.nativeSandboxBindings(), ProcessEnvironment: m.nativeProcessEnvironment, SessionEnvironmentCapture: m.nativeSessionEnvironmentCapture, SessionEnvironmentScript: m.nativeSessionEnvironmentScript, HookPathSafety: m.nativeHookPathSafetyContext, HookInternalEditable: m.nativeHookInternalEditable, InternalReadable: m.nativeInternalReadable, HookPermissionRoot: m.nativeHookPermissionRoot, TeamMessage: m.nativeTeamMessageBindings(), FileHistoryTrack: m.nativeTrackFileHistory, ToolResultsDirectory: m.Controller.ToolResultsDirectoryOnOwner, ForegroundShellProgress: o.ForegroundShellProgress, Workflow: m.nativeWorkflowBindings(), SessionCollaboration: m.nativeSessionCollaborationBindings(), ToolSearch: m.nativeToolSearchBindings(), Worktree: worktreeBindings, BackgroundTasks: m.nativeBackgroundBindings(), MCPResources: m.nativeMCPResources(), Skill: m.nativeSkillBindings(), FileSkills: fileSkills, WebSearch: webSearch, WebFetch: webFetch, PlanMode: m.nativePlanModeBindings(), Random: nativeMathRandom, TaskStore: m.taskStore, TaskListID: m.Controller.TaskListIDOnOwner, ExpandTaskView: m.expandNativeTerminalTaskView, TaskHookWithContext: m.nativeTaskHookWithContext, GetTodos: m.Controller.GetTodosOnOwner, SetTodos: m.Controller.SetTodosOnOwner, NonInteractive: !o.Interactive, RunAgent: m.runAgent, RunBackgroundAgent: m.runBackgroundAgent, ResumeAgent: m.resumeNativeAgentForTool, SendMessageEnabled: m.nativeSendMessageEnabled, AgentDefinitions: func(scope *cooperative.Scope) ([]agentcatalog.Definition, error) {
			if m.agents == nil {
				return nil, mainCap("actual configured agent producer")
			}
			definitions, err := m.agents.List(scope)
			if err != nil {
				return nil, err
			}
			return definitions.Active, nil
		}, AgentPrompt: m.nativeAgentPrompt, AgentPromptJSON: m.nativeAgentPromptJSON, AgentAdditionalTools: func(scope *cooperative.Scope) ([]queryloop.RegisteredTool, error) {
			if m.mcpTools == nil {
				return []queryloop.RegisteredTool{}, nil
			}
			_, registered, err := m.mcpTools.Catalog(scope)
			return registered, err
		}, Owner: m.Owner, NativeBinding: binding, Cache: cache, Write: write, Home: n.Home, CWD: n.CWD, Platform: o.Platform, NowMillis: n.NowMillis, Environment: n.Environment, PermissionContext: func(s *cooperative.Scope) (protocol.JSONObject, error) {
			return m.nativeToolPermissionContext(s)
		}, PermissionRoots: m.nativeFilePermissionRoots()}
		if d := o.DetachedAgent; d != nil && d.sourceMCP != nil {
			toolOptions, e = d.sourceMCP.workflowChildOptions(scope, m, toolOptions)
			if e != nil {
				return e
			}
		}
		tools, e = builtintools.New(toolOptions)
		if e != nil {
			return e
		}
	}
	if tools != nil && n.NativeProcess && (o.DetachedAgent == nil || o.DetachedAgent.sourceMCP == nil) {
		if e := tools.BindNativeAgentSkills(scope, m.Controller, m.skillCommands); e != nil {
			return e
		}
		if e := tools.BindNativeAgentMCP(scope, m.Controller, m.mcpTools, m.mcpRuntime, m.mcpConfigurations); e != nil {
			return e
		}
	}
	m.nativeTools = tools
	if tools != nil {
		teamBindings, err := m.nativeTeamBindings(scope)
		if err != nil {
			return err
		}
		if err = tools.BindTeam(scope, teamBindings); err != nil {
			return err
		}
	}
	if tools != nil && m.options.DetachedAgent == nil {
		if _, e = m.Cleanup.Register(scope, runtimecleanup.NewCallback(tools.StartBackgroundCleanup)); e != nil {
			return e
		}
	}
	mode := o.Arguments.PermissionMode
	if n.NativeProcess && !m.agentChild {
		if !m.nativePermissionStartup.Owns(m.Owner) {
			return cooperative.ErrRestricted
		}
		mode = m.nativePermissionInitial.Mode
	} else if mode == "" {
		mode = "bypassPermissions"
	}
	var permissionRules map[string]sdkpermissions.RuleSet
	if n.NativeProcess {
		permissionRules, e = m.nativePermissionRules()
		if e != nil {
			return e
		}
		if !m.agentChild || m.detachedConstructed {
			accounting, err := m.Controller.ModelAccountingFor(m.Owner)
			if err != nil {
				return err
			}
			if err = accounting.InstallNativeScopedCalculator(scope, func(current *cooperative.Scope, model string, environment map[string]string) (*modelaccounting.Calculator, error) {
				// Accounting reads process model controls, which are intentionally absent from the wire request environment.
				return modelaccounting.NewNativeCalculator(model, modelaccounting.NativeCalculatorOptions{Environment: m.nativeOpenAIEnvironment(), Settings: m.nativeSDKInitialSettings, Global: m.Global.Get, SDKBetas: m.nativeSDKBetas, ModelSelector: func(options modelselection.Options) (*modelselection.Selector, error) {
					return m.nativeModelSelection(context.Background(), current, options)
				}})
			}); err != nil {
				return err
			}
		}
	}
	var runtimeModel func(*cooperative.Scope, querypreflight.RuntimeModelInput) (string, error)
	if n.NativeProcess {
		runtimeModel = func(scope *cooperative.Scope, in querypreflight.RuntimeModelInput) (string, error) {
			if !scope.BelongsTo(m.Owner) || m.nativeModelSelector == nil {
				return "", cooperative.ErrRestricted
			}
			model, err := m.nativeModelSelector.RuntimeModel(quoteMain(in.MainLoopModel), in.PermissionMode, in.Exceeds200kTokens)
			if err != nil {
				return "", err
			}
			return decodeNativeModel(model)
		}
	}
	initialEffort, e := m.initialNativeEffort()
	if e != nil {
		return e
	}
	var skillInvocation func(*cooperative.Scope) (skillcommands.Invocation, error)
	var processSkillPrompt func(context.Context, *cooperative.Scope, string, string, []queryloop.Message, json.RawMessage) (*sessioncontroller.ProcessedInput, error)
	var inputAttachments func(context.Context, *cooperative.Scope, string, []queryloop.Message) ([]queryloop.Message, error)
	if m.nativeSettings != nil {
		skillInvocation = m.nativeSkillInvocation
		processSkillPrompt = m.nativeProcessSkillPrompt
		inputAttachments = m.nativeInputAttachments
	}
	var stopTask func(*cooperative.Scope, string) (protocol.JSONObject, error)
	if tools != nil && tools.HasBackgroundTaskBindings() {
		stopTask = tools.StopBackgroundTaskFromControl
	}
	teamWorkerBinding, e := m.nativeTeamWorkerBinding(scope)
	if e != nil {
		return e
	}
	var teamExecution *sdkhost.NativeTeamExecution
	if m.nativeInProcessTeammateIdentity() != nil {
		if teamWorkerBinding != nil {
			return cooperative.ErrRestricted
		}
		teamExecution, e = sdkhost.NewNativeInProcessTeamExecution(scope, m.Owner, m.Controller, tools, o.AgentID)
		if e != nil {
			return e
		}
	} else {
		teamExecution, e = sdkhost.NewNativeTeamExecution(scope, m.Owner, m.Controller, tools, teamWorkerBinding, n.Environment)
		if e != nil {
			return e
		}
	}
	teammateExit, e := m.nativeTeammateExitBinding(scope)
	if e != nil {
		return e
	}
	nativeClaudeAuth := m.nativeClaudeAuthBinding
	if d := o.DetachedAgent; d != nil && d.sourceMCP != nil {
		nativeClaudeAuth = d.sourceMCP.ordinaryClaudeAuthBinding
	}
	if o.Arguments.PermissionPromptToolName == "stdio" && o.ResolvePermission == nil {
		m.options.ResolvePermission = m.nativeSDKPermissionResolver
		o.ResolvePermission = m.options.ResolvePermission
	}
	m.Host, e = sdkhost.NewOwnedPrint(scope, m.nativeHarnessPolicyHostOptions(sdkhost.Options{NativeHTTPPool: m.nativeHTTPPool(), SDKBetas: m.nativeSDKBetas, ObserveRequest: o.ObserveSDKRequest, ObserveFetch: o.ObserveSDKFetch, ObserveNativeFetch: o.ObserveSDKNativeFetch, ObserveModelFailure: o.ObserveModelFailure, NativeValidationAuth: m.nativeModelValidationClaudeAuth, NativeFastMode: m.nativeFastModeBinding, NativePermissionHookRetention: m.nativePermissionHookRetention(), NativeSDKGetSettings: m.nativeSDKGetSettings, NativeSDKMCPControl: m.nativeSDKMCPControl, NativeSDKMCPStatus: m.nativeSDKMCPStatus, NativeSDKMCP: m.sdkMCPState, NativeSDKPrepareTurn: m.prepareNativeSDKMCPTurn, NativeSDKMCPMessage: m.deliverNativeSDKMCPMessage, NativeSDKInitializeOptions: m.nativeSDKInitializeOptions, NativeSDKInferenceControl: m.nativeSDKInferenceControl, NativeSDKHookInitialize: m.nativeSDKInitializeMetadata, NativeSDKHookError: m.nativeSDKHookError, NativeVertex: m.nativeVertexBinding, NativeBedrock: m.nativeBedrockBinding, NativeAuthHelper: m.nativeAuthHelperBinding, NativeClaudeAuth: nativeClaudeAuth, NativeAPIKeySource: m.nativeAPIKeySource, NativeParentSettings: m.nativeParentSettingsProvider(), SendAgentMessage: m.sendAgentMessageOnOwner, StopTask: stopTask, Trace: m.options.Trace, AgentContext: o.AgentContext, OnQueryYield: o.OnQueryYield, Print: &sdkhost.PrintOptions{BeginNativeInitCatalog: m.nativeInitCatalogProducer(), NativeQuotaStatus: m.nativeQuotaStatus, NativeSuggestionSuppression: m.nativeSuggestionSuppression, NativeAgentSummaryEnabled: m.nativeSDKAgentSummaryEnabled, NativeAgentSummaryFeatureGate: m.nativeSDKAgentSummaryFeatureGate, NativeAgentSummary: m.nativeAgentSummaryBindings(), NativeStatusLineSettings: m.nativeStatusLineSettingsBound, NativeToolResult: m.nativeToolResult, NativeAutoDream: m.nativeAutoDream, CloseNativeAutoDream: m.closeNativeAutoDream, NativeThinkingSummaryBeta: m.nativeThinkingSummaryBeta, NativeModelValidationThinkingSummaryBeta: m.nativeModelValidationThinkingSummaryBeta, NativeSpinnerVerb: m.nativeSpinnerVerb, NativeTerminalSpinner: m.nativeTerminalSpinnerObserver(), ToolAllowedNow: m.nativeAgentToolFilter(), NativeMainAgentSettings: m.nativeMainAgentSettingsBound, NativeTypedModelSettings: m.nativeModelCommandSettingsBound, PermissionPromptToolName: o.Arguments.PermissionPromptToolName, NativeInteractive: o.Interactive, NativeKeybindingsEnabled: m.NativeKeybindingsEnabled, NativeInputMedia: m.nativeInputMedia, NativeGoalCommand: m.nativeGoalCommand, NativePlanCommand: m.nativePlanCommand, NativeTerminalSetupCommand: m.nativeTerminalSetupCommand, NativeRewindCommand: m.nativeRewindCommand, NativeLocalCommand: m.nativeLocalCommand, NativeContextUsage: m.nativeContextUsage, NativeSystemContext: m.nativeSystemContext, BeginNativeSystemContext: m.beginNativeSystemContext, OnLocalInputMessages: o.OnLocalInputMessages, AppendSystemPrompt: o.Arguments.AppendSystemPrompt, TeamWorker: teamWorkerBinding, NativeTeamExecution: teamExecution, NativeTeammateExit: teammateExit, NativeTeammateStop: m.nativeTeammateStopBinding(), NativeLeaderInbox: m.nativeLeaderInboxOnOwner, FileHistory: m.fileHistory, AgentIteratorReady: m.nativeAgentIteratorReady(), AgentRestartPrompt: m.nativeAgentRestartPrompt(), ToolSearch: m.toolSearch, PersistPermission: func(scope *cooperative.Scope, update protocol.JSONObject) error {
		return m.persistNativePermission(scope, binding, update)
	}, StartSandboxNetworkPermission: m.options.StartSandboxNetworkPermission, CompactCleanup: m.nativeCompactCleanup, CompactExcludedFiles: m.nativeCompactExcludedFiles, CompactAttachments: m.nativeCompactAttachments, PlanProjection: m.nativePlanProjection, PlanAttachments: m.nativeAttachments, NativeCloudSettings: m.nativeCloudSettingsBindings, ContextAttachments: m.nativeContextAttachments, InputAttachments: inputAttachments, OnlyToolNames: o.OnlyToolNames, NativeSynthetic: m.nativeSynthetic, NativeJSONSchema: m.nativeJSONSchema, NativeMCP: m.mcpTools, SkillInvocation: skillInvocation, ProcessSkillPrompt: processSkillPrompt, ResolveRuntimeModel: runtimeModel, AgentID: o.AgentID, QuerySource: o.QuerySource, Thinking: o.Arguments.Thinking, EffortValue: initialEffort, EffortValueOverridesEnv: o.Arguments.EffortLevel != "" || o.AgentEffortOverridesEnv, Tools: tools, NativeProcess: n.NativeProcess, ResolvePermission: m.nativeAgentPermissionResolver(o.ResolvePermission), Permissions: sdkpermissions.Input{NativeStartup: m.nativePermissionStartup, AdditionalDirectories: o.Arguments.AdditionalDirectories, Profile: sdkpermissions.PublicPrintProfile, NativeProcess: n.NativeProcess, PermissionRules: permissionRules, Platform: o.Platform, Model: o.Arguments.Model, NonInteractive: true, BaseToolsCLI: o.Arguments.Tools, Allowed: o.Arguments.AllowedTools, Disallowed: o.Arguments.DisallowedTools, Mode: mode, AllowBypass: mode == "bypassPermissions" || o.Arguments.AllowDangerouslySkipPermissions}, MaxTurns: o.Arguments.MaxTurns, Write: write, Hooks: m.hook, Registry: m.Registry, ProcessStartMillis: m.processStartMillis}, Diagnostics: m.diagnostics, Activity: m.Activity, RunState: &sdkhost.RunStateOptions{DrainSDK: m.nativeDrainSessionEvents, Registry: m.Cleanup, Worker: m.Worker, GetTasks: m.nativeBackgroundSnapshot, IsShuttingDown: func() bool { return m.shuttingDown }}, Controller: m.Controller, Owner: m.Owner, Factory: m.factory, UUID: m.mainUUID, Now: n.Now, NowMillis: n.NowMillis, Global: m.Global, Startup: m.Startup, Settings: m.settings, SettingsState: m.settingState, Environment: m.environment, Model: o.Arguments.Model, Version: o.Version, CWD: n.CWD, OriginalEnv: n.Environment, Runtime: m.sdkRuntime(), TLSState: m.tls, NativeOpenAI: m.nativeOpenAIConfig, NativeGrok: m.nativeGrokConfig, OnPhase: m.sdkPhase, Output: sdkhost.Output{Enqueue: m.enqueue}}))
	if e != nil {
		return fmt.Errorf("public print SDK host construct: %w", e)
	}
	if n.NativeProcess && !m.agentChild && m.teamWorker == nil {
		if e = m.verifyNativeAutoGateOnOwner(scope); e != nil {
			return e
		}
		if o.Interactive {
			current, err := m.Host.NativePermissionContext(scope)
			if err != nil {
				return err
			}
			opted, err := m.Controller.NativeAutoModeOptIn(scope)
			if err != nil {
				return err
			}
			if current.String("mode") == "auto" && !opted {
				m.nativeAutoConsent = &NativeAutoConsent{ID: uuidMain(), Previous: "default", Startup: true}
			}
		}
	}
	if e = m.bindNativePermissionModeObserver(scope); e != nil {
		return e
	}
	if e = m.bindNativeSDKChildCallbacks(scope); e != nil {
		return e
	}
	if e = m.seedNativeCLITitle(scope); e != nil {
		return e
	}
	if m.agentChild {
		if e = m.Controller.ActivateAgentOwned(scope); e != nil {
			return e
		}
	} else if e = m.startNativeEnvironmentWatcher(scope); e != nil {
		return e
	} else if e = m.runNativeCLISetup(scope); e != nil {
		return e
	} else if o.Arguments.InitOnly {
		f, e := m.Controller.BeginActivateFreshOwned(scope, sessioncontroller.FreshPrintProfile)
		if e != nil {
			return e
		}
		if _, e = f.Await(scope); e != nil {
			return e
		}
	} else if !o.Arguments.Continue && (o.Arguments.Teleport != "" || o.Arguments.TeleportWithoutValue || o.Interactive && o.Arguments.Remote != nil) {
		f, e := m.Controller.BeginActivateFreshOwned(scope, sessioncontroller.FreshRemoteCLIProfile)
		if e != nil {
			return e
		}
		if _, e = f.Await(scope); e != nil {
			return e
		}
	} else if o.Arguments.Continue {
		if e = m.activateNativeContinue(scope); e != nil {
			return e
		}
	} else if o.PublicPrintLoadErrors && (o.Arguments.Resume != "" || o.Arguments.ResumeWithoutValue) {
		if e = m.hydrateNativeRemoteSession(scope); e != nil {
			return e
		}
		if e = m.activatePublicPrintResume(scope); e != nil {
			return e
		}
	} else if o.Arguments.Resume != "" {
		if e = m.hydrateNativeRemoteSession(scope); e != nil {
			return e
		}
		resume := resumestate.Input{Source: quoteMain(o.Arguments.Resume)}
		if m.nativeResumeIsURL {
			resume.Source = quoteMain(m.nativeResumeParsedSID)
		}
		if strings.HasSuffix(strings.ToLower(o.Arguments.Resume), ".jsonl") {
			resume.JSONLPath = o.Arguments.Resume
		}
		f, e := m.Controller.BeginActivate(scope, sessioncontroller.Activation{Resume: resume, ForkSession: o.Arguments.ForkSession, ResumeAt: o.Arguments.ResumeAt, RemoteEmptyStartup: m.nativeResumeIsURL})
		if e != nil {
			return e
		}
		if _, e = f.Await(scope); e != nil {
			return e
		}
	} else {
		f, e := m.Controller.BeginActivateFreshOwned(scope, sessioncontroller.FreshPrintProfile)
		if e != nil {
			return e
		}
		if _, e = f.Await(scope); e != nil {
			return e
		}
	}
	if e = m.restoreNativeFileCache(scope); e != nil {
		return e
	}
	if e = m.restoreNativeMainThreadAgent(scope); e != nil {
		return e
	}
	if e = m.initializeNativePaneTeamPermissions(scope); e != nil {
		return e
	}
	if e = m.Host.InitializeSessionInbox(scope); e != nil {
		return e
	}
	if _, e = m.Cleanup.Register(scope, runtimecleanup.NewCallback(m.Host.StartCleanup)); e != nil {
		return e
	}
	if d := m.options.DetachedAgent; d != nil && d.sourceMCP != nil {
		d.sourceMCP.tools = m.nativeTools
		d.sourceMCP.models, e = sdkhost.BorrowNativeMCPModels(scope, m.Host)
		if e != nil {
			return e
		}
	}
	if n.NativeProcess && o.Interactive {
		if e = m.initializeNativeModelCommandState(scope); e != nil {
			return e
		}
	}
	m.initialized = true
	if n.NativeProcess && o.Interactive {
		if e = m.beginNativeModelFastStatus(scope); e != nil {
			return e
		}
	}
	if e = m.startNativeSettingsWatcher(scope); e != nil {
		return e
	}
	if e := m.startNativeIDEIntegration(scope); e != nil {
		return e
	}
	if e := m.beginNativePluginInstallation(scope); e != nil {
		return e
	}
	return m.startNativeSkillWatcher()
}

// StartSession initializes a long-lived host without starting a model request.
func (m *MainPrint) StartSession(ctx context.Context) error {
	return m.Owner.Run(ctx, m.initializeOnOwner)
}

// RunTurn admits input through the existing SerialASK/Controller and model loop.
// Only the model context is cancellable; the owner and session survive a stop.
func (m *MainPrint) RunTurn(ctx context.Context, input protocol.JSONObject) error {
	if m.Controller.NativeRemoteFrontend() {
		return mainCap("remote frontend inputs must use their actual CCR session transport")
	}
	if !m.initialized || m.closed {
		return cooperative.ErrClosed
	}
	return m.Owner.Run(context.Background(), func(scope *cooperative.Scope) (err error) {
		if e := m.awaitNativePluginInstallation(scope, ctx); e != nil {
			return e
		}
		if m.nativeAutoConsent != nil {
			return mainCap("Auto mode consent is pending")
		}
		if err = m.markNativePaneActive(scope); err != nil {
			return err
		}
		if err = m.nativeRunState(scope, "running"); err != nil {
			return err
		}
		defer func() {
			if !m.shuttingDown {
				if idleErr := m.nativeRunState(scope, "idle"); err == nil {
					err = idleErr
				}
			}
		}()
		if e := m.Host.RunOne(scope, ctx, input); e != nil {
			return e
		}
		// REPL onQuery returns when this foreground query completes; detached
		// tasks remain in AppState for /tasks and the next prompt. A blocking
		// TaskOutput is already awaited inside RunOne. Headless and worker
		// execution retain their existing background completion drain.
		if m.options.Interactive && m.teamWorker == nil && !m.agentChild && m.options.AgentID == "" {
			return nil
		}
		return m.drainBackgroundTasksOnOwner(scope, ctx)
	})
}

func (m *MainPrint) ShutdownSession(ctx context.Context) error {
	return m.ShutdownSessionWithReason(ctx, "other")
}

func (m *MainPrint) ShutdownSessionWithReason(ctx context.Context, reason string) error {
	if m.remoteManaged != nil && m.remoteManaged.owner == m.Owner {
		m.remoteManaged.close()
	}
	m.nativeMigrations.close()
	m.BeginNativeAPIPreconnectClose()
	if err := m.joinNativeAPIPreconnect(ctx); err != nil {
		return err
	}
	m.BeginNativeRemoteAgentTasksClose()
	m.BeginNativeTerminalMCPAuthClose()
	m.BeginNativeComputerUseCleanupClose()
	m.BeginNativeLSPClose()
	m.BeginNativePromptClose()
	m.BeginNativeTerminalStatusLineClose()
	m.closeNativeAgentPreset()
	if m.closed {
		return nil
	}
	if err := m.joinNativeRemoteAgentTasks(ctx); err != nil {
		return err
	}
	m.closeNativePluginInstallation()
	m.nativePDFLife.close()
	m.nativeReadCountLife.close()
	m.BeginNativeRemoteRuntimeClose()
	m.BeginNativeTerminalIDEDiffClose()
	// Interactive MCP prefetch may still own a native reference when /exit
	// arrives. Cancel/join its actual transports before Host.Join waits for
	// reference deliveries, retaining the same borrowed namespace boundary.
	if m.options.Interactive && m.claudeAIMCP != nil && !m.claudeAIMCPBorrowed {
		m.claudeAIMCP.Close()
	}
	if m.options.Interactive && m.mcpRuntime != nil && !m.mcpBorrowed && (!m.agentChild || m.options.DetachedAgent != nil) {
		_ = m.mcpRuntime.Close(ctx)
	}
	if m.Host != nil {
		if e := m.Host.CloseAgentSummaries(ctx); e != nil {
			return e
		}
		if e := m.Host.CloseAutoDream(ctx); e != nil {
			return e
		}
		if e := m.Host.CloseSideQuestions(ctx); e != nil {
			return e
		}
	}
	if m.apiKeyHelper != nil && !m.apiKeyHelperBorrowed {
		_ = m.apiKeyHelper.Close(ctx)
	}
	m.settingsWatcher.Close()
	m.skillWatcher.close()
	m.ideIntegration.stop()
	if m.environmentWatcher != nil {
		m.environmentWatcher.Close()
	}
	if m.closed {
		return nil
	}
	var err error
	if m.initialized {
		err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			m.shuttingDown = true
			future, e := m.Cleanup.Start(scope)
			if e != nil {
				return e
			}
			_, e = future.Await(scope)
			if e != nil {
				return e
			}
			return m.nativeSessionEndOnOwner(scope, reason)
		})
		if e := m.Host.WaitTranscriptRuntime(ctx); err == nil {
			err = e
		}
		if e := m.Host.Join(ctx); err == nil {
			err = e
		}
	}
	if m.Git != nil {
		if e := m.Git.Close(ctx); err == nil {
			err = e
		}
	}
	for _, child := range m.agentHosts {
		if e := child.WaitTranscriptRuntime(ctx); err == nil {
			err = e
		}
		if e := child.Join(ctx); err == nil {
			err = e
		}
	}
	if m.watch != nil {
		m.watch.Join()
	}
	m.Close()
	return err
}

func (m *MainPrint) Run(ctx context.Context) (int, error) {
	if m.remoteManaged != nil {
		bound, release := m.remoteManaged.bindProcessContext(ctx)
		defer release()
		ctx = bound
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	m.nativeIngressLife.bindRun(cancel)
	defer m.nativeIngressLife.bindRun(nil)
	if m.options.Arguments.SDKURL != "" {
		if err := m.prepareNativeRemoteIO(ctx, cancel); err != nil {
			return 1, err
		}
		defer m.remoteIO.Close()
	}
	go m.drain()
	o := m.options
	e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if e := m.initializeOnOwner(scope); e != nil {
			return e
		}
		if e := m.awaitNativePluginInstallation(scope, ctx); e != nil {
			return e
		}
		r := bufio.NewReader(o.Input)
		initialLine, e := m.nativeMainAgentInitialSDKLine(scope)
		if e != nil {
			return e
		}

		stdinEOF := false
		var read sdkhost.GeneratorContinuation[string]
		read = func(s *cooperative.Scope, it *sdkhost.OwnerIterator[string], req sdkhost.IteratorRequest[string]) error {
			if req.Returning {
				return it.CompleteReturn(s)
			}
			if stdinEOF {
				return it.Complete(s)
			}
			consume := func(next *cooperative.Scope, chunk mainInputChunk) error {
				if chunk.eof {
					stdinEOF = true
				}
				if chunk.failure != nil && !chunk.eof {
					return chunk.failure
				}
				if len(chunk.bytes) == 0 {
					return it.Complete(next)
				}
				// Source prepends a filesystem agent's async initialPrompt only
				// when the actual SDK initialize producer has been consumed.
				// The original control line owns its ACK before the first ASK.
				var frame protocol.JSONObject
				request, control := protocol.JSONObject{}, false
				if frame.UnmarshalJSON(chunk.bytes) == nil && frame.String("type") == "control_request" {
					request, control = frame.Object("request")
				}
				if initialLine != "" && control && request.String("subtype") == "initialize" {
					line := initialLine
					initialLine = ""
					return it.Yield(next, string(chunk.bytes), func(s *cooperative.Scope, it *sdkhost.OwnerIterator[string], req sdkhost.IteratorRequest[string]) error {
						if req.Returning {
							return it.CompleteReturn(s)
						}
						return it.Yield(s, line, read)
					})
				}
				return it.Yield(next, string(chunk.bytes), read)
			}
			// StructuredIO parses the next complete line of an already-read
			// chunk inside its async generator. A buffered newline needs no
			// second native stdin I/O/poll callback and must stay on this Owner.
			if buffered := r.Buffered(); buffered > 0 {
				available, failure := r.Peek(buffered)
				if failure != nil {
					return failure
				}
				if bytes.IndexByte(available, '\n') >= 0 {
					line, failure := r.ReadBytes('\n')
					return consume(s, mainInputChunk{bytes: line, failure: failure})
				}
			}
			// Only a new physical read owns an asynchronous native Promise. It
			// unwinds through AwaitGenerator; no blocking I/O occurs on Owner.
			future, e := sdkhost.NewOwnerFuture[mainInputChunk](m.Owner)
			if e != nil {
				return e
			}
			go func() {
				line, readError := r.ReadBytes('\n')
				chunk := mainInputChunk{bytes: append([]byte(nil), line...), eof: errors.Is(readError, io.EOF), failure: readError}
				_, submitError := m.Owner.SubmitIndependent(context.Background(), cooperative.Input, func(next *cooperative.Scope) error { return future.Resolve(next, chunk) })
				if submitError != nil {
					_ = future.RejectAdministrative(submitError)
				}
			}()
			return sdkhost.AwaitGenerator(s, it, future, consume, nil)
		}
		raw, e := sdkhost.NewOwnerIterator(m.Owner, read)
		if e != nil {
			return e
		}
		structured, e := sdkhost.NewPlainStructuredInputWithDiagnostics(m.Owner, raw, m.diagnostics)
		if e != nil {
			return e
		}
		if m.remoteIO != nil {
			if e = structured.BindRemoteIO(scope, m.remoteIO); e != nil {
				return e
			}
		}
		initial, e := m.hook.TakeInitialUserMessage(scope)
		if e != nil {
			return e
		}
		if len(initial) > 0 {
			if e = structured.PrependUserMessage(scope, initial); e != nil {
				return e
			}
		}
		if e = m.Host.RunStream(scope, ctx, structured, sdkhost.StreamOptions{WrapASK: o.WrapSDKASK}); e != nil {
			return fmt.Errorf("public print SDK input/ASK consume: %w", e)
		}
		m.shuttingDown = true
		m.cancelNativePluginInstallation()
		if m.environmentWatcher != nil {
			future, failure := nativeSkillWork(m, scope, func() (struct{}, error) { m.environmentWatcher.Close(); return struct{}{}, nil })
			if failure != nil {
				return failure
			}
			if _, failure = future.Await(scope); failure != nil {
				return failure
			}
		}
		if m.settingsWatcher != nil {
			future, failure := nativeSkillWork(m, scope, func() (struct{}, error) { m.settingsWatcher.Close(); return struct{}{}, nil })
			if failure != nil {
				return failure
			}
			if _, failure = future.Await(scope); failure != nil {
				return failure
			}
		}
		f, e := m.Cleanup.Start(scope)
		if e != nil {
			return e
		}
		_, e = f.Await(scope)
		if e != nil {
			return e
		}
		return m.nativeSessionEndOnOwner(scope, "other")
	})
	m.closeNativePluginInstallation()
	close(m.finish)
	<-m.outputDone
	m.BeginNativeTerminalMCPAuthClose()
	m.BeginNativeLSPClose()
	m.BeginNativePromptClose()
	m.BeginNativeTerminalStatusLineClose()
	if m.Host != nil {
		// A returned SDK input failure does not cancel already dispatched native
		// transcript work. Let its original reactions dispatch their continuations
		// before administrative Join fences further Record worker admission.
		if j := m.Host.WaitTranscriptRuntime(ctx); e == nil {
			e = j
		}
		if j := m.Host.Join(ctx); e == nil {
			e = j
		}
	}
	if m.Git != nil {
		if j := m.Git.Close(ctx); e == nil {
			e = j
		}
	}
	for _, child := range m.agentHosts {
		if j := child.WaitTranscriptRuntime(ctx); e == nil {
			e = j
		}
		if j := child.Join(ctx); e == nil {
			e = j
		}
	}
	m.watch.Join()
	m.Close()
	if failure := m.nativeIngressLife.err(); failure != nil {
		return 1, failure
	}
	if e != nil && m.remoteIO != nil && remoteio.PermanentClose(m.remoteIO.CloseCode()) {
		return 0, nil
	}
	if e != nil {
		return 1, e
	}
	if m.outputError != nil {
		return 1, m.outputError
	}
	return m.Host.ExitCode(), nil
}
func (m *MainPrint) Close() {
	if m.removeRemoteManagedCacheListener != nil {
		m.removeRemoteManagedCacheListener()
		m.removeRemoteManagedCacheListener = nil
	}
	if m.remoteManaged != nil && m.remoteManaged.owner == m.Owner {
		m.remoteManaged.close()
	}
	m.nativeMigrations.close()
	m.closeNativeTerminalTaskList()
	m.BeginNativeAPIPreconnectClose()
	_ = m.joinNativeAPIPreconnect(context.Background())
	m.BeginNativeRemoteAgentTasksClose()
	_ = m.joinNativeRemoteAgentTasks(context.Background())
	m.BeginNativeTerminalMCPAuthClose()
	m.BeginNativeComputerUseCleanupClose()
	m.BeginNativeLSPClose()
	m.BeginNativePromptClose()
	m.BeginNativeTerminalStatusLineClose()
	m.closeNativeAgentPreset()
	m.closeNativePluginInstallation()
	m.joinNativeChromeInstallation()
	m.closeCLIStartupFiles()
	m.nativePDFLife.close()
	m.nativeReadCountLife.close()
	m.BeginNativeRemoteRuntimeClose()
	m.closeNativeModelFastStatus()
	if m.closed {
		return
	}
	if m.teamLeader != nil && m.teamLeader.permissionCancel != nil {
		m.teamLeader.permissionCancel()
	}
	if m.initialized && !m.shuttingDown && m.nativeTools != nil && m.options.DetachedAgent == nil {
		_ = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			future, e := m.nativeTools.StartBackgroundCleanup(scope)
			if e != nil {
				return e
			}
			_, e = future.Await(scope)
			return e
		})
	}
	m.closeNativeFileAnalytics()
	_ = m.closeNativeLSP(context.Background())
	m.CloseNativeTerminalStatusLine()
	_ = m.joinNativePrompt(context.Background())
	m.closeNativeErrorLog()
	m.skillWatcher.close()
	m.ideIntegration.stop()
	if m.environmentWatcher != nil {
		m.environmentWatcher.Close()
	}
	m.settingsWatcher.Close()
	if m.apiKeyHelper != nil && !m.apiKeyHelperBorrowed {
		_ = m.apiKeyHelper.Close(context.Background())
	}
	_ = m.closeNativeAsyncHooks(context.Background())
	m.closeNativeCloudCredentialCaches(context.Background())
	m.closeNativeComputerUse()
	_ = m.closeNativeSandbox(context.Background())
	_ = m.closeNativeStandaloneProxy(context.Background())
	if m.ownsTrace && m.options.Trace != nil {
		_ = m.options.Trace.Close(context.Background())
	}
	if m.mcpRuntime != nil && !m.mcpBorrowed && (!m.agentChild || m.options.DetachedAgent != nil) {
		_ = m.mcpRuntime.Close(context.Background())
	}
	if m.claudeAIMCP != nil && !m.claudeAIMCPBorrowed {
		m.claudeAIMCP.Close()
	}
	if m.nativeSettings != nil {
		_ = m.nativeSettings.Close(context.Background())
	} else {
		_ = m.Global.Close()
	}
	m.joinNativeCommandSources()
	m.Owner.Close()
	if pool := m.nativeHTTPPool(); pool != nil {
		_ = pool.Close()
	}
	if m.nativeIngress != nil {
		m.nativeIngress.Close()
	}
	m.closed = true
}

type mainInputChunk struct {
	bytes   []byte
	eof     bool
	failure error
}
