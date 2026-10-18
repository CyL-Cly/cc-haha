package clibootstrap

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionplans"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolsearch"
	"github.com/NanmiCoder/cc-haha/backend/internal/webfetch"
	"github.com/NanmiCoder/cc-haha/backend/internal/websearch"
)

//go:embed assets/mcp-review.md
var mcpReviewPrompt string

func (m *NativeMCPToolContext) checkOrdinary(scope *cooperative.Scope) error {
	if !scope.BelongsTo(m.command.Owner) {
		return cooperative.ErrRestricted
	}
	return nil
}

// entrypoints/mcp.ts supplies fresh default AppState getters and no-op setters.
// These callbacks are that real context, not the interactive ASK state store.
func (m *NativeMCPToolContext) ordinaryOptions(options builtintools.Options) (builtintools.Options, error) {
	n := m.command.Native
	var err error
	if m.state == nil {
		m.state, err = bootstrapstate.New(m.command.Owner, uuidMain(), n.CWD, func() (string, error) { return uuidMain(), nil })
		if err != nil {
			return options, err
		}
	}
	config := n.ConfigHomeDirectory()
	if config == "" {
		config = filepath.Join(n.Home, ".claude")
	}
	plans := m.plans
	if plans == nil {
		plans, err = sessionplans.New(sessionplans.Options{Bootstrap: m.state, ConfigDir: config, CWD: n.CWD, InitialSettings: m.command.Startup.GetInitialSettings, OnError: func(err error) { _, _ = fmt.Fprintln(m.command.Error, err) }})
		if err != nil {
			return options, err
		}
	}
	m.plans = plans
	options.RipgrepConfig = func() (string, []string, error) {
		executable, e := os.Executable()
		if e != nil {
			return "", nil, e
		}
		config, e := nativeAgentRipgrepConfig(n, executable, filepath.Join(filepath.Dir(executable), "vendor", "ripgrep"))
		if e != nil {
			return "", nil, fmt.Errorf("ripgrep is not available. Install ripgrep and ensure rg --version works in this environment.")
		}
		return config.Command, config.Args, nil
	}
	options.BashCwd = &builtintools.BashCwdBindings{Owner: m.command.Owner, TemporaryDirectory: n.Temp, Changed: func(scope *cooperative.Scope, _, cwd string) error { return m.setOrdinaryCWD(scope, cwd) }}
	options.Images = m.ordinaryImageBindings()
	options.AgentDefinitions = func(scope *cooperative.Scope) ([]agentcatalog.Definition, error) {
		return []agentcatalog.Definition{}, m.checkOrdinary(scope)
	}
	// No active agent can pass the shared Agent implementation's lookup. Retain
	// this assertion instead of silently constructing an interactive Agent loop.
	noActiveAgent := func(_ context.Context, scope *cooperative.Scope, _ builtintools.AgentRequest) (*ownerasync.OwnerFuture[builtintools.AgentRunResult], error) {
		if err := m.checkOrdinary(scope); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("ordinary MCP has no active agent definition")
	}
	options.RunAgent = noActiveAgent
	options.RunBackgroundAgent = noActiveAgent
	options.GetTodos = func(scope *cooperative.Scope) (json.RawMessage, error) {
		return json.RawMessage(`[]`), m.checkOrdinary(scope)
	}
	options.SetTodos = func(scope *cooperative.Scope, _ json.RawMessage) error { return m.checkOrdinary(scope) }
	noop := m.checkOrdinary
	options.PlanMode = &builtintools.PlanModeBindings{Plans: plans, CheckInactive: func(scope *cooperative.Scope) error {
		if err := noop(scope); err != nil {
			return err
		}
		settings, err := m.command.Startup.GetInitialSettings()
		if err != nil {
			return err
		}
		if settings != nil && string(settings.Get("disablePlanMode")) == "true" {
			return fmt.Errorf("Plan mode is disabled by your organization.")
		}
		return nil
	}, Enter: noop, Exit: noop, InterviewPhase: func() (bool, error) {
		return n.Environment["USER_TYPE"] == "ant" || envTruthyNative(n.Environment["CLAUDE_CODE_PLAN_MODE_INTERVIEW_PHASE"]), nil
	}, HasTaskTool: func(scope *cooperative.Scope) (bool, error) { return true, noop(scope) }}
	options.BackgroundTasks = m.ordinaryBackgroundBindings()
	m.background = options.BackgroundTasks
	review, err := ordinaryReviewCommand()
	if err != nil {
		return options, err
	}
	options.Skill = &builtintools.SkillBindings{Commands: func(scope *cooperative.Scope) ([]builtintools.SkillCommand, error) {
		return []builtintools.SkillCommand{{Metadata: review.Metadata(), Definition: &review}}, noop(scope)
	}, Call: m.callOrdinaryReview}

	options.ToolSearch = &builtintools.ToolSearchBindings{Service: toolsearch.New(), Enabled: func(scope *cooperative.Scope) (bool, error) { return toolsearch.Optimistic(n.Environment), noop(scope) }}
	tls, err := tlsconfig.NewState(n.readFile)
	if err != nil {
		return options, err
	}
	m.modelTLS = tls
	client, err := webfetch.NewHTTPClient(n.Environment, tls)
	if err != nil {
		return options, err
	}
	fetch, err := webfetch.New(webfetch.Options{HTTPClient: client, Now: n.Now, UserAgent: fmt.Sprintf("Claude-User (claude-code/%s; +https://support.anthropic.com/)", m.command.version), Settings: func(context.Context) (webfetch.Settings, error) {
		settings, err := m.command.Startup.GetInitialSettings()
		if err != nil {
			return webfetch.Settings{}, err
		}
		result := webfetch.Settings{DesktopServerURL: n.Environment["CC_HAHA_DESKTOP_SERVER_URL"]}
		if settings != nil && settings.Get("skipWebFetchPreflight") != nil {
			var skip bool
			_ = json.Unmarshal(settings.Get("skipWebFetchPreflight"), &skip)
			result.SkipPreflight = &skip
		}
		return result, nil
	}, PersistID: func() string {
		return fmt.Sprintf("webfetch-%d-%s", n.Now().UnixMilli(), webfetch.RandomBase36Suffix(nativeMathRandom()))
	}, PersistBinary: func(ctx context.Context, data []byte, mime, id string) (webfetch.Persisted, error) {
		dir, ok := ctx.Value(webPersistenceKey{}).(string)
		if !ok || !filepath.IsAbs(dir) {
			return webfetch.Persisted{}, cooperative.ErrRestricted
		}
		if err := n.check("MCP WebFetch binary", dir); err != nil {
			return webfetch.Persisted{}, err
		}
		if err := os.MkdirAll(dir, 0700); err != nil {
			return webfetch.Persisted{}, err
		}
		path := filepath.Join(dir, id+"."+webfetch.ExtensionForMIME(mime))
		if err := n.check("writeFile", path); err != nil {
			return webfetch.Persisted{}, err
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			return webfetch.Persisted{Error: err}, nil
		}
		return webfetch.Persisted{Path: path, Size: len(data)}, nil
	}, ApplyPrompt: func(context.Context, json.RawMessage, bool) (json.RawMessage, error) {
		return nil, cooperative.ErrRestricted
	}})
	if err != nil {
		return options, err
	}
	options.WebFetch = &builtintools.WebFetchBindings{Fetch: fetch, Now: n.Now, Prepare: func(scope *cooperative.Scope, ctx context.Context) (context.Context, error) {
		if err := noop(scope); err != nil {
			return nil, err
		}
		dir := filepath.Join(config, "projects", projectpath.Sanitize(m.state.Snapshot().OriginalCWD), m.state.Snapshot().SessionID, "tool-results")
		return context.WithValue(ctx, webPersistenceKey{}, dir), nil
	}, ApplyPrompt: func(ctx context.Context, scope *cooperative.Scope, prompt json.RawMessage, nonInteractive bool) (json.RawMessage, error) {
		models, err := m.ordinaryModels(scope, tls)
		if err != nil {
			return nil, err
		}
		settings, err := m.command.Startup.GetInitialSettings()
		if err != nil {
			return nil, err
		}
		merged := protocol.NewJSONObject()
		if settings != nil {
			merged = settings.Clone()
		}
		selector, err := modelselection.NewParsing(modelselection.Options{Environment: n.Environment, Settings: merged})
		if err != nil {
			return nil, err
		}
		return models.ApplyWebFetchPrompt(ctx, scope, prompt, selector.SmallFastModel(), nonInteractive)
	}}
	search, err := websearch.New(websearch.Options{Client: client, Now: n.Now, Settings: func() (protocol.JSONObject, error) {
		settings, err := m.command.Startup.GetInitialSettings()
		if settings == nil {
			return protocol.NewJSONObject(), err
		}
		return settings.Clone(), err
	}})
	if err != nil {
		return options, err
	}
	options.WebSearch = &builtintools.WebSearchBindings{Search: search, Model: func(scope *cooperative.Scope) (string, error) { return m.model, noop(scope) }, Native: func(ctx context.Context, scope *cooperative.Scope, in protocol.JSONObject, model string, nonInteractive bool, progress websearch.Progress) ([]protocol.JSONObject, error) {
		models, err := m.ordinaryModels(scope, tls)
		if err != nil {
			return nil, err
		}
		return models.NativeWebSearch(ctx, scope, in, model, nonInteractive, progress)
	}, MonthYear: func() string {
		now := n.Now()
		if override := n.Environment["CLAUDE_CODE_OVERRIDE_DATE"]; override != "" {
			if date, err := time.Parse("2006-01-02", override); err == nil {
				now = date
			}
		}
		return now.Format("January 2006")
	}}
	if m.workflowDefinitions == nil {
		m.workflowDefinitions, err = m.command.Settings.WorkflowDefinitions(m.command.Owner, n.CWD)
	}
	if err != nil {
		return options, err
	}
	options.Workflow = &builtintools.WorkflowBindings{Enabled: m.workflowDefinitions.EnabledOnOwner, Launch: m.launchOrdinaryWorkflow, Guideline: m.ordinaryWorkflowGuideline, AutoConsent: func(scope *cooperative.Scope) (bool, error) { return false, noop(scope) }, Ultracode: func(scope *cooperative.Scope) (bool, error) { return false, noop(scope) }}
	m.hooks, err = m.ordinaryHooks()
	if err != nil {
		return options, err
	}
	options.Worktree, err = m.ordinaryWorktreeBindings()
	if err != nil {
		return options, err
	}
	return options, nil
}
func (m *NativeMCPToolContext) ordinaryModels(scope *cooperative.Scope, tls *tlsconfig.State) (*sdkhost.NativeMCPModels, error) {
	if err := m.checkOrdinary(scope); err != nil {
		return nil, err
	}

	global, err := m.command.Global.Get()
	if err != nil {
		return nil, err
	}
	if global.Get("userID") == nil || global.String("userID") == "" {
		data := make([]byte, 32)
		if _, err := rand.Read(data); err != nil {
			return nil, err
		}
		id := hex.EncodeToString(data)
		if err := m.command.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
			next := current.Clone()
			next.Set("userID", quoteMain(id))
			return &next, nil
		}); err != nil {
			return nil, err
		}
	}
	if m.models == nil {
		return nil, cooperative.ErrRestricted
	}
	return m.models, nil
}
func (m *NativeMCPToolContext) initializeOrdinaryModels(scope *cooperative.Scope, tls *tlsconfig.State) error {
	factory, err := messages.NewFactory(uuidMain, m.command.Native.Now)
	if err != nil {
		return err
	}
	n := m.command.Native
	permission := func(scope *cooperative.Scope) (*ownerasync.OwnerFuture[protocol.JSONObject], error) {
		if err := m.checkOrdinary(scope); err != nil {
			return nil, err
		}
		future, err := ownerasync.NewOwnerFuture[protocol.JSONObject](m.command.Owner)
		if err != nil {
			return nil, err
		}
		return future, future.Resolve(scope, emptyMCPPermissionContext())
	}
	auth, err := m.ordinaryModelAuth(tls)
	if err != nil {
		return err
	}
	m.modelAuth = auth
	settings, err := m.command.Startup.GetInitialSettings()
	if err != nil {
		return err
	}
	m.model, _, err = auth.resolveNativeModel(context.Background(), scope, settings, "")
	if err != nil {
		return err
	}
	m.models, err = sdkhost.NewNativeMCPModels(scope, sdkhost.Options{NativeOpenAI: auth.nativeOpenAIConfig, NativeGrok: auth.nativeGrokConfig, NativeAPIKeySource: auth.nativeAPIKeySource, NativeAuthHelper: auth.nativeAuthHelperBinding, NativeClaudeAuth: m.ordinaryClaudeAuthBinding, NativeBedrock: auth.nativeBedrockBinding, NativeVertex: auth.nativeVertexBinding, Owner: m.command.Owner, Factory: factory, Global: m.command.Global, Startup: m.command.Startup, UUID: uuidMain, Now: n.Now, NowMillis: n.NowMillis, OriginalEnv: n.Environment, TLSState: tls, Model: m.model, Runtime: provider.SourceRuntime{Platform: m.platform, Arch: "arm64", Version: "v24.3.0"}, Print: &sdkhost.PrintOptions{NativeProcess: true, Tools: m.tools, Thinking: requestcontext.Thinking{Type: "disabled"}}, Version: m.command.version}, m.state.Snapshot().SessionID, permission, func(scope *cooperative.Scope, o modelselection.Options) (*modelselection.Selector, error) {
		return auth.nativeModelSelection(context.Background(), scope, o)
	})
	return err
}
func (m *NativeMCPToolContext) callOrdinaryReview(ctx context.Context, scope *cooperative.Scope, name string, command builtintools.SkillCommand, args *string, call queryloop.JSONToolCallContext) (queryloop.ToolCallResult, error) {
	if err := m.checkOrdinary(scope); err != nil {
		return queryloop.ToolCallResult{}, err
	}
	if command.Definition == nil {
		return queryloop.ToolCallResult{}, cooperative.ErrRestricted
	}
	if _, err := command.Definition.Prompt(ctx, args, skillcommands.Invocation{NativeScope: scope, SessionID: m.state.Snapshot().SessionID, Platform: m.platform}); err != nil {
		return queryloop.ToolCallResult{}, err
	}
	// MCP discards the prompt command's returned messages/ModifyContext. Its
	// original command list contains only review; it never starts a query loop.
	return queryloop.ToolCallResult{Data: rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "success", Value: json.RawMessage(`true`)}, protocol.JSONField{Name: "commandName", Value: quoteMain(name)}))}, nil
}

func (m *NativeMCPToolContext) ordinaryBackgroundBindings() *builtintools.BackgroundTaskBindings {
	n := m.command.Native
	noop := m.checkOrdinary
	return &builtintools.BackgroundTaskBindings{Register: func(scope *cooperative.Scope, _ *sessioncontroller.BackgroundTask) error { return noop(scope) }, Lookup: func(scope *cooperative.Scope, _ string) (*sessioncontroller.BackgroundTask, error) {
		return nil, noop(scope)
	}, List: func(scope *cooperative.Scope) ([]protocol.JSONObject, error) {
		return []protocol.JSONObject{}, noop(scope)
	}, Update: func(scope *cooperative.Scope, _ string, _ func(protocol.JSONObject) (protocol.JSONObject, error)) error {
		return noop(scope)
	}, MarkNotified: func(scope *cooperative.Scope, _ string) error { return noop(scope) }, Stop: func(scope *cooperative.Scope, _ string, _ float64) (protocol.JSONObject, error) {
		return protocol.JSONObject{}, noop(scope)
	}, Delete: func(scope *cooperative.Scope, _ string) error { return noop(scope) }, RegisterName: func(scope *cooperative.Scope, _, _ string) error { return noop(scope) }, ResolveName: func(scope *cooperative.Scope, _ string) (string, error) { return "", noop(scope) }, Emit: func(scope *cooperative.Scope, _ protocol.JSONObject) error { return noop(scope) }, Notify: func(scope *cooperative.Scope, _, _ string) error { return noop(scope) }, OutputDirectory: func(scope *cooperative.Scope) (string, error) {
		if err := noop(scope); err != nil {
			return "", err
		}
		base := n.Environment["CLAUDE_CODE_TMPDIR"]
		if base == "" {
			base = "/tmp"
		}
		if physical, err := filepath.EvalSymlinks(base); err == nil {
			base = physical
		}
		return filepath.Join(base, "claude-"+strconv.Itoa(os.Getuid()), projectpath.Sanitize(n.CWD), m.state.Snapshot().SessionID, "tasks"), nil
	}, CheckPath: func(path string) error { return n.check("MCP task output", path) }}
}

func (m *NativeMCPToolContext) ordinaryWorkflowGuideline(scope *cooperative.Scope) (string, error) {
	if e := m.checkOrdinary(scope); e != nil {
		return "", e
	}
	auth := &MainPrint{Owner: m.command.Owner, Startup: m.command.Startup, Global: m.command.Global}
	return auth.nativeWorkflowGuideline(scope)
}

func ordinaryReviewCommand() (skillcommands.Definition, error) {
	return skillcommands.NewNativeCommand(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("prompt")}, protocol.JSONField{Name: "name", Value: quoteMain("review")}, protocol.JSONField{Name: "description", Value: quoteMain("Review a pull request")}, protocol.JSONField{Name: "progressMessage", Value: quoteMain("reviewing pull request")}, protocol.JSONField{Name: "contentLength", Value: json.RawMessage(`0`)}, protocol.JSONField{Name: "source", Value: quoteMain("builtin")}), func(ctx context.Context, args *string, _ skillcommands.Invocation) (string, error) {
		text := ""
		if args != nil {
			text = *args
		}
		return strings.Replace(mcpReviewPrompt+"    ", "${args}", text, 1), ctx.Err()
	})
}
