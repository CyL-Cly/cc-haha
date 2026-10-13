package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliargs"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
)

type Command struct {
	Arguments                         clibootstrap.PrintArguments
	Print                             bool
	Prompt, InputFormat, OutputFormat string
	Verbose, IncludePartial           bool
	Bare                              bool
	PaneTeammate                      *teamspawn.PaneIdentity
}

// ParseCommand consumes the existing Commander-compatible registration result.
// Every still-unported flag has an explicit boundary, rather than being ignored.
func ParseCommand(args []string, version, cwd, temp string, environments ...map[string]string) (Command, cliargs.Result, error) {
	parsed := cliargs.Parse(args, cliargs.Config{Version: version})
	if parsed.Parsed == nil {
		return Command{}, parsed, nil
	}
	if parsed.Parsed.Path != "claude" {
		return Command{}, parsed, &clibootstrap.MainCapabilityError{Boundary: "CLI action " + parsed.Parsed.Path}
	}
	accepted := map[string]bool{"--resume-session-at": true, "--no-session-persistence": true, "--append-system-prompt": true, "--worktree": true, "-w": true, "--worktree-base-ref": true, "--bare": true, "--no-computer-use": true, "--print": true, "-p": true, "--verbose": true, "--input-format": true, "--output-format": true, "--include-partial-messages": true, "--replay-user-messages": true, "--tools": true, "--allowedTools": true, "--allowed-tools": true, "--dangerously-skip-permissions": true, "--allow-dangerously-skip-permissions": true, "--add-dir": true, "--model": true, "--thinking": true, "--max-thinking-tokens": true, "--system-prompt": true, "--max-turns": true, "--setting-sources": true, "--settings": true, "--session-id": true, "--resume": true, "-r": true, "--fork-session": true, "--permission-mode": true}
	accepted["--betas"], accepted["--mcp-config"], accepted["--strict-mcp-config"] = true, true, true
	accepted["--agent"], accepted["--agents"], accepted["--disable-slash-commands"] = true, true, true
	accepted["--disallowedTools"], accepted["--disallowed-tools"], accepted["--mcp-debug"] = true, true, true
	for _, key := range []string{"--continue", "-c", "--system-prompt-file", "--append-system-prompt-file", "--name", "-n"} {
		accepted[key] = true
	}
	for _, key := range []string{"--prefill", "--rewind-files", "--init", "--init-only", "--maintenance"} {
		accepted[key] = true
	}
	accepted["--ide"] = true
	accepted["--from-pr"] = true
	accepted["--enable-auto-mode"] = true
	accepted["--include-hook-events"] = true
	accepted["--json-schema"] = true
	for _, key := range []string{"--max-budget-usd", "--task-budget", "--fallback-model", "--workload", "--enable-auth-status"} {
		accepted[key] = true
	}
	accepted["--permission-prompt-tool"] = true
	accepted["--sdk-url"] = true
	for _, key := range []string{"--debug", "-d", "--debug-file", "--debug-to-stderr"} {
		accepted[key] = true
	}
	for _, key := range []string{"--agent-id", "--agent-name", "--team-name", "--agent-color", "--parent-session-id", "--plan-mode-required", "--agent-type", "--teammate-mode", "--effort", "--plugin-dir", "--no-chrome"} {
		accepted[key] = true
	}
	for _, key := range parsed.Parsed.ProvidedOptions {
		if key == "--chrome" && parsed.Parsed.Options["chrome"] == false {
			continue
		}
		if !accepted[key] {
			return Command{}, parsed, &clibootstrap.MainCapabilityError{Boundary: "CLI option " + key}
		}
	}
	o := parsed.Parsed.Options
	str := func(k string) string { v, _ := o[k].(string); return v }
	boolean := func(k string) bool { v, _ := o[k].(bool); return v }
	list := func(k string) []string {
		var result []string
		switch value := o[k].(type) {
		case []string:
			result = append(result, value...)
		case []any:
			for _, v := range value {
				s, ok := v.(string)
				if ok {
					result = append(result, s)
				}
			}
		}
		return result
	}
	sources := clibootstrap.Prepare(args, clibootstrap.Options{WorkingDir: cwd, TempDir: temp})
	if sources.ExitCode != 0 {
		return Command{}, parsed, errors.New(strings.TrimSuffix(sources.Stderr, "\n"))
	}
	mode := str("permissionMode")
	turns := math.MaxInt32
	if value, ok := o["maxTurns"].(float64); ok {
		if value < 1 || value > math.MaxInt32 || math.Trunc(value) != value {
			return Command{}, parsed, errors.New("max-turns must be a positive integer")
		}
		turns = int(value)
	}
	a := clibootstrap.PrintArguments{ResumeAt: str("resumeSessionAt"), AppendSystemPrompt: str("appendSystemPrompt"), Model: str("model"), SystemPrompt: str("systemPrompt"), SessionID: str("sessionId"), Resume: str("resume"), ResumeWithoutValue: boolean("resume"), ForkSession: boolean("forkSession"), Tools: list("tools"), AllowedTools: list("allowedTools"), MaxTurns: turns, PermissionMode: mode, DangerouslySkipPermissions: boolean("dangerouslySkipPermissions"), AllowDangerouslySkipPermissions: boolean("allowDangerouslySkipPermissions"), AdditionalDirectories: list("addDir"), Replay: boolean("replayUserMessages"), Sources: sources.State}
	a.Agent = str("agent")
	a.AgentsJSON = str("agents")
	a.Betas, a.MCPConfigs, a.StrictMCPConfig = list("betas"), list("mcpConfig"), boolean("strictMcpConfig")
	a.DisableSlashCommands = boolean("disableSlashCommands")
	a.DisallowedTools = list("disallowedTools")
	a.Continue, a.SystemPromptFile, a.AppendSystemPromptFile, a.Name = boolean("continue"), str("systemPromptFile"), str("appendSystemPromptFile"), str("name")
	a.Prefill, a.RewindFiles = str("prefill"), str("rewindFiles")
	a.Init, a.InitOnly, a.Maintenance = boolean("init"), boolean("initOnly"), boolean("maintenance")
	a.IDE = boolean("ide")
	if value, exists := o["fromPr"]; exists {
		a.FromPR, _ = json.Marshal(value)
	}
	a.EnableAutoMode = boolean("enableAutoMode")
	a.IncludeHookEvents = boolean("includeHookEvents")
	a.JSONSchema = str("jsonSchema")
	if err := applyPublicHarnessPolicyArguments(o, &a); err != nil {
		return Command{}, parsed, err
	}
	a.PermissionPromptToolName = str("permissionPromptTool")
	a.SDKURL = str("sdkUrl")

	a.DebugArguments = append([]string{}, args...)
	a.DebugFormatted = boolean("print") && (str("outputFormat") == "json" || str("outputFormat") == "stream-json")
	a.TeammateMode = str("teammateMode")
	a.PluginDirectories = list("pluginDir")
	a.EffortLevel = str("effort")
	if value, ok := o["chrome"].(bool); ok {
		a.Chrome = &value
	}
	var pane *teamspawn.PaneIdentity
	if commandTeamsEnabled(environments) {
		anyIdentity := str("agentId") != "" || str("agentName") != "" || str("teamName") != ""
		if anyIdentity && (str("agentId") == "" || str("agentName") == "" || str("teamName") == "") {
			return Command{}, parsed, errors.New("Error: --agent-id, --agent-name, and --team-name must all be provided together")
		}
		if anyIdentity {
			pane = &teamspawn.PaneIdentity{AgentID: str("agentId"), AgentName: str("agentName"), TeamName: str("teamName"), Color: str("agentColor"), ParentSessionID: str("parentSessionId"), AgentType: str("agentType"), PlanModeRequired: boolean("planModeRequired")}
		}
	}
	_, a.HasCustomSystemPrompt = o["systemPrompt"].(string)
	if value, exists := o["worktree"]; exists {
		a.Worktree = &worktree.Startup{BaseRef: str("worktreeBaseRef")}
		if name, ok := value.(string); ok {
			a.Worktree.Name = &name
		}
	}
	if value, ok := o["computerUse"].(bool); ok {
		a.ComputerUse = &value
	}
	if value, exists := o["sessionPersistence"].(bool); exists {
		a.SessionPersistenceDisabled = !value
	}
	if a.SDKURL == "" && a.SessionID != "" && (a.Resume != "" || a.ResumeWithoutValue || a.Continue) && !a.ForkSession {
		return Command{}, parsed, &commandValidationError{"Error: --session-id can only be used with --continue or --resume if --fork-session is also specified.\n"}
	}
	// The full default prompt and default catalog are ported separately. An
	// explicit subset reaches the actual shared Harness without altering them.
	simple := boolean("bare")
	if len(environments) > 0 {
		simple = simple || commandTruthy(environments[0]["CLAUDE_CODE_SIMPLE"])
	}
	if len(a.Tools) == 0 && simple {
		a.Tools = []string{"Bash,Read,Edit"}
	}
	switch str("thinking") {
	case "enabled", "adaptive":
		a.Thinking = requestcontext.Thinking{Type: "adaptive"}
	case "disabled":
		a.Thinking = requestcontext.Thinking{Type: "disabled"}
	default:
		if value, exists := o["maxThinkingTokens"].(float64); exists {
			a.MaxThinkingTokens = &value
		}
	}
	command := Command{PaneTeammate: pane, Bare: boolean("bare"), Arguments: a, Print: boolean("print"), InputFormat: str("inputFormat"), OutputFormat: str("outputFormat"), Verbose: boolean("verbose"), IncludePartial: boolean("includePartialMessages")}
	if a.SDKURL != "" {
		command.Print = true
		if command.InputFormat == "" {
			command.InputFormat = "stream-json"
		}
		if command.OutputFormat == "" {
			command.OutputFormat = "stream-json"
		}
		if _, exists := o["verbose"]; !exists {
			command.Verbose = true
		}
	}
	if command.InputFormat == "" {
		command.InputFormat = "text"
	}
	if command.OutputFormat == "" {
		command.OutputFormat = "text"
	}
	if len(parsed.Parsed.ProcessedArgs) > 0 {
		command.Prompt, _ = parsed.Parsed.ProcessedArgs[0].(string)
	}
	if command.InputFormat == "stream-json" && command.OutputFormat != "stream-json" {
		return Command{}, parsed, &commandValidationError{"Error: --input-format=stream-json requires output-format=stream-json.\n"}
	}
	if a.SDKURL != "" && (command.InputFormat != "stream-json" || command.OutputFormat != "stream-json") {
		return Command{}, parsed, &commandValidationError{"Error: --sdk-url requires both --input-format=stream-json and --output-format=stream-json.\n"}
	}
	if command.Arguments.Replay && (command.InputFormat != "stream-json" || command.OutputFormat != "stream-json") {
		return Command{}, parsed, &commandValidationError{"Error: --replay-user-messages requires both --input-format=stream-json and --output-format=stream-json.\n"}
	}
	if command.IncludePartial && (!command.Print || command.OutputFormat != "stream-json") {
		return Command{}, parsed, &commandValidationError{"Error: --include-partial-messages requires --print and --output-format=stream-json."}
	}
	if command.Arguments.SessionPersistenceDisabled && !command.Print {
		return Command{}, parsed, &commandValidationError{"Error: --no-session-persistence can only be used with --print mode."}
	}
	// Source checks verbose after loadInitialMessages; a resume failure must
	// reach emitLoadError even without verbose, before this format validation.
	if command.OutputFormat == "stream-json" && !command.Verbose && !command.Arguments.InitOnly && command.Arguments.RewindFiles == "" && !(command.Print && (command.Arguments.Resume != "" || command.Arguments.ResumeWithoutValue || command.Arguments.Continue)) {
		return Command{}, parsed, errors.New("Error: When using --print, --output-format=stream-json requires --verbose")
	}
	return command, parsed, nil
}

// RunCommand adapts terminal/print output around one shared Session. SDK input
// retains MainPrint's original owner iterator and asynchronous output FIFO.
func RunCommand(ctx context.Context, command Command, o Options, in io.Reader, out io.Writer, env []string) (code int, err error) {
	if command.Print {
		o.publicPrintLoadErrors = true
		defer func() {
			if handled, failure := clibootstrap.WritePrintLoadError(out, o.Stderr, command.OutputFormat, err); handled {
				code, err = 1, failure
			}
		}()
	}
	if command.Print && command.Arguments.ResumeAt != "" && command.Arguments.Resume == "" && !command.Arguments.ResumeWithoutValue {
		return 1, errors.New("Error: --resume-session-at requires --resume")
	}
	if command.Print && command.Arguments.RewindFiles != "" {
		if command.Arguments.Resume == "" && !command.Arguments.ResumeWithoutValue {
			return 1, &commandValidationError{"Error: --rewind-files requires --resume\n"}
		}
		if command.Prompt != "" {
			return 1, &commandValidationError{"Error: --rewind-files is a standalone operation and cannot be used with a prompt\n"}
		}
	}
	if o.Stderr == nil {
		o.Stderr = io.Discard
	}
	o.Arguments = command.Arguments
	o.PaneTeammate = command.PaneTeammate
	o.HookEvents = command.Print && command.OutputFormat == "stream-json" && command.Verbose
	if command.Bare {
		environment := make(map[string]string, len(o.Environment)+1)
		for key, value := range o.Environment {
			environment[key] = value
		}
		environment["CLAUDE_CODE_SIMPLE"] = "1"
		o.Environment = environment
		if o.Native != nil {
			o.Native.Environment["CLAUDE_CODE_SIMPLE"] = "1"
		}
	}
	if command.Print && command.InputFormat == "stream-json" && !command.Arguments.InitOnly {
		if command.Prompt != "" && command.Arguments.SDKURL == "" {
			return 1, &clibootstrap.MainCapabilityError{Boundary: "positional prompt with structured SDK stdin"}
		}
		if command.Arguments.SDKURL != "" && command.Prompt != "" {
			if protocol.TrimJavaScriptString(command.Prompt) == "" {
				in = strings.NewReader("")
			} else {
				data, err := protocol.EncodeNDJSON(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: []byte(`"user"`)}, protocol.JSONField{Name: "session_id", Value: []byte(`""`)}, protocol.JSONField{Name: "message", Value: raw(map[string]string{"role": "user", "content": command.Prompt})}, protocol.JSONField{Name: "parent_tool_use_id", Value: []byte("null")}))
				if err != nil {
					return 1, err
				}
				in = strings.NewReader(string(data))
			}
		}
		process, err := clibootstrap.NewMainPrint(clibootstrap.MainPrintOptions{PublicPrintLoadErrors: true, AnalyticsHTTP: o.AnalyticsHTTP, HookEvents: o.HookEvents, ComputerUse: o.ComputerUse, Arguments: o.Arguments, PaneTeammate: o.PaneTeammate, Native: o.Native, PID: os.Getpid(), Version: o.Version, Platform: o.Platform, Input: in, Output: out, Error: o.Stderr})
		if err != nil {
			return 1, err
		}
		defer process.Close()
		if command.Arguments.RewindFiles != "" {
			if err := process.StartSession(ctx); err != nil {
				return 1, err
			}
			if err := process.RewindCLIFileCheckpoint(ctx, command.Arguments.RewindFiles); err != nil {
				return 1, err
			}
			_, err := fmt.Fprintf(out, "Files rewound to state at message %s\n", command.Arguments.RewindFiles)
			return 0, err
		}
		if !command.Verbose {
			if err := process.StartSession(ctx); err != nil {
				return 1, err
			}
			return 1, errors.New("Error: When using --print, --output-format=stream-json requires --verbose")
		}
		return process.Run(ctx)
	}
	var result json.RawMessage
	jsonOutput := printJSONCollector{verbose: command.Verbose}
	var partial printPartialOutputTracker
	prior := o.OnSDK
	o.OnSDK = func(data json.RawMessage) error {
		if prior != nil {
			if err := prior(data); err != nil {
				return err
			}
		}
		m := obj(data)
		partial.observe(m)
		if command.Print && command.OutputFormat == "json" {
			jsonOutput.observe(data)
		}
		if m.String("type") == "result" {
			result = append(result[:0], data...)
		}
		if command.Print && command.OutputFormat == "stream-json" && (command.IncludePartial || m.String("type") != "stream_event") {
			_, err := fmt.Fprintln(out, string(data))
			return err
		}
		return nil
	}
	o.NonInteractivePermissions = command.Print || command.Arguments.InitOnly
	o.Interactive = !command.Print && !command.Arguments.InitOnly
	o.ForegroundShellProgress = !command.Print && !command.Arguments.InitOnly
	if !command.Print {
		if o.Native != nil && o.Native.NativeProcess && o.Native.Environment["CLAUDE_CODE_ENTRYPOINT"] == "" {
			o.Native.Environment["CLAUDE_CODE_ENTRYPOINT"] = "cli"
		} else if o.Native == nil {
			environment := make(map[string]string, len(o.Environment)+1)
			for key, value := range o.Environment {
				environment[key] = value
			}
			if environment["CLAUDE_CODE_ENTRYPOINT"] == "" {
				environment["CLAUDE_CODE_ENTRYPOINT"] = "cli"
			}
			o.Environment = environment
		}
	}
	if !command.Print && !command.Arguments.Continue && (command.Arguments.ResumeWithoutValue || command.Arguments.Resume == "" && commandTruthyJSON(command.Arguments.FromPR)) {
		var picked bool
		command, picked, err = selectCLIResumeFromPicker(ctx, command, o, in, out, env)
		if err != nil {
			return 1, err
		}
		if !picked {
			return 1, nil
		}
		o.Arguments = command.Arguments
	}
	session, err := NewSession(ctx, o)
	if err != nil {
		return 1, err
	}
	defer session.Close(context.Background())
	if command.Arguments.InitOnly {
		return 0, nil
	}
	if command.Print && command.Arguments.RewindFiles != "" {
		if err = session.process.RewindCLIFileCheckpoint(ctx, command.Arguments.RewindFiles); err != nil {
			return 1, err
		}
		_, err = fmt.Fprintf(out, "Files rewound to state at message %s\n", command.Arguments.RewindFiles)
		return 0, err
	}
	if command.Print && command.OutputFormat == "stream-json" && !command.Verbose {
		return 1, errors.New("Error: When using --print, --output-format=stream-json requires --verbose")
	}
	if !command.Print {
		history, fullscreen, bootstrapError := session.initialTUIHistory(ctx)
		if bootstrapError != nil {
			return 1, bootstrapError
		}
		terminalTitle, titleError := session.initialCLITerminalTitle(ctx)
		if titleError != nil {
			return 1, titleError
		}
		err = tui.Run(ctx, session, tui.Options{Input: in, Output: out, Environment: env, Title: "Claude Code", TerminalTitle: terminalTitle, InitialDraft: protocol.TrimJavaScriptString(command.Arguments.Prefill), InitialPrompt: command.Prompt, InitialHistory: history, Fullscreen: fullscreen})
		if err != nil {
			return 1, err
		}
		return 0, nil
	}
	initial, err := session.submitHookInitialInput(ctx)
	if err != nil {
		return 1, err
	}
	prompt := command.Prompt
	if prompt == "" {
		data, e := io.ReadAll(in)
		if e != nil {
			return 1, e
		}
		prompt = string(data)
	}
	if session.process != nil {
		prompt, err = session.process.PrependNativeMainAgentInitialPrompt(ctx, prompt)
		if err != nil {
			return 1, err
		}
	}
	if prompt == "" && !initial {
		return 1, errors.New("Error: Input must be provided either through stdin or as a prompt argument when using --print")
	}
	if prompt != "" {
		if err = session.Submit(ctx, tui.Input{TurnID: newID(), Text: prompt}, nil); err != nil {
			return 1, err
		}
	}
	message := obj(result)
	if command.OutputFormat == "json" {
		var document json.RawMessage
		document, err = jsonOutput.document()
		if err == nil {
			_, err = fmt.Fprintln(out, string(document))
		}
	} else if command.OutputFormat == "text" {
		if message.String("subtype") == "error_max_budget_usd" {
			budget := "undefined"
			if command.Arguments.MaxBudgetUSD != nil {
				budget = publicBudgetNumber(*command.Arguments.MaxBudgetUSD)
			}
			_, err = io.WriteString(out, "Error: Exceeded USD budget ("+budget+")")
		} else {
			_, err = io.WriteString(out, partial.formatResultLine(message.String("result"), string(message.Get("is_error")) == "true"))
		}
	}
	if err != nil {
		return 1, err
	}
	if string(message.Get("is_error")) == "true" {
		return 1, nil
	}
	return 0, nil
}

func environmentValue(values []map[string]string, key string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0][key]
}

func commandTeamsEnabled(values []map[string]string) bool {
	if len(values) == 0 {
		return false
	}
	env := values[0]
	if value, exists := env["CC_HAHA_AGENT_TEAMS_ENABLED"]; exists && !commandTruthy(value) {
		return false
	}
	if env["USER_TYPE"] == "ant" {
		return true
	}
	for _, key := range []string{"CC_HAHA_AGENT_TEAMS_ENABLED", "CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS", "CC_HAHA_AGENT_TEAMS_DEFAULT"} {
		if value, exists := env[key]; exists {
			return commandTruthy(value)
		}
	}
	return false
}
