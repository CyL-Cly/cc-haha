// Package sdkhost connects a finite serial, bare, empty-tools SDK ASK to the
// unique queryloop/modelyield and real Controller/Writer. Active SDK controls,
// tools and nonempty discovery are capabilities, not placeholder services.
package sdkhost

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/attachments"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/cloudauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/diagnostics"
	"github.com/NanmiCoder/cc-haha/backend/internal/goalstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelyield"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/querypreflight"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkoutput"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionactivity"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolsearch"
	"github.com/NanmiCoder/cc-haha/backend/internal/tracecapture"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflowcommands"
	"net/http"
	"strings"
	"time"
)

type CapabilityError struct{ Boundary string }

func (e *CapabilityError) Error() string {
	return "SDK ASK host capability is not migrated: " + e.Boundary
}
func cap(s string) error { return &CapabilityError{s} }

// Enqueue is synchronous FIFO admission. It does not await a physical stdout
// write. Receipt is separate ASK-consumer control; nil means local headless
// Stream.enqueue resumed synchronously, as the actual Source print path does.
type Output struct {
	Enqueue func([]byte) error
	Receipt func([]byte) *cooperative.Yield
}
type Options struct {
	AuthStatus                    *cloudauth.AuthStatusManager
	EnableAuthStatus              bool
	SDKBetas                      []string
	NativeFastMode                func(*cooperative.Scope) (*NativeFastModeBinding, error)
	NativeValidationAuth          func(*cooperative.Scope) (*requestretry.NativeClaudeAuth, error)
	NativePermissionHookRetention func(*cooperative.Scope, context.CancelFunc) (bool, error)
	NativeSDKMCP                  *NativeSDKMCPState
	NativeSDKGetSettings          func(*cooperative.Scope) (protocol.JSONObject, error)
	NativeSDKMCPControl           func(*cooperative.Scope, protocol.JSONObject) (NativeSDKMCPControlResult, error)
	NativeSDKMCPStatus            func(*cooperative.Scope) (protocol.JSONObject, error)
	NativeSDKMCPMessage           func(*cooperative.Scope, string, protocol.JSONObject) error
	NativeSDKPrepareTurn          func(*cooperative.Scope) error
	NativeSDKEnvironmentUpdate    func(*cooperative.Scope, protocol.JSONObject) error
	NativeSDKInitializeOptions    func(*cooperative.Scope, protocol.JSONObject) error
	NativeSDKInferenceControl     func(*cooperative.Scope, protocol.JSONObject) error
	NativeSDKPermissionControl    func(*cooperative.Scope, protocol.JSONObject, func(*cooperative.Scope, protocol.JSONObject) error) error
	NativeSDKHookInitialize       func(*cooperative.Scope) (protocol.JSONObject, error)
	NativeSDKHookError            func(*cooperative.Scope, string, error) error
	NativeParentSettings          func(*cooperative.Scope) (NativeSettingsTransfer, error)
	OnQueryYield                  func(*cooperative.Scope, *queryloop.Yield) error
	AgentContext                  *modelaccounting.AgentContextBinding
	Print                         *PrintOptions
	Controller                    *sessioncontroller.Controller
	Owner                         *cooperative.Owner
	Factory                       *messages.Factory
	UUID                          func() string
	Now                           func() time.Time
	NowMillis                     func() float64
	Diagnostics                   *diagnostics.Process
	Activity                      *sessionactivity.Tracker
	RunState                      *RunStateOptions
	StopTask                      func(*cooperative.Scope, string) (protocol.JSONObject, error)
	SendAgentMessage              func(*cooperative.Scope, string, string) (protocol.JSONObject, error)
	Global                        *cliglobalconfig.Service
	Startup                       *clistartupenv.Service
	Settings                      *clisettings.Service
	SettingsState                 *clisettings.State
	Environment                   *clistartupenv.Environment
	Model, Version, CWD           string
	Workload                      string
	OriginalEnv                   map[string]string
	Runtime                       provider.SourceRuntime
	TLSState                      *tlsconfig.State
	NativeOpenAI                  func(*cooperative.Scope) *provider.OpenAICodexConfig
	NativeGrok                    func(*cooperative.Scope) *provider.GrokOAuthConfig
	NativeAPIKeySource            func(*cooperative.Scope) (string, error)
	NativeAuthHelper              func(*cooperative.Scope) (*requestretry.NativeAuthHelper, error)
	NativeClaudeAuth              func(*cooperative.Scope) (*requestretry.NativeClaudeAuth, error)
	NativeBedrock                 func(*cooperative.Scope) (*requestretry.NativeBedrock, error)
	NativeVertex                  func(*cooperative.Scope) (*requestretry.NativeVertex, error)
	Trace                         *tracecapture.Service
	Output                        Output
	OnPhase                       func(string)
	ObserveRequest                func(string, http.Header, json.RawMessage)
	ObserveModelInput             func(requestretry.Input)
	// UnhandledRecord receives only fire-and-forget Record rejections on the canonical owner.
	UnhandledRecord func(*cooperative.Scope, error)
	// PermissionRejections is borrowed from the same process/Owner when supplied.
	// This tracks settlements, not native unhandled-event admission.
	PermissionRejections *ownerasync.IgnoredRejections
}
type Host struct {
	nativePermissionPromptTool               *queryloop.RegisteredTool
	nativeAutoModeModels                     *cooperative.NativeAutoModeModels
	autoConsecutiveDenials, autoTotalDenials int
	internalFastSettings                     *protocol.JSONObject
	nativeModelValidationModels              *cooperative.NativeModelValidationModels
	nativeSideQuestionModels                 *cooperative.NativeSideQuestionModels
	nativeAutoDreamModels                    *cooperative.NativeAutoDreamModels
	nativeCompactModels                      *cooperative.NativeCompactModels
	nativeCompactFailures                    float64
	sideQuestions                            *nativeSideQuestions
	sideQuestionCandidate, sideQuestionCache *nativeSideQuestionCache
	sdkHooks                                 *nativeSDKHookState
	mcpModelSessionID                        string
	mcpModelPermission                       func(*cooperative.Scope) (*ownerasync.OwnerFuture[protocol.JSONObject], error)
	sandboxInputOpen                         bool
	sandboxPending                           map[string]*OwnerFuture[bool]
	nativePermissionView                     func(*cooperative.Scope) (protocol.JSONObject, error)
	terminalThinking                         *terminalThinkingState
	inputModelContext                        context.Context
	terminalBash                             *terminalBashInput
	toolSearchCatalogue                      *toolsearch.Catalogue
	askSkillOverrides                        *protocol.JSONObject
	askInferenceModel                        string
	askFallbackModel                         string
	permissionDenials                        []protocol.JSONObject
	definitions                              []toolschema.Definition
	registered                               []queryloop.RegisteredTool
	toolSchemas                              *toolschema.Session
	originalTools                            []protocol.JSONObject
	mcpServers                               []protocol.JSONObject
	options                                  Options
	serial                                   *sessioncontroller.SerialASK
	pending                                  *sessioncontroller.PendingWriter
	preflight                                *querypreflight.Service
	hooks                                    *queryhooks.Service
	normalizer                               *sdkoutput.Normalizer
	sessionAccounting                        *modelaccounting.Session
	permissionRejections                     *ownerasync.IgnoredRejections
	internalModelSettings                    *protocol.JSONObject
	internalOwnerSettings                    *protocol.JSONObject
	internalOwnerSettingsBase                *protocol.JSONObject
	internalOwnerSettingsSnapshot            []byte
	internalOwnerSettingsBaseSnapshot        []byte
	transferredASKSettings                   *protocol.JSONObject
	transferredASKMerged                     *protocol.JSONObject
	transferredASKProof                      *nativeSettingsProof
	agentContext                             *modelaccounting.AgentContextBinding
	agentRestartPromptConsumed               bool
	requestEnvironment                       map[string]string
	streamClock                              *modelyield.ProducerClock
	streamPolicy                             *modelyield.StreamErrorPolicy
	nativeAuxiliaryModels                    *cooperative.NativeAuxiliaryModels
	nativeMCPAuxiliaryModels                 *cooperative.NativeMCPAuxiliaryModels
	nativePromptHookModels                   *cooperative.NativePromptHookModels
	nativeAgentHookModels                    *cooperative.NativeAgentHookModels
	nativeInsightsModels                     *cooperative.NativeInsightsModels
	lastOutputType                           string
	lastOutputIsError                        bool
	closing                                  bool
	heldBackResult                           protocol.JSONObject
	sessionInbox                             bool
	agentQueryIterator                       *queryIteratorBridge
	askRunning                               bool
	emitIterator                             *OwnerIterator[[]byte]
	neutralSettings                          bool
	permissionMode                           string
	sdkPermissions                           *nativeSDKPermissionState
	nativeSDKPermissionAbort                 context.CancelFunc
	nativePermissionHooks                    *cooperative.NativePermissionRequestHooks
	nativePermissionHookSignal               context.Context
	nativePermissionHookContext              func(queryloop.Round) queryhooks.Context
	sdkMCPControlRefresh                     *OwnerFuture[struct{}]
	taskNotificationDispose                  func(*cooperative.Scope) error
	authStatusDispose                        func()
}

func New(options Options) (*Host, error) { return newHost(nil, options) }

// NewOwnedNeutral is a separate original-settings entry; old New remains the
// historical empty-settings component boundary. No alternate model consumer.
func NewOwnedNeutral(scope *cooperative.Scope, options Options) (*Host, error) {
	if !scope.BelongsTo(options.Owner) {
		return nil, cooperative.ErrRestricted
	}
	if !options.Startup.SharesSettings(options.Settings, options.SettingsState, options.Environment) || !options.Controller.UsesOriginalSettingsContext(options.Startup, options.Settings, options.SettingsState, options.Environment) {
		return nil, cap("shared settings construction identity")
	}
	if err := validateNeutralEnvironment(options.Environment, options.OriginalEnv); err != nil {
		return nil, err
	}
	return newHost(scope, options)
}
func newHost(scope *cooperative.Scope, options Options) (*Host, error) {
	if options.Controller != nil && !options.Controller.BootstrapOwns(options.Owner) {
		return nil, &CapabilityError{"bootstrap/controller owner mismatch"}
	}
	if options.Controller == nil || options.Owner == nil || options.Factory == nil || options.UUID == nil || options.Now == nil || options.NowMillis == nil || options.Global == nil || options.Startup == nil || options.Settings == nil || options.SettingsState == nil || options.TLSState == nil || options.Output.Enqueue == nil || options.OnPhase == nil || options.Version == "" || options.CWD == "" {
		return nil, errors.New("SDK ASK requires actual shared host services, clocks and FIFO")
	}
	if options.Print == nil && options.OriginalEnv["CLAUDE_CODE_SIMPLE"] != "1" {
		return nil, cap("non-bare original ASK lifecycle")
	}
	requestEnvironment, err := requestEnvironmentForProfile(options.OriginalEnv, scope != nil)
	if options.Print != nil {
		if options.Print.NativeProcess {
			requestEnvironment, err = NativePrintRequestEnvironment(options.OriginalEnv)
		} else {
			requestEnvironment, err = publicPrintRequestEnvironment(options.OriginalEnv)
		}
	}
	if err != nil {
		return nil, err
	}

	if options.OriginalEnv["CLAUDE_CODE_DIAGNOSTICS_FILE"] != "" && options.Diagnostics == nil {
		return nil, cap("diagnostics process sink unavailable")
	}
	if options.Activity != nil && (!options.Activity.Owns(options.Owner) || !options.Activity.UsesDiagnostics(options.Diagnostics)) {
		return nil, cap("shared process activity construction identity")
	}
	if options.RunState != nil && (options.Activity == nil || !options.Activity.UsesRegistry(options.RunState.Registry) || !options.RunState.Registry.Owns(options.Owner) || !options.RunState.Worker.Owns(options.Owner) || options.RunState.GetTasks == nil || options.RunState.IsShuttingDown == nil) {
		return nil, cap("shared process run-state registry/activity/worker")
	}
	if options.Diagnostics != nil && (!options.Global.UsesDiagnostics(options.Diagnostics) || !options.Startup.UsesDiagnostics(options.Diagnostics) || !options.Controller.UsesDiagnostics(options.Diagnostics)) {
		return nil, cap("diagnostics process construction identity")
	}
	streamPolicy, err := producerPolicyFromOriginalEnvironment(options.OriginalEnv, options.Print != nil && options.Print.NativeProcess)
	if err != nil {
		return nil, err
	}
	if err = options.Controller.ValidateSDKRecordProfile(); err != nil {
		return nil, err
	}
	merged, err := options.Startup.GetInitialSettings()
	if err != nil {
		return nil, err
	}
	if merged == nil || !merged.IsObject() {
		return nil, cap("original merged settings")
	}
	// This first host slice proves genuine empty discovery/config, not injected
	// pre-resolved commands, plugins, hooks, options or schema defaults.
	if scope == nil && len(merged.OwnProperties()) != 0 {
		return nil, cap("nonempty original settings/discovery")
	}
	if scope != nil {
		if options.Print != nil && options.Print.NativeProcess {
			err = sdkpermissions.ValidateNativeWithBindings(merged, options.Print.Registry.HasNativePluginBindings(), options.Print.NativeMCP != nil, options.Print.Tools != nil && options.Print.Tools.HasWebFetchBindings(), options.Print.Tools != nil && options.Print.Tools.HasWebSearchBindings(), options.Print.Permissions.NativeStartup.Owns(options.Owner), options.Print.Tools != nil && options.Print.Tools.HasWorktreeBindings(), options.Print.Hooks.HasNativeCommandBindings(options.Owner), options.Print.NativeSystemContext != nil, nativeTeamExecutionBound(scope, options), nativeAuthHelperBound(scope, options), nativeCloudSettingsBound(scope, options), nativeWorkflowKeywordBound(options), options.Print.Tools != nil && options.Print.Tools.HasInternalReadableBindings(), options.Print.Tools != nil && options.Print.Tools.HasSandboxBindings(), options.Print.NativeTypedModelSettings != nil && options.Print.NativeTypedModelSettings(scope), options.Print.NativeMainAgentSettings != nil && options.Print.NativeMainAgentSettings(scope), nativePlanSettingsBound(options), options.Print.NativeSpinnerVerb != nil, options.Print.NativeThinkingSummaryBeta != nil, options.Print.NativeAutoDream != nil && options.Print.CloseNativeAutoDream != nil)
		} else {
			err = sdkpermissions.ValidateNeutral(merged)
		}
		if err != nil {
			return nil, err
		}
		gate, e := workflowcommands.NewGate(options.Owner, options.Startup, options.Environment)
		if e != nil {
			return nil, e
		}
		permissionInput := sdkpermissions.Input{Profile: sdkpermissions.NeutralBareProfile, Platform: options.Runtime.Platform, Model: options.Model, NonInteractive: true, BaseToolsCLI: []string{""}, Mode: "default"}
		if options.Print != nil {
			permissionInput = options.Print.Permissions
			permissionInput.NativeDefaultCatalogBindings = options.Print.Tools != nil && options.Print.Tools.HasDefaultCatalogBindings()
			permissionInput.NativePluginBindings = options.Print.Registry.HasNativePluginBindings()
			permissionInput.NativeMCPBindings = options.Print.NativeMCP != nil
			permissionInput.NativePlanBindings = options.Print.Tools != nil && options.Print.Tools.HasPlanBindings()
			permissionInput.NativeWebFetchBindings = options.Print.Tools != nil && options.Print.Tools.HasWebFetchBindings()
			permissionInput.NativeWebSearchBindings = options.Print.Tools != nil && options.Print.Tools.HasWebSearchBindings()
			permissionInput.NativeSkillBindings = options.Print.Tools != nil && options.Print.Tools.HasSkillBindings()
			permissionInput.NativeMCPResourceBindings = options.Print.Tools != nil && options.Print.Tools.HasMCPResourceBindings()
			permissionInput.NativeToolSearchBindings = options.Print.Tools != nil && options.Print.Tools.HasToolSearchBindings() && options.Print.ToolSearch != nil
			permissionInput.NativeWorkflowBindings = options.Print.Tools != nil && options.Print.Tools.HasWorkflowBindings()
			permissionInput.NativeTeamToolBindings = options.Print.Tools != nil && options.Print.Tools.HasTeamBindings()
			permissionInput.NativeImageBindings = options.Print.Tools != nil && options.Print.Tools.HasImageBindings()
			if options.Print.Tools != nil {
				permissionInput.NativeImageEnabled, e = options.Print.Tools.ImageToolsEnabled(scope)
				if e != nil {
					return nil, e
				}
			}
			permissionInput.NativeTeamExecutionBindings = nativeTeamExecutionBound(scope, options)
			permissionInput.NativeSessionCollaborationBindings = options.Print.Tools != nil && options.Print.Tools.HasSessionCollaborationBindings()
			permissionInput.NativeWorktreeBindings = options.Print.Tools != nil && options.Print.Tools.HasWorktreeBindings()
			permissionInput.NativeBackgroundBindings = options.Print.Tools != nil && options.Print.Tools.HasBackgroundTaskBindings()
			permissionInput.NativeHookBindings = options.Print.Hooks.HasNativeCommandBindings(options.Owner)
			permissionInput.NativeSystemContextBindings = options.Print.NativeSystemContext != nil
			permissionInput.NativeAuthHelperBindings = nativeAuthHelperBound(scope, options)
			permissionInput.NativeCloudSettingsBindings = nativeCloudSettingsBound(scope, options)
			permissionInput.NativeWorkflowKeywordBindings = nativeWorkflowKeywordBound(options)
			permissionInput.NativeInternalReadBindings = options.Print.Tools != nil && options.Print.Tools.HasInternalReadableBindings()
			permissionInput.NativePlanBindings = nativePlanSettingsBound(options)
			permissionInput.NativeSpinnerBindings = options.Print.NativeSpinnerVerb != nil
			permissionInput.NativeAutoDreamBindings = options.Print.NativeAutoDream != nil && options.Print.CloseNativeAutoDream != nil
			permissionInput.NativeThinkingSummaryBindings = options.Print.NativeThinkingSummaryBeta != nil
			permissionInput.NativeModelCommandBindings = options.Print.NativeTypedModelSettings != nil && options.Print.NativeTypedModelSettings(scope)
			permissionInput.NativeMainAgentBindings = options.Print.NativeMainAgentSettings != nil && options.Print.NativeMainAgentSettings(scope)
			permissionInput.NativeSandboxBindings = options.Print.Tools != nil && options.Print.Tools.HasSandboxBindings()
		}
		permissions, e := sdkpermissions.Initialize(scope, options.Owner, merged, gate, permissionInput)
		if e != nil {
			return nil, e
		}
		if e = options.Controller.InitializeSDKSettings(scope, permissions); e != nil {
			return nil, e
		}
	}
	sessionAccounting, err := options.Controller.ModelAccountingFor(options.Owner)
	if err != nil {
		return nil, err
	}
	if options.Print != nil && options.Print.NativeProcess {
		err = sessionAccounting.ValidateNativeModel(scope, options.Model, options.OriginalEnv)
	} else {
		err = validateOriginalAccountingProfile(options.Model, options.OriginalEnv)
	}
	if err != nil {
		return nil, err
	}
	agentContext, err := originalAgentContext(options.Owner, options.AgentContext)
	if err != nil {
		return nil, err
	}
	if !options.Controller.UsesIndependentPersistence() {
		return nil, cap("independent transcript persistence selected before startup")
	}
	pending, err := options.Controller.Writer().PendingWriter(options.Owner)
	if err != nil {
		return nil, err
	}
	serial, err := sessioncontroller.NewSerialASK(options.Controller, options.Owner)
	if err != nil {
		return nil, err
	}
	preflight, err := querypreflight.New(querypreflight.Dependencies{Global: options.Global, InitialSettings: options.Startup, Factory: options.Factory})
	if err != nil {
		return nil, err
	}
	var hooks *queryhooks.Service
	if options.Print != nil {
		hooks = options.Print.Hooks
	} else {
		hooks, err = queryhooks.New(queryhooks.Options{Profile: queryhooks.EmptyExternalProfile, Settings: options.Settings, SettingsState: options.SettingsState, OriginalEnv: map[string]string{"CLAUDE_CODE_SIMPLE": "1"}, MainSessionID: options.Controller.SessionID(), PolicyAbsent: true, FlagAbsent: true, PluginBaseAbsent: true, UUID: options.UUID})
	}
	if err != nil {
		return nil, err
	}
	normalizer, err := sdkoutput.New(sdkoutput.Dependencies{Factory: options.Factory, SessionID: func() json.RawMessage { return quote(options.Controller.SessionID()) }, NowMillis: options.NowMillis, OriginalEnv: func(key string) string { return options.OriginalEnv[key] }})
	if err != nil {
		return nil, err
	}
	ignored := options.PermissionRejections
	if ignored == nil {
		ignored, err = ownerasync.NewIgnoredRejections(options.Owner)
		if err != nil {
			return nil, err
		}
	}
	if !ignored.Owns(options.Owner) {
		return nil, cooperative.ErrRestricted
	}
	options.SDKBetas = append([]string(nil), options.SDKBetas...)
	host := &Host{permissionRejections: ignored, options: options, serial: serial, pending: pending, preflight: preflight, hooks: hooks, normalizer: normalizer, sessionAccounting: sessionAccounting, agentContext: agentContext, requestEnvironment: requestEnvironment, streamClock: &modelyield.ProducerClock{NowMillis: options.NowMillis}, streamPolicy: streamPolicy, neutralSettings: scope != nil, permissionMode: "default"}
	if options.Print != nil && options.Print.NativeProcess && hooks.HasNativeAgentBindings(options.Owner) {
		host.nativeAgentHookModels, err = cooperative.NewNativeAgentHookModels(scope, options.Owner)
		if err != nil {
			return nil, err
		}
	}
	if options.Print != nil && options.Print.NativeProcess && options.Print.Tools != nil && (options.Print.Tools.HasWebFetchBindings() || options.Print.Tools.HasWebSearchBindings()) {
		host.nativeAuxiliaryModels, err = cooperative.NewNativeAuxiliaryModels(scope, options.Owner)
		if err != nil {
			return nil, err
		}
	}
	if options.Print != nil {
		if options.Print.NativeProcess && options.Print.PermissionPromptToolName == "stdio" {
			host.nativePermissionHooks, err = cooperative.NewNativePermissionRequestHooks(scope, options.Owner)
			if err != nil {
				return nil, err
			}
		}
		if options.Print.NativeProcess && hooks.HasNativePromptBindings(options.Owner) {
			host.nativePromptHookModels, err = cooperative.NewNativePromptHookModels(scope, options.Owner)
			if err != nil {
				return nil, err
			}
		}
		if options.Print.NativeProcess {
			host.nativeAutoDreamModels, err = cooperative.NewNativeAutoDreamModels(scope, options.Owner)
			if err != nil {
				return nil, err
			}
			host.nativeAutoModeModels, err = cooperative.NewNativeAutoModeModels(scope, options.Owner)
			if err != nil {
				return nil, err
			}
			host.nativeModelValidationModels, err = cooperative.NewNativeModelValidationModels(scope, options.Owner)
			if err != nil {
				return nil, err
			}
			host.sideQuestions = &nativeSideQuestions{tasks: map[string]*nativeSideQuestionTask{}}
			host.nativeSideQuestionModels, err = cooperative.NewNativeSideQuestionModels(scope, options.Owner)
			if err != nil {
				return nil, err
			}
			host.nativeCompactModels, err = cooperative.NewNativeCompactModels(scope, options.Owner)
			if err != nil {
				return nil, err
			}
			host.nativeInsightsModels, err = cooperative.NewNativeInsightsModels(scope, options.Owner)
			if err != nil {
				return nil, err
			}
		}
		host.toolSchemas = toolschema.NewSession()
		if err = host.initializePrintCatalog(scope); err != nil {
			return nil, err
		}
		if err = serial.InstallPrintInputProcessor(scope, sessioncontroller.InputProcessor{NativeMedia: options.Print.NativeProcess && options.Print.NativeInputMedia != nil, ProcessQueuedBatch: host.processInteractiveQueuedBatch, ProcessTerminalBash: host.processTerminalBash, Validate: host.validatePrintUserInput, Process: host.processPrintUserInput, ProcessQueued: host.processQueuedPrintInput}); err != nil {
			return nil, err
		}
	}
	if options.Print != nil && options.Print.NativeProcess && options.Print.AgentID == "" {
		host.taskNotificationDispose, err = options.Controller.BindTaskNotificationsOnOwner(scope, host.publishTaskNotificationOnOwner)
		if err != nil {
			return nil, err
		}
	}
	if options.EnableAuthStatus {
		if err := host.subscribeAuthStatus(); err != nil {
			return nil, err
		}
	}
	return host, nil
}
func (h *Host) enqueue(scope *cooperative.Scope, object protocol.JSONObject) (requestretry.NoticeAction, error) {
	if object.String("type") == "result" {
		held, e := h.holdNativeBackgroundResult(scope, object)
		if e != nil {
			return 0, e
		}
		if held {
			return requestretry.Resume, nil
		}
	}
	data, err := jsonBytes(object)
	if err != nil {
		return 0, err
	}
	return h.deliver(scope, data)
}
func (h *Host) deliver(scope *cooperative.Scope, data []byte) (requestretry.NoticeAction, error) {
	if h.emitIterator != nil {
		return h.yieldIterator(scope, data)
	}
	return h.deliverOutput(scope, data)
}
func (h *Host) deliverOutput(scope *cooperative.Scope, data []byte) (requestretry.NoticeAction, error) {
	h.options.OnPhase("FIFO:enqueue")
	if err := h.options.Output.Enqueue(append([]byte(nil), data...)); err != nil {
		return 0, err
	}
	h.lastOutputType = object(data).String("type")
	h.lastOutputIsError = string(object(data).Get("is_error")) == "true"
	if h.options.Output.Receipt != nil {
		receipt := h.options.Output.Receipt(append([]byte(nil), data...))
		if receipt == nil {
			return 0, errors.New("SDK consumer requires original receipt")
		}
		if err := scope.YieldReceipt(receipt); err != nil {
			if err == cooperative.ErrStop {
				return requestretry.Stop, nil
			}
			return 0, err
		}
	}
	return requestretry.Resume, nil
}
func (h *Host) initMessage() protocol.JSONObject {
	return h.initMessageForModel(h.options.Model)
}
func (h *Host) initMessageForModel(model string) protocol.JSONObject {
	// Empty arrays are the actual original --tools empty/bare discovery profile,
	// whose real settings resolver has been required and validated by New.
	o := protocol.NewJSONObject()
	fields := []protocol.JSONField{{Name: "type", Value: quote("system")}, {Name: "subtype", Value: quote("init")}, {Name: "cwd", Value: quote(h.options.CWD)}, {Name: "session_id", Value: quote(h.options.Controller.SessionID())}, {Name: "tools", Value: raw([]any{})}, {Name: "mcp_servers", Value: raw([]any{})}, {Name: "model", Value: quote(model)}, {Name: "permissionMode", Value: quote(h.permissionMode)}, {Name: "slash_commands", Value: raw([]any{})}, {Name: "apiKeySource", Value: quote("ANTHROPIC_API_KEY")}, {Name: "betas", Value: raw([]any{})}, {Name: "claude_code_version", Value: quote(h.options.Version)}, {Name: "output_style", Value: quote("default")}, {Name: "agents", Value: raw([]any{})}, {Name: "skills", Value: raw([]any{})}, {Name: "plugins", Value: raw([]any{})}, {Name: "uuid", Value: quote(h.options.UUID())}, {Name: "fast_mode_state", Value: quote("off")}}
	for _, f := range fields {
		o.Set(f.Name, f.Value)
	}
	if h.options.Print != nil {
		// Main keeps the filtered empty beta state undefined in JSON.
		if len(h.options.SDKBetas) > 0 {
			o.Set("betas", raw(h.options.SDKBetas))
		} else {
			o.Set("betas", nil)
		}
		names := []string{}
		for _, t := range h.registered {
			names = append(names, t.Name)
		}
		o.Set("tools", raw(names))
		if h.options.Print.Registry == nil {
			panic("normal print registry missing")
		}
		c := h.options.Print.Registry.Catalog()
		o.Set("agents", raw(c.Agents))
		o.Set("slash_commands", raw(c.Commands))
		o.Set("skills", c.skillNamesJSON())
		o.Set("plugins", raw(c.Plugins))
		if h.options.Print.NativeMCP != nil {
			o.Set("mcp_servers", raw(h.mcpServers))
		}
	}
	return o
}

func (h *Host) initMessageOnOwner(scope *cooperative.Scope, model string) (protocol.JSONObject, error) {
	if !scope.BelongsTo(h.options.Owner) {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	message := h.initMessageForModel(model)
	if h.options.NativeAPIKeySource != nil {
		source, err := h.options.NativeAPIKeySource(scope)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		message.Set("apiKeySource", quote(source))
	}
	return message, nil
}
func (h *Host) RunOne(scope *cooperative.Scope, modelContext context.Context, sdk protocol.JSONObject) (failure error) {
	if !scope.BelongsTo(h.options.Owner) || modelContext == nil {
		return cap("ASK owner/model context")
	}
	if h.closing {
		return cooperative.ErrClosed
	}
	endInputContext := h.beginInputContext(modelContext)
	initial, err := h.serial.Begin(scope, sdk)
	endInputContext()
	if err != nil {
		return err
	}
	if initial == nil {
		return cap("empty original serial admission")
	}
	if h.options.Print != nil && h.options.Print.PermissionPromptToolName != "" {
		turnContext, cancelModel := context.WithCancel(modelContext)
		previous := h.nativeSDKPermissionAbort
		h.nativeSDKPermissionAbort = cancelModel
		defer func() {
			retain := false
			if failure == nil && turnContext.Err() == nil {
				var err error
				retain, err = h.retainNativePermissionHookSignal(scope, cancelModel)
				if failure == nil {
					failure = err
				}
			}
			if !retain {
				cancelModel()
			}
			h.nativeSDKPermissionAbort = previous
		}()
		return h.runAccepted(scope, turnContext, initial)
	}
	return h.runAccepted(scope, modelContext, initial)
}

// AdmitParsedSDK belongs to the session input task and may finish while the
// current ASK iterator is suspended. It never starts another model consumer.
func (h *Host) AdmitParsedSDK(scope *cooperative.Scope, input protocol.JSONObject) (*sessioncontroller.ParsedSDKFuture, error) {
	if h.closing {
		return nil, cooperative.ErrClosed
	}
	return h.serial.AdmitParsedSDK(scope, input)
}

// RunDequeued is the sole model stack for already admitted session input.
// Empty dequeue does not enter ASK or perform its finally/accounting.
func (h *Host) RunDequeued(scope *cooperative.Scope, modelContext context.Context) (bool, error) {
	if !scope.BelongsTo(h.options.Owner) || modelContext == nil {
		return false, cap("ASK owner/model context")
	}
	if h.closing {
		return false, cooperative.ErrClosed
	}
	endInputContext := h.beginInputContext(modelContext)
	initial, err := h.serial.BeginDequeued(scope)
	endInputContext()
	if err != nil || initial == nil {
		return false, err
	}
	return true, h.runAccepted(scope, modelContext, initial)
}

func (h *Host) runAccepted(scope *cooperative.Scope, modelContext context.Context, initial *protocol.JSONObject) (failure error) {
	h.permissionDenials = nil
	previousHookSignal := h.nativePermissionHookSignal
	h.nativePermissionHookSignal = modelContext
	defer func() { h.nativePermissionHookSignal = previousHookSignal }()

	computerUseCleaned := false
	cleanupComputerUse := func(_ context.Context) error {
		if h.isAgent() || h.options.Print == nil || h.options.Print.NativeMCP == nil || !h.options.Print.NativeMCP.HasComputerUse() {
			return nil
		}
		computerUseCleaned = true
		return h.options.Print.NativeMCP.EndTurnOwner(scope)
	}
	defer func() {
		if !computerUseCleaned {
			_ = cleanupComputerUse(context.Background())
		}
	}()
	// Source run() awaits updateSdkMcp after initialize has enqueued its ACK.
	// Connecting here lets the input task deliver handshake responses without
	// blocking initialize, and refreshes the tools before the ASK catalogue.
	if h.options.NativeSDKPrepareTurn != nil {
		if err := h.options.NativeSDKPrepareTurn(scope); err != nil {
			return err
		}
	}
	persistSession := true
	if h.options.Print != nil && h.options.Print.NativeProcess {
		var e error
		persistSession, e = h.options.Controller.PersistsOnOwner(scope)
		if e != nil {
			return e
		}
	}
	inference := h.captureInferenceSettings()
	if prompt := h.serial.PromptInputResult(); prompt != nil {
		if prompt.Model != "" {
			inference.model = prompt.Model
		}
		if prompt.Effort != nil {
			inference.effort = append(json.RawMessage(nil), prompt.Effort...)
			inference.effortOverridesEnv = true
		}
	}
	h.askSkillOverrides = nil
	if prompt := h.serial.PromptInputResult(); prompt != nil && len(prompt.CommandRules) > 0 {
		overrides := protocol.NewJSONObject(protocol.JSONField{Name: "commandRules", Value: raw(prompt.CommandRules)})
		h.askSkillOverrides = &overrides
	}
	defer func() { h.askSkillOverrides = nil }()
	h.askInferenceModel = inference.model
	h.askFallbackModel = ""
	defer func() { h.askInferenceModel, h.askFallbackModel = "", "" }()
	h.askRunning = true
	defer func() { h.askRunning = false }()
	if h.neutralSettings {
		settings, permissions, e := h.readASKSettings(scope)
		if e != nil {
			return e
		}
		merged, e := h.readASKMergedSettings()
		if e != nil {
			return e
		}
		if e = h.validateASKSettingsReference(settings, merged, scope); e != nil {
			return e
		}
		h.permissionMode = permissions.String("mode")
	}

	defer func() {
		if h.emitIterator == nil {
			err := h.serial.Finally(scope)
			if failure == nil {
				failure = err
			}
		}
	}()
	if local := h.serial.LocalInputResult(); local != nil {
		return h.runLocalInput(scope, local)
	}
	if prompt := h.serial.PromptInputResult(); prompt != nil {
		goalCommand := len(prompt.Messages) > 0 && strings.Contains(prompt.Messages[0].String("content"), "<command-name>/goal</command-name>")
		for _, record := range prompt.Messages {
			if err := h.emitNativeGoalOutput(scope, record, goalCommand); err != nil {
				return err
			}
		}
	}
	if err := h.prepareNativeTeamWorker(scope, modelContext); err != nil {
		return err
	}
	if prompt := h.serial.PromptInputResult(); prompt != nil && prompt.NoQuery {
		if observer := h.options.Print.OnLocalInputMessages; observer != nil {
			messages := make([]protocol.JSONObject, len(prompt.Messages))
			for index, message := range prompt.Messages {
				messages[index] = message.Clone()
			}
			if err := observer(messages); err != nil {
				return err
			}
		}
		return h.runLocalOutput(scope, &sessioncontroller.LocalInputResult{Text: prompt.NoQueryResultText, StartMillis: prompt.StartMillis}, false)
	}
	if h.options.Print != nil && (h.options.Print.InputAttachments != nil || h.options.Print.PlanAttachments != nil) && !(h.serial.PromptInputResult() != nil && h.serial.PromptInputResult().AttachmentsHandled) {
		history := h.serial.History()
		// processUserInput passes the previous conversation to the attachment
		// producer, then appends the incoming user followed by its attachments.
		previous := history
		inputCount := 1
		if prompt := h.serial.PromptInputResult(); prompt != nil {
			inputCount = len(prompt.Messages)
		}
		if len(previous) >= inputCount {
			previous = previous[:len(previous)-inputCount]
		}
		var attached []queryloop.Message
		var e error
		if h.options.Print.InputAttachments != nil {
			text, present, failure := originalInputAttachmentText(initial)
			if failure != nil {
				return failure
			}
			if present {
				attached, e = h.options.Print.InputAttachments(modelContext, scope, text, previous)
			}
		} else {
			attached, e = h.options.Print.PlanAttachments(scope, previous)
		}
		if e != nil {
			return e
		}
		for _, message := range attached {
			if message.Type() != "attachment" {
				return cap("initial plan attachment producer")
			}
			if e := h.serial.Accept(scope, message); e != nil {
				return e
			}
		}
	}
	pendingSessionInput := false
	if h.sessionInbox {
		var e error
		pendingSessionInput, e = h.options.Controller.SessionMessagePendingOnOwner(scope, initial.String("uuid"))
		if e != nil {
			return e
		}
	}
	if persistSession {
		initialRecord, err := h.serial.BeginRecord(scope, h.serial.History())
		if err != nil {
			return err
		}
		h.options.OnPhase("Record:returned")
		if h.isAgent() || pendingSessionInput {
			if _, err = initialRecord.Await(scope); err != nil {
				return err
			}
			h.options.OnPhase("Record:fulfilled")
			if pendingSessionInput {
				flush, e := h.pending.Flush(scope)
				if e != nil {
					return e
				}
				if e = flush.Await(scope); e != nil {
					return e
				}
			}
		} else {
			h.observeRecord(scope, initialRecord)
		}
	}

	if pendingSessionInput {
		if e := h.options.Controller.ConsumeSessionMessageOnOwner(scope, initial.String("uuid")); e != nil {
			return e
		}
	}

	start := h.options.NowMillis()
	if !h.isAgent() {
		message, e := h.initMessageOnOwner(scope, inference.model)
		if e != nil {
			return e
		}
		action, e := h.enqueue(scope, message)
		if e != nil {
			return e
		}
		if action == requestretry.Stop {
			return nil
		}
	}
	if err := h.captureToolSearchCatalogue(scope); err != nil {
		return err
	}
	contextInput := requestcontext.Context{Tools: h.originalTools, Thinking: requestcontext.Thinking{Type: "disabled"}, Options: requestcontext.Options{Model: inference.model}, Host: requestcontext.Host{Profile: requestcontext.DirectRelayProfile, Env: h.requestEnvironment, SDKBetas: h.options.SDKBetas, TTLAllowlist: []string{}}}
	deviceID, err := h.requestDeviceID(scope)
	if err != nil {
		return err
	}
	contextInput.Host.DeviceID = deviceID
	contextInput.Host.Workload = h.options.Workload
	contextInput.Host.NativeThinkingSummaryBeta, err = h.nativeThinkingSummaryBeta(scope, inference.model)
	if err != nil {
		return err
	}
	if h.options.Print != nil {
		contextInput.Options.EffortValue = inference.effort
		contextInput.Options.HasAppendSystemPrompt = h.options.Print.AppendSystemPrompt != ""
		contextInput.Options.EffortValueOverridesEnv = inference.effortOverridesEnv
		if h.options.Print.NativeProcess {
			contextInput.Host.Profile = requestcontext.NativeDirectRelayProfile
			if h.options.NativeGrok != nil {
				contextInput.Host.NativeGrokOAuth = h.options.NativeGrok(scope)
			}
			if h.options.NativeOpenAI != nil {
				binding := h.options.NativeOpenAI(scope)
				if binding != nil {
					binding.TurnState = providerproxy.NewOpenAICodexTurnState(modelContext)
					defer binding.TurnState.Dispose()
					contextInput.Host.NativeCodexOAuth = binding
				}
			}
			definitions := append([]toolschema.Definition{}, h.definitions...)
			contextInput.Host.NativeToolDefinitions = &definitions
			contextInput.Host.NativeToolSearch = h.options.Print.ToolSearch
			contextInput.Host.ToolSearchCatalogue = h.toolSearchCatalogue
			contextInput.Thinking = h.options.Print.Thinking
			if contextInput.Thinking.Type == "" {
				contextInput.Thinking.Type = "adaptive"
			}
		}
		contextInput.Host.ToolSchemas = h.toolSchemas
		contextInput.Host.ToolSchemaHost = &toolschema.Host{BaseURL: h.options.OriginalEnv["ANTHROPIC_BASE_URL"], MainLoopModel: inference.model}
		if h.options.Print.Tools != nil {
			contextInput.Host.ToolSchemaHost, err = h.options.Print.Tools.SchemaHostForScope(scope, inference.model, h.options.OriginalEnv["ANTHROPIC_BASE_URL"])
			if err != nil {
				return err
			}
		}
	}
	if h.options.Print != nil && h.options.Print.PlanProjection != nil {
		contextInput.Host.PlanOptions, err = h.options.Print.PlanProjection(scope)
		if err != nil {
			return err
		}
	}
	resolved, err := h.serial.Raw(contextInput)
	if err != nil {
		return err
	}
	systemContext := protocol.NewJSONObject()
	if h.options.Print != nil && h.options.Print.NativeSystemContext != nil {
		systemContext, err = h.options.Print.NativeSystemContext(scope)
		if err != nil {
			return err
		}
	}
	if h.options.Print != nil && h.options.Print.AppendSystemPrompt != "" {
		resolved.Context.SystemPrompt = append(append([]json.RawMessage{}, resolved.Context.SystemPrompt...), quote(h.options.Print.AppendSystemPrompt))
	}
	var checkpointMessageID json.RawMessage
	if h.options.Print != nil && h.options.Print.FileHistory != nil && !h.isAgent() {
		persist, e := h.options.Controller.PersistsOnOwner(scope)
		if e != nil {
			return e
		}
		if persist {
			history := h.serial.History()
			for i := len(history) - 1; i >= 0; i-- {
				if history[i].Type() == "user" && string(history[i].Record.Get("isMeta")) != "true" {
					checkpointMessageID = history[i].Record.Get("uuid")
					break
				}
			}
			if len(checkpointMessageID) > 0 {
				if e := h.options.Print.FileHistory.Make(scope, checkpointMessageID); e != nil {
					return e
				}
			}
		}
	}
	checkpointDone := false
	checkpoint := func() error {
		if checkpointDone {
			// Actual completeCheckpoint is async even after its once flag.
			// Its explicit callers still await that resolved return Promise.
			h.options.OnPhase("checkpoint:already-complete")
			return scope.AwaitFulfilled()
		}
		checkpointDone = true
		return h.completeCheckpointForMessage(scope, checkpointMessageID)
	}
	defer func() {
		// Lifecycle fallback is not another Source call site after completion.
		if !checkpointDone {
			if e := checkpoint(); failure == nil {
				failure = e
			}
		}
	}()
	initialHistory := h.serial.History()
	schemaTurn, err := h.beginNativeJSONSchemaTurn(scope, initialHistory)
	if err != nil {
		return err
	}
	askHistory := append([]queryloop.Message(nil), initialHistory...)
	current, total := zeroUsage(), zeroUsage()
	lastStop := json.RawMessage("null")
	// Inbox idle input is a Source isMeta channel message, excluded from
	// replayableMessages even when replayUserMessages is enabled.
	replayUserMessages, err := h.options.Controller.ReplayUserMessagesOnOwner(scope)
	if err != nil {
		return err
	}
	acknowledged := pendingSessionInput || !replayUserMessages
	turnCount := 1
	currentMessageTtftMillis := float64(0)
	preflightEnv := map[string]string{}
	for key, value := range h.requestEnvironment {
		if key != "CLAUDE_CODE_SIMPLE" && (h.options.Print == nil || !h.options.Print.NativeProcess || nativePreflightEnvironment(key)) {
			preflightEnv[key] = value
		}
	}
	if h.options.Print != nil && h.options.Print.NativeProcess {
		preflightEnv = map[string]string{}
		for name, value := range h.options.OriginalEnv {
			if nativePreflightEnvironment(name) {
				preflightEnv[name] = value
			}
		}
	}
	var decision querypreflight.Result
	taskBudget := h.newTaskBudgetTracker()
	stopHookActive := false
	hookContext := func(round queryloop.Round, assistants []*streamhistory.Envelope) queryhooks.Context {
		history, _ := round.ToolContext.([]queryloop.Message)
		toolContext := round.ToolContext
		if h.options.Print != nil && h.options.Print.NativeProcess {
			model, _ := h.skillMainModel(inference.model)
			toolContext = &builtinwrite.CallContext{Scope: scope, AgentID: h.agentID(), MainLoopModel: model, SkillOverrides: h.askSkillOverrides, Messages: history, ToolSearchCatalogue: h.toolSearchCatalogue, PermissionContext: h.NativePermissionContext}
		}
		var teammateStop func(context.Context, queryhooks.Context, queryloop.Emit) (queryloop.StopDecision, error)
		if h.options.Print != nil {
			teammateStop = h.options.Print.NativeTeammateStop
		}
		return queryhooks.Context{Scope: scope, StartAutoDream: h.startNativeAutoDream(scope), StopHookActive: stopHookActive, History: history, Assistants: assistants, SystemPrompt: decision.SystemPrompt, UserContext: map[string]string{"currentDate": resolved.UserContext.String("currentDate")}, SystemContext: systemContextStrings(systemContext), ToolUseContext: toolContext, QuerySource: h.querySource(), AgentID: h.subagentHookID(), ComputerUseInactive: h.options.Print == nil || h.options.Print.NativeMCP == nil || !h.options.Print.NativeMCP.HasComputerUse(), CleanupComputerUse: cleanupComputerUse, GoalInactive: true, TeammateInactive: teammateStop == nil, TeammateStop: teammateStop}
	}
	loopHost := queryloop.Host{Activity: h.options.Activity,
		ModelDisplayName: func(model string) (string, error) {
			if h.options.Print != nil && h.options.Print.ModelDisplayName != nil {
				return h.options.Print.ModelDisplayName(model)
			}
			return model, nil
		},
		ModelFallbackChanged: func(_ context.Context, model string) error {
			h.askFallbackModel, h.askInferenceModel, decision.CurrentModel = model, model, model
			return nil
		},
		Session: h.sessionAccounting, AgentContext: h.agentContext,
		StreamClock: h.streamClock, StreamPolicy: h.streamPolicy,
		SuccessDuration: &modelaccounting.SuccessDuration{NowMillis: h.options.NowMillis, GetPermission: h.options.Controller.SuccessPermissionOnOwner, Ignored: h.permissionRejections},
		ResolvePreflight: func(ctx context.Context, round queryloop.Round) (queryloop.PreflightResult, error) {
			var e error
			profile := ""
			if h.options.Print != nil && h.options.Print.NativeProcess {
				profile = querypreflight.NativeProcessProfile
			}
			currentMainModel, e := h.skillMainModel(inference.model)
			if e != nil {
				return queryloop.PreflightResult{}, e
			}
			input := querypreflight.Input{SDKBetas: h.options.SDKBetas, NativeClaudeProof: h.nativeClaudeProof(scope), PlanOptions: contextInput.Host.PlanOptions, Profile: profile, History: round.History, SystemPrompt: resolved.Context.SystemPrompt, UserContext: resolved.UserContext, SystemContext: systemContext, MainLoopModel: currentMainModel, QuerySource: h.querySource(), PermissionMode: h.permissionMode, OriginalEnvironment: preflightEnv, ToolCount: len(h.definitions), Tools: h.definitions}
			if h.options.Print != nil && h.options.Print.NativeProcess {
				input.ConsecutiveFailures = &h.nativeCompactFailures
				input.AutoCompact = func(ctx context.Context, history []queryloop.Message) ([]queryloop.Message, error) {
					result, e := h.compactConversationOnOwner(ctx, scope, history, "", "", 0, true)
					if e != nil {
						return nil, e
					}
					if e = taskBudget.Compacted(history); e != nil {
						return nil, e
					}
					// Publish the persisted canonical boundary and summary to
					// CLI, desktop and Web consumers before the resumed model.
					for _, m := range result.History {
						if e = h.emitCompactOutput(scope, m); e != nil {
							return nil, e
						}
					}
					if e = h.serial.Record(scope, h.serial.History()); e != nil {
						return nil, e
					}
					return h.serial.History(), nil
				}
			}
			if h.options.Print != nil && h.options.Print.ResolveRuntimeModel != nil {
				input.CurrentPermissionMode = func() (string, error) {
					_, permission, err := h.options.Controller.SDKSettingsOnOwner(scope)
					return permission.String("mode"), err
				}
				input.ResolveRuntimeModel = func(input querypreflight.RuntimeModelInput) (string, error) {
					return h.options.Print.ResolveRuntimeModel(scope, input)
				}
			}
			decision, e = h.preflight.Run(ctx, input)
			result := queryloop.PreflightResult{History: decision.ModelHistory, ToolContext: decision.ToolContextHistory}
			if h.options.Print != nil && h.options.Print.NativeProcess && h.options.Print.ResolveRuntimeModel != nil {
				result.CurrentModel = decision.CurrentModel
			}
			if decision.Blocked != nil {
				result.Terminal = &queryloop.PreflightTerminal{Message: *decision.Blocked, Reason: "blocking_limit"}
			}
			return result, e
		},
		ResolveModelInput: func(_ context.Context, round queryloop.Round) (requestretry.Input, error) {
			history := []protocol.JSONObject{}
			for _, message := range round.History {
				bytes, e := message.MarshalJSON()
				if e != nil {
					return requestretry.Input{}, e
				}
				var record protocol.JSONObject
				if e = record.UnmarshalJSON(bytes); e != nil {
					return requestretry.Input{}, e
				}
				history = append(history, record)
			}
			input, e := h.serial.Resolve(context.Background(), resolved, history)
			if e != nil {
				return requestretry.Input{}, e
			}
			input = h.filterDynamicAgentTools(input)
			input.Options.Model = decision.CurrentModel
			input.Options.TaskBudget = taskBudget.RequestBudget()
			if h.askSkillOverrides != nil && h.askSkillOverrides.Get("effort") != nil {
				input.Options.EffortValue = append(json.RawMessage(nil), h.askSkillOverrides.Get("effort")...)
				input.Options.EffortValueOverridesEnv = true
			}
			input.Options.QuerySource = h.querySource()
			input.SystemPrompt = decision.SystemPrompt
			binding, e := h.nativeFastModeBinding(scope)
			if e != nil {
				return requestretry.Input{}, e
			}
			var proof *cooperative.NativeFastModeRequest
			if binding != nil {
				proof = binding.Proof
			}
			input.Host.NativeFastModeProof = proof
			fast, e := h.options.Controller.ResolvedFastModeOnOwner(scope, proof)
			if e != nil {
				return requestretry.Input{}, e
			}
			original := requestretry.Input{Context: input, Runtime: h.options.Runtime, ResolvedFastMode: fast}
			h.recordUserContextSnapshot(scope, resolved.UserContext)
			h.rememberSideQuestionCandidate(original, resolved, systemContext, round.Tracking)
			if h.options.ObserveModelInput != nil {
				h.options.ObserveModelInput(original)
			}
			return original, nil
		},
		LookupBackfill: func(string) (queryloop.Backfill, error) { return nil, cap("active tool backfill") }, ExecuteBatch: func(context.Context, queryloop.Round, []queryloop.Invocation, func(queryloop.Message) error) (any, error) {
			return nil, cap("active tool execution")
		}, NormalizeToolResult: func(queryloop.Message) ([]queryloop.Message, error) { return nil, cap("active tool result") }, PauseAfterTools: func(context.Context, queryloop.Round) (bool, error) { return false, cap("active tool pause") },
		PostSampling: func(round queryloop.Round, a []*streamhistory.Envelope) error {
			_, e := h.hooks.StartPostSampling(hookContext(round, a))
			return e
		}, StopFailure: func(round queryloop.Round, a *streamhistory.Envelope) error {
			_, e := h.hooks.StartStopFailure(hookContext(round, nil), a)
			return e
		}, AbortCleanup: h.abortCleanupHandler(func(round queryloop.Round) queryhooks.Context { return hookContext(round, nil) }), StopHooks: func(ctx context.Context, round queryloop.Round, a []*streamhistory.Envelope, emit queryloop.Emit) (queryloop.StopDecision, error) {
			h.saveSideQuestionCache()
			result, e := h.hooks.StopHooks(ctx, hookContext(round, a), emit)
			if e == nil {
				e = h.nativeJSONSchemaStop(scope, round, a, &result, emit)
			}
			if e == nil && !result.PreventContinuation && len(result.BlockingMessages) > 0 {
				stopHookActive = true
			}
			return result, e
		},
	}
	loopHost.Request = h.requestDependencies(scope)
	if h.options.Print != nil {
		if err = h.installPrintBatch(scope, &loopHost, inference.model, func(round queryloop.Round) queryhooks.Context { return hookContext(round, nil) }); err != nil {
			return err
		}
	}

	askConsumerWithoutBudget := func(value queryloop.Yield) (requestretry.NoticeAction, error) {
		if value.RequestStart {
			return requestretry.Resume, nil
		}
		if h.options.OnQueryYield != nil {
			if e := h.options.OnQueryYield(scope, &value); e != nil {
				return 0, e
			}
		}
		if value.Stream != nil {
			event := object(value.Stream.Event)
			switch event.String("type") {
			case "message_start":
				currentMessageTtftMillis = float64(value.Stream.Timing.TTFTMillis)
				current = zeroUsage()
				current, err = updateUsage(current, object(object(event.Get("message")).Get("usage")))
			case "message_delta":
				current, err = updateUsage(current, object(event.Get("usage")))
				if v := object(event.Get("delta")).Get("stop_reason"); v != nil && string(v) != "null" {
					lastStop = v
				}
			case "message_stop":
				total = accumulateUsage(total, current)
				err = h.sessionAccounting.AddGenerationDuration(scope, float64(value.Stream.Timing.DecodeMillis), currentMessageTtftMillis)
			}
			if err != nil {
				return 0, err
			}
			if h.isAgent() {
				return requestretry.Resume, nil
			}
			o := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("stream_event")}, protocol.JSONField{Name: "event", Value: value.Stream.Event}, protocol.JSONField{Name: "session_id", Value: quote(h.options.Controller.SessionID())}, protocol.JSONField{Name: "parent_tool_use_id", Value: raw(nil)}, protocol.JSONField{Name: "uuid", Value: quote(h.options.UUID())})
			return h.enqueue(scope, o)
		}
		if value.Message == nil {
			return 0, errors.New("ASK received no original yield")
		}
		message := *value.Message
		if value.ModelFallbackWarning {
			if !messages.IsNativeModelFallbackRecord(message.Record) {
				return 0, cap("original model fallback warning")
			}
			return requestretry.Resume, h.serial.Accept(scope, message)
		}
		if err := h.observeNativeJSONSchemaYield(scope, schemaTurn, message); err != nil {
			return 0, err
		}
		if message.Type() == "system" && message.Record.String("subtype") == "streaming_fallback" {
			return h.nativeStreamRecovery(scope, message)
		}
		if h.options.Print != nil && h.options.Print.NativeProcess && message.Type() == "system" && goalstate.IsLocalOutput(message.Record.String("content")) {
			if e := h.serial.Accept(scope, message); e != nil {
				return 0, e
			}
			askHistory = append(askHistory, message)
			if persistSession {
				future, e := h.serial.BeginRecord(scope, askHistory)
				if e != nil {
					return 0, e
				}
				h.options.OnPhase("Record:returned")
				h.observeRecord(scope, future)
			}
			if e := h.emitNativeGoalOutput(scope, message.Record); e != nil {
				return 0, e
			}
			return requestretry.Resume, nil
		}
		if message.Type() == "attachment" && object(message.Record.Get("attachment")).String("type") == "max_turns_reached" {
			// Source handles attachment admission separately from assistant/user
			// Record/ACK. The detached Record remains on the same Owner/Writer.
			if e := h.serial.Accept(scope, message); e != nil {
				return 0, e
			}
			askHistory = append(askHistory, message)
			if persistSession {
				future, e := h.serial.BeginRecord(scope, askHistory)
				if e != nil {
					return 0, e
				}
				h.options.OnPhase("Record:returned")
				if h.isAgent() {
					if _, e = future.Await(scope); e != nil {
						return 0, e
					}
					h.options.OnPhase("Record:fulfilled")
				} else {
					h.observeRecord(scope, future)
				}
			}
			if h.isAgent() {
				return requestretry.Stop, nil
			}
			// Eager/cowork flags are outside this host's admitted environment;
			// the ordinary original caller proceeds directly to checkpoint.
			if e := checkpoint(); e != nil {
				return 0, e
			}
			result, e := h.maxTurnsResultOnOwner(scope, message, total, lastStop, start)
			if e != nil {
				return 0, e
			}
			if _, e = h.enqueue(scope, result); e != nil {
				return 0, e
			}
			// QueryEngine returns after this result. Return is sent to the same
			// suspended query/fileHistory iterators, invoking their real finally.
			return requestretry.Stop, nil
		}
		if message.Type() == "attachment" && object(message.Record.Get("attachment")).String("type") == "queued_command" && h.options.Print != nil && h.options.Print.NativeProcess {
			return h.acceptQueuedCommand(scope, message, &askHistory, persistSession)
		}
		if message.Type() == "attachment" && (attachments.Ultracode(message.Record.Get("attachment")) || retainedPlanAttachment(message.Record.Get("attachment")) || retainedSkillAttachment(message.Record.Get("attachment")) || h.options.Print != nil && h.options.Print.NativeProcess && (object(message.Record.Get("attachment")).String("type") == "teammate_mailbox" || object(message.Record.Get("attachment")).String("type") == "hook_stopped_continuation" || object(message.Record.Get("attachment")).String("type") == "hook_blocking_error" || object(message.Record.Get("attachment")).String("type") == "hook_additional_context" || object(message.Record.Get("attachment")).String("type") == "hook_success" || object(message.Record.Get("attachment")).String("type") == "async_hook_response") || attachments.Invisible(message.Record.Get("attachment"))) {
			askHistory = append(askHistory, message)
			if persistSession {
				future, e := h.serial.BeginRecord(scope, askHistory)
				if e != nil {
					return 0, e
				}
				h.options.OnPhase("Record:returned")
				h.observeRecord(scope, future)
			}
			if e := h.serial.Accept(scope, message); e != nil {
				return 0, e
			}
			return requestretry.Resume, nil
		}
		if (message.Type() == "progress" && (retainedWebProgress(message.Record) || retainedTaskOutputProgress(message.Record) || retainedNativeBashProgress(message.Record))) || h.options.Print != nil && h.options.Print.NativeProcess && messages.IsNativeHookRecord(message.Record) {
			// Source keeps progress in mutableMessages and records it inline;
			// its normalizer emits only the original variant-specific SDK messages.
			if e := h.serial.Accept(scope, message); e != nil {
				return 0, e
			}
			askHistory = append(askHistory, message)
			if persistSession {
				future, e := h.serial.BeginRecord(scope, askHistory)
				if e != nil {
					return 0, e
				}
				h.options.OnPhase("Record:returned")
				h.observeRecord(scope, future)
			}
			if h.isAgent() {
				return requestretry.Resume, nil
			}
			data, e := message.MarshalJSON()
			if e != nil {
				return 0, e
			}
			output, e := sdkoutput.Decode(data)
			if e != nil {
				return 0, e
			}
			return h.normalizeYields(scope, output)
		}
		if value.APIRetryNotice && h.options.Print != nil && h.options.Print.NativeProcess && messages.IsNativeAPIRetryRecord(message.Record) {
			if h.isAgent() {
				return requestretry.Resume, nil
			}
			if e := h.serial.Accept(scope, message); e != nil {
				return 0, e
			}
			return h.enqueue(scope, nativeAPIRetryOutput(message.Record, h.options.Controller.SessionID(), value.APIRetryError))
		}
		if message.Type() != "assistant" && message.Type() != "user" {
			return 0, cap("nonordinary ASK yield " + message.Type())
		}
		askHistory = append(askHistory, message)
		if persistSession {
			h.options.OnPhase("Record:returned")
			recordFuture, e := h.serial.BeginRecord(scope, askHistory)
			if e != nil {
				return 0, e
			}
			if message.Type() == "assistant" && !h.isAgent() {
				h.observeRecord(scope, recordFuture)
			} else {
				if _, e = recordFuture.Await(scope); e != nil {
					return 0, e
				}
				h.options.OnPhase("Record:fulfilled")
			}
		}
		if !acknowledged && !h.isAgent() {
			acknowledged = true
			replay := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("user")}, protocol.JSONField{Name: "message", Value: initial.Get("message")}, protocol.JSONField{Name: "session_id", Value: quote(h.options.Controller.SessionID())}, protocol.JSONField{Name: "parent_tool_use_id", Value: raw(nil)}, protocol.JSONField{Name: "uuid", Value: initial.Get("uuid")}, protocol.JSONField{Name: "timestamp", Value: initial.Get("timestamp")}, protocol.JSONField{Name: "isReplay", Value: raw(true)})
			action, e := h.enqueue(scope, replay)
			if e != nil || action == requestretry.Stop {
				return action, e
			}
		}
		// Source QueryEngine increments only a real query user yield after Record/ACK.
		// The initial replay control message does not increment this counter.
		if message.Type() == "user" && !h.isAgent() {
			turnCount++
		}
		if err := h.serial.Accept(scope, message); err != nil {
			return 0, err
		}
		if h.isAgent() {
			return h.afterTeammateToolProjection(scope, requestretry.Resume, nil)
		}
		var output sdkoutput.Message
		if message.Assistant != nil {
			output, err = sdkoutput.FromEnvelope(message.Assistant)
			if v := message.Assistant.Message.Get("stop_reason"); v != nil && string(v) != "null" {
				lastStop = v
			}
		} else {
			bytes, e := message.MarshalJSON()
			if e != nil {
				return 0, e
			}
			output, err = sdkoutput.Decode(bytes)
			if v := object(message.Record.Get("message")).Get("stop_reason"); v != nil && string(v) != "null" {
				lastStop = v
			}
		}
		if err != nil {
			return 0, err
		}
		action, failure := h.normalizeYields(scope, output)
		if failure == nil && action != requestretry.Stop {
			// Budget and structured-output limits are tested in Source order by
			// the single outer yield consumer below.
		}
		return h.afterTeammateToolProjection(scope, action, failure)
	}
	askConsumer := func(value queryloop.Yield) (requestretry.NoticeAction, error) {
		action, err := askConsumerWithoutBudget(value)
		action, err = h.applyBudgetAfterYield(scope, action, err, total, lastStop, start, turnCount, checkpoint)
		if err == nil && action == requestretry.Resume && value.Message != nil && (value.Message.Type() == "user" || value.Message.Type() == "assistant") && !h.isAgent() {
			stopped, failure := h.nativeJSONSchemaRetryLimit(scope, schemaTurn, *value.Message, total, lastStop, start, turnCount, checkpoint)
			if stopped || failure != nil {
				return requestretry.Stop, failure
			}
		}
		return action, err
	}
	if h.options.Print != nil && h.options.Print.PlanAttachments != nil {
		loopHost.AttachmentsAfterTools = func(_ context.Context, round queryloop.Round) ([]queryloop.Message, error) {
			return h.options.Print.PlanAttachments(scope, round.History)
		}
	}
	outcome, err := h.runQueryWrapped(scope, modelContext, initialHistory, queryloop.Options{Profile: queryloop.SuccessfulToolRound, Model: inference.model, NativeModelPresent: h.options.Print != nil && h.options.Print.NativeProcess, FallbackModel: h.fallbackModel(), QuerySource: h.querySource(), NonInteractive: true, MaxTurns: h.maxTurns(), UUID: h.options.UUID, Now: h.options.Now}, loopHost, askConsumer, checkpoint)
	if err != nil {
		return err
	}
	if outcome.Reason == "consumer_stopped" {
		return nil
	}
	if h.isAgent() {
		return nil
	}
	// Per-ASK usage remains independent of cumulative producer-finally cost.
	// This consumer selects only the original final result message.
	var last queryloop.Message
	for _, message := range askHistory {
		if message.Type() == "assistant" || message.Type() == "user" {
			last = message
		}
	}
	if err = checkpoint(); err != nil {
		return err
	}
	// QueryEngine:1198 awaits completeCheckpoint before testing its terminal;
	// both the error (:1213) and success (:1266) branch await it again before
	// constructing/yielding result. Preserve these actual calls and their async
	// return, without repeating snapshot or physical flush work.
	if err = checkpoint(); err != nil {
		return err
	}
	result, err := h.resultOnOwner(scope, last, total, lastStop, start, turnCount)
	if err != nil {
		return err
	}
	_, err = h.enqueue(scope, result)
	return err
}
func (h *Host) completeCheckpoint(scope *cooperative.Scope) error {
	return h.completeCheckpointForMessage(scope, nil)
}
func (h *Host) completeCheckpointForMessage(scope *cooperative.Scope, messageID json.RawMessage) error {
	// Active snapshots are refused by Controller admission before mutation.
	// QueryEngine awaits the disabled async snapshot's catch-forwarded Promise
	// before calling flush; fulfilled returns retain that FIFO reaction boundary.
	h.options.OnPhase("checkpoint:begin")
	if h.options.Print != nil && h.options.Print.NativeProcess {
		persist, e := h.options.Controller.PersistsOnOwner(scope)
		if e != nil {
			return e
		}
		if !persist {
			e = scope.AwaitFulfilled()
			h.options.OnPhase("checkpoint:settled")
			return e
		}
	}
	if len(messageID) > 0 && h.options.Print != nil && h.options.Print.FileHistory != nil && !h.isAgent() {
		if e := h.options.Print.FileHistory.Complete(scope, messageID); e != nil {
			h.options.Controller.ReportFileHistoryError(e)
		}
	}
	if err := scope.AwaitFulfilledCatch(); err != nil {
		return err
	}
	future, err := h.pending.Flush(scope)
	if err != nil {
		return err
	}
	err = future.Await(scope)
	h.options.OnPhase("checkpoint:settled")
	return err
}
func (h *Host) Close(ctx context.Context) error {
	if e := h.CloseAutoDream(ctx); e != nil {
		return e
	}
	if e := h.closeSideQuestions(ctx); e != nil {
		return e
	}
	return h.options.Owner.Run(ctx, h.Shutdown)
}

// Shutdown is the actual Project cleanup boundary. It waits for admitted
// tracked writes and physical drains, leaving untracked outer Record futures
// independent. The process exit snapshot must follow on this same owner stack.
func (h *Host) Shutdown(scope *cooperative.Scope) error {
	if !scope.BelongsTo(h.options.Owner) {
		return cooperative.ErrRestricted
	}
	h.closing = true
	if h.authStatusDispose != nil {
		h.authStatusDispose()
		h.authStatusDispose = nil
	}
	if h.taskNotificationDispose != nil {
		if err := h.taskNotificationDispose(scope); err != nil {
			return err
		}
		h.taskNotificationDispose = nil
	}
	if err := h.nativePermissionHooks.Cancel(scope); err != nil {
		return err
	}
	if err := h.closeNativeSDKPermissionInput(scope); err != nil {
		return err
	}
	if err := h.closeNativeSDKHookInput(scope); err != nil {
		return err
	}
	if h.sessionInbox {
		if err := h.options.Controller.DisposeSessionInboxOnOwner(scope); err != nil {
			return err
		}
	}
	future, err := h.pending.Flush(scope)
	if err != nil {
		return err
	}
	if err = future.Await(scope); err != nil {
		return err
	}
	h.options.OnPhase("metadata:shutdown-reappend")
	// Source cleanup catches this best-effort synchronous operation independently.
	_ = h.options.Controller.Writer().RefreshMetadata()
	return nil
}

// Join is administrative resource teardown after the retained exit snapshot,
// not Source cleanup and not an implicit all-Record flush.
func (h *Host) Join(ctx context.Context) error {
	if e := h.CloseAutoDream(ctx); e != nil {
		return e
	}
	if e := h.closeSideQuestions(ctx); e != nil {
		return e
	}
	if err := h.nativePermissionHooks.Join(ctx); err != nil {
		return err
	}
	return h.pending.Close(ctx)
}

func (h *Host) History() []queryloop.Message { return h.serial.History() }

// OriginalHistoryOnOwner retains the actual producer envelopes for Agent
// finalization. It cannot be read from a foreign/stale scope or during ASK.
func (h *Host) OriginalHistoryOnOwner(scope *cooperative.Scope) ([]queryloop.Message, error) {
	if h == nil || !scope.BelongsTo(h.options.Owner) {
		return nil, cooperative.ErrRestricted
	}
	if h.serial == nil || h.askRunning || h.emitIterator != nil {
		return nil, cap("original settled ASK history lifecycle")
	}
	return h.serial.History(), nil
}

// ExitCode is the finite stream-json print policy after its final FIFO message.
// It does not join pending Record promises or model/SDK cancellation domains.
func (h *Host) ExitCode() int {
	if h.lastOutputType == "result" && h.lastOutputIsError {
		return 1
	}
	return 0
}

func (h *Host) observeRecord(scope *cooperative.Scope, future *sessioncontroller.RecordFuture) {
	if h.options.UnhandledRecord != nil {
		err := future.ObserveDetached(scope, func(scope *cooperative.Scope, err error) {
			if err == nil {
				h.options.OnPhase("Record:fulfilled")
				return
			}
			h.options.OnPhase("Record:rejected")
			h.options.UnhandledRecord(scope, err)
		})
		if err != nil {
			h.options.UnhandledRecord(scope, err)
		}
		return
	}
	// Observers never inspect/mutate model messages and never own cancellation.
	// The callback is posted with owner lifetime, independently of ASK's Scope.
	go func() {
		select {
		case <-future.Done():
		case <-h.options.Owner.Done():
			return
		}
		_, err := future.Wait(context.Background())
		_, _ = h.options.Owner.Submit(context.Background(), cooperative.Read, func(*cooperative.Scope) error {
			if err == nil {
				h.options.OnPhase("Record:fulfilled")
			} else {
				h.options.OnPhase("Record:rejected")
			}
			return nil
		})
	}()
}

// PendingPermissionRejections observes immutable pending state only. It cannot
// replace Bun runtime admission or be used as an ASK checkpoint/exit join.
func (h *Host) PendingPermissionRejections(scope *cooperative.Scope) ([]ownerasync.IgnoredRejection, error) {
	if h == nil {
		return nil, cooperative.ErrRestricted
	}
	return h.permissionRejections.Pending(scope)
}

func retainedPlanAttachment(raw json.RawMessage) bool {
	kind := object(raw).String("type")
	return kind == "plan_mode" || kind == "plan_mode_reentry" || kind == "plan_mode_exit" || kind == "auto_mode" || kind == "auto_mode_exit"
}

func retainedSkillAttachment(raw json.RawMessage) bool {
	kind := object(raw).String("type")
	return kind == "invoked_skills" || kind == "skill_listing" || kind == "file" || kind == "directory" || kind == "agent_mention" || kind == "mcp_resource" || kind == "deferred_tools_delta"
}

func retainedWebProgress(record protocol.JSONObject) bool {
	kind := object(record.Get("data")).String("type")
	return kind == "query_update" || kind == "search_results_received"
}

func retainedTaskOutputProgress(record protocol.JSONObject) bool {
	data := object(record.Get("data"))
	if data.String("type") != "waiting_for_task" || data.Get("taskDescription") == nil || data.Get("taskType") == nil {
		return false
	}
	for _, key := range []string{"taskDescription", "taskType"} {
		if _, e := protocol.DecodeJavaScriptJSONString(data.Get(key)); e != nil {
			return false
		}
	}
	if len(data.OwnProperties()) != 3 {
		return false
	}
	return strings.HasPrefix(record.String("toolUseID"), "task-output-waiting-")
}
