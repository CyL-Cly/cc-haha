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
	accepted["--agent"] = true
	accepted["--include-hook-events"] = true
	accepted["--json-schema"] = true
	accepted["--permission-prompt-tool"] = true
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
	a.IncludeHookEvents = boolean("includeHookEvents")
	a.JSONSchema = str("jsonSchema")
	a.PermissionPromptToolName = str("permissionPromptTool")
	if a.PermissionPromptToolName != "" && a.PermissionPromptToolName != "stdio" {
		return Command{}, parsed, &clibootstrap.MainCapabilityError{Boundary: "MCP permission prompt tool consumer"}
	}
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
	if a.SessionID != "" && a.Resume != "" && !a.ForkSession {
		return Command{}, parsed, errors.New("session-id and resume cannot be used together")
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
	if command.OutputFormat == "stream-json" && !command.Verbose && !(command.Print && (command.Arguments.Resume != "" || command.Arguments.ResumeWithoutValue)) {
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
	if command.Print && command.InputFormat == "stream-json" {
		if command.Prompt != "" {
			return 1, &clibootstrap.MainCapabilityError{Boundary: "positional prompt with structured SDK stdin"}
		}
		process, err := clibootstrap.NewMainPrint(clibootstrap.MainPrintOptions{PublicPrintLoadErrors: true, AnalyticsHTTP: o.AnalyticsHTTP, HookEvents: o.HookEvents, ComputerUse: o.ComputerUse, Arguments: o.Arguments, PaneTeammate: o.PaneTeammate, Native: o.Native, PID: os.Getpid(), Version: o.Version, Platform: o.Platform, Input: in, Output: out, Error: o.Stderr})
		if err != nil {
			return 1, err
		}
		defer process.Close()
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
	o.NonInteractivePermissions = command.Print
	o.Interactive = !command.Print
	o.ForegroundShellProgress = !command.Print
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
	session, err := NewSession(ctx, o)
	if err != nil {
		return 1, err
	}
	defer session.Close(context.Background())
	if command.Print && command.OutputFormat == "stream-json" && !command.Verbose {
		return 1, errors.New("Error: When using --print, --output-format=stream-json requires --verbose")
	}
	if !command.Print {
		history, fullscreen, bootstrapError := session.initialTUIHistory(ctx)
		if bootstrapError != nil {
			return 1, bootstrapError
		}
		err = tui.Run(ctx, session, tui.Options{Input: in, Output: out, Environment: env, Title: "Claude Code", InitialPrompt: command.Prompt, InitialHistory: history, Fullscreen: fullscreen})
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
		_, err = io.WriteString(out, partial.formatResultLine(message.String("result"), string(message.Get("is_error")) == "true"))
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
