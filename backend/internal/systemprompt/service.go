// Package systemprompt builds the release system sections before request
// preparation. It owns the same section cache lifecycle as the original CLI.
package systemprompt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const DynamicBoundary = "__SYSTEM_PROMPT_DYNAMIC_BOUNDARY__"

type OSInfo struct{ Type, Version, Release string }
type Command struct{ Name string }
type OutputStyle struct {
	Name, Prompt           string
	KeepCodingInstructions bool
}
type MCPClient struct{ Name, Type, Instructions string }

// Options contains host state and native primitives, never prepared prompt
// blocks. The registered tool catalog is supplied at Resolve time.
type Options struct {
	Environment           *clistartupenv.Environment
	CWD                   func() string
	Model, Platform       string
	AdditionalDirectories []string
	OSInfo                func() (OSInfo, error)
	IsGit                 func(context.Context) (bool, error)
	InitialSettings       func() (protocol.JSONObject, error)
	SessionStartDate      func() json.RawMessage
	NonInteractive        func() bool
	Worktree              func() bool
	SkillCommands         func(context.Context, string) ([]Command, error)
	OutputStyle           func(context.Context, string) (*OutputStyle, error)
	MCPClients            func() []MCPClient
	GlobalCacheScope      func() bool
	MCPInstructionsDelta  func() bool
	EmbeddedSearchTools   func() bool
	REPLMode              func() bool
	ForkSubagents         func() bool
	ScratchpadDirectory   func() (string, bool)
	AutoMemory            *AutoMemory
}

type section struct {
	name     string
	volatile bool
	compute  func() (*string, error)
}
type Service struct {
	options Options
	mu      sync.Mutex
	cache   map[string]*string
}

func New(options Options) (*Service, error) {
	if options.Environment == nil || options.CWD == nil || options.OSInfo == nil || options.IsGit == nil || options.InitialSettings == nil || options.SessionStartDate == nil || options.NonInteractive == nil || options.SkillCommands == nil || options.OutputStyle == nil {
		return nil, errors.New("system prompt requires native environment, settings, cwd, OS/git, session date, skills, style and interactive state")
	}
	return &Service{options: options, cache: make(map[string]*string)}, nil
}

// Clear is called by the host's /clear and /compact lifecycle. Resetting beta
// header latches remains owned by that host at the same original boundary.
func (s *Service) Clear() { s.mu.Lock(); s.cache = make(map[string]*string); s.mu.Unlock() }

func pointer(value string) *string { return &value }
func enabled(f func() bool) bool   { return f != nil && f() }
func rawString(value json.RawMessage) string {
	var text string
	_ = json.Unmarshal(value, &text)
	return text
}
func quoted(value string) json.RawMessage {
	return protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(value)))
}
func envEnabled(environment *clistartupenv.Environment, name string) bool {
	raw := environment.Get(name)
	if string(raw) == "true" {
		return true
	}
	value := strings.ToLower(strings.TrimSpace(rawString(raw)))
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

func (s *Service) resolveSections(sections []section) ([]*string, error) {
	s.mu.Lock()
	capturedCache := s.cache
	s.mu.Unlock()
	values := make([]*string, len(sections))
	done := make(chan struct{})
	failures := make(chan error, len(sections))
	var pending sync.WaitGroup
	for i, entry := range sections {
		pending.Add(1)
		go func(i int, entry section) {
			defer pending.Done()
			s.mu.Lock()
			cached, exists := capturedCache[entry.name]
			s.mu.Unlock()
			if !entry.volatile && exists {
				values[i] = cached
				return
			}
			value, err := entry.compute()
			if err != nil {
				failures <- err
				return
			}
			s.mu.Lock()
			s.cache[entry.name] = value
			s.mu.Unlock()
			values[i] = value
		}(i, entry)
	}
	go func() { pending.Wait(); close(done) }()
	select {
	case err := <-failures:
		return nil, err
	case <-done:
		select {
		case err := <-failures:
			return nil, err
		default:
			return values, nil
		}
	}
}

func (s *Service) Resolve(ctx context.Context, tools []protocol.JSONObject) ([]json.RawMessage, error) {
	s.mu.Lock()
	model := s.options.Model
	s.mu.Unlock()
	return s.resolveForModel(ctx, tools, model)
}

// ResolveForModel uses the same section cache and original native producers
// while rendering the caller's current runtime model.
func (s *Service) ResolveForModel(ctx context.Context, tools []protocol.JSONObject, model string) ([]json.RawMessage, error) {
	return s.resolveForModel(ctx, tools, model)
}
func (s *Service) resolveForModel(ctx context.Context, tools []protocol.JSONObject, model string) ([]json.RawMessage, error) {
	s.mu.Lock()
	o := s.options
	o.AdditionalDirectories = append([]string{}, s.options.AdditionalDirectories...)
	s.mu.Unlock()
	o.Model = model
	if envEnabled(o.Environment, "CLAUDE_CODE_SIMPLE") {
		prefix, _ := protocol.DecodeJavaScriptJSONString(quoted("You are Claude Code, Anthropic's official CLI for Claude.\n\nCWD: " + o.CWD() + "\nDate: "))
		date, err := protocol.DecodeJavaScriptJSONString(o.SessionStartDate())
		if err != nil {
			return nil, err
		}
		return []json.RawMessage{protocol.QuoteJavaScriptUTF16(append(prefix, date...))}, nil
	}
	// Source starts these three independent producers before reading initial
	// settings. envInfo is deliberately computed again by the dynamic section.
	var skills []Command
	var style *OutputStyle
	var errSkills, errStyle, errEnv error
	var producers sync.WaitGroup
	producers.Add(3)
	go func() { defer producers.Done(); skills, errSkills = o.SkillCommands(ctx, o.CWD()) }()
	go func() { defer producers.Done(); style, errStyle = o.OutputStyle(ctx, o.CWD()) }()
	go func() { defer producers.Done(); _, errEnv = s.environmentWithOptions(ctx, o) }()
	producers.Wait()
	if err := errors.Join(errSkills, errStyle, errEnv); err != nil {
		return nil, err
	}
	settings, err := o.InitialSettings()
	if err != nil {
		return nil, err
	}
	return s.renderWithOptions(ctx, o, tools, skills, style, settings, s.resolveSections)
}

func (s *Service) renderWithOptions(ctx context.Context, o Options, tools []protocol.JSONObject, skills []Command, style *OutputStyle, settings protocol.JSONObject, resolve func([]section) ([]*string, error)) ([]json.RawMessage, error) {
	names := make(map[string]bool, len(tools))
	for _, tool := range tools {
		names[rawString(tool.Get("name"))] = true
	}
	sections := []section{
		{name: "session_guidance", compute: func() (*string, error) { return s.sessionGuidance(names, skills), nil }},
		{name: "memory", compute: func() (*string, error) {
			if o.AutoMemory == nil {
				if AutoMemoryEnabled(o.Environment, settings) {
					return nil, errors.New("enabled auto memory requires its native path and filesystem binding")
				}
				return nil, nil
			}
			return o.AutoMemory.Load(ctx, o.Environment, settings)
		}},
		{name: "ant_model_override", compute: func() (*string, error) { return nil, nil }},
		{name: "env_info_simple", compute: func() (*string, error) {
			text, err := s.environmentWithOptions(ctx, o)
			if err != nil {
				return nil, err
			}
			return pointer(text), nil
		}},
		{name: "language", compute: func() (*string, error) {
			language := rawString(settings.Get("language"))
			if language == "" {
				return nil, nil
			}
			return pointer("# Language\nAlways respond in " + language + ". Use " + language + " for all explanations, comments, and communications with the user. Technical terms and code identifiers should remain in their original form."), nil
		}},
		{name: "output_style", compute: func() (*string, error) {
			if style == nil {
				return nil, nil
			}
			return pointer("# Output Style: " + style.Name + "\n" + style.Prompt), nil
		}},
		{name: "mcp_instructions", volatile: true, compute: func() (*string, error) {
			if enabled(o.MCPInstructionsDelta) || o.MCPClients == nil {
				return nil, nil
			}
			return mcpInstructions(o.MCPClients()), nil
		}},
		{name: "scratchpad", compute: func() (*string, error) {
			if o.ScratchpadDirectory == nil {
				return nil, nil
			}
			path, active := o.ScratchpadDirectory()
			if !active {
				return nil, nil
			}
			return pointer(scratchpad(path)), nil
		}},
		{name: "frc", compute: func() (*string, error) { return nil, nil }},
		{name: "summarize_tool_results", compute: func() (*string, error) { return pointer(summarizeText), nil }},
	}
	dynamic, err := resolve(sections)
	if err != nil {
		return nil, err
	}
	intro := introText
	if style != nil {
		intro = introStyledText
	}
	result := []json.RawMessage{quoted(intro), quoted(systemText)}
	if style == nil || style.KeepCodingInstructions {
		result = append(result, quoted(doingText))
	}
	result = append(result, quoted(actionsText), quoted(s.usingTools(names)), quoted(toneText), quoted(efficiencyText))
	if enabled(o.GlobalCacheScope) {
		result = append(result, quoted(DynamicBoundary))
	}
	for _, value := range dynamic {
		if value != nil {
			result = append(result, quoted(*value))
		}
	}
	return result, nil
}

func mcpInstructions(clients []MCPClient) *string {
	var blocks []string
	for _, client := range clients {
		if client.Type == "connected" && client.Instructions != "" {
			blocks = append(blocks, "## "+client.Name+"\n"+client.Instructions)
		}
	}
	if len(blocks) == 0 {
		return nil
	}
	return pointer("# MCP Server Instructions\n\nThe following MCP servers have provided instructions for how to use their tools and resources:\n\n" + strings.Join(blocks, "\n\n"))
}

func (s *Service) environment(ctx context.Context) (string, error) {
	return s.environmentWithOptions(ctx, s.options)
}

func (s *Service) environmentWithOptions(ctx context.Context, o Options) (string, error) {
	var git bool
	var osinfo OSInfo
	var errGit, errOS error
	var producers sync.WaitGroup
	producers.Add(2)
	go func() { defer producers.Done(); git, errGit = o.IsGit(ctx) }()
	go func() { defer producers.Done(); osinfo, errOS = o.OSInfo() }()
	producers.Wait()
	if err := errors.Join(errGit, errOS); err != nil {
		return "", err
	}
	return s.environmentValues(o, git, osinfo), nil
}

func (s *Service) environmentValues(o Options, git bool, osinfo OSInfo) string {
	items := []string{"# Environment", "You have been invoked in the following environment: ", " - Primary working directory: " + o.CWD()}
	if enabled(o.Worktree) {
		items = append(items, " - This is a git worktree — an isolated copy of the repository. Run all commands from this directory. Do NOT `cd` to the original repository root.")
	}
	items = append(items, fmt.Sprintf("  - Is a git repository: %t", git))
	if len(o.AdditionalDirectories) > 0 {
		items = append(items, " - Additional working directories:")
		for _, directory := range o.AdditionalDirectories {
			items = append(items, "  - "+directory)
		}
	}
	shell := rawString(o.Environment.Get("SHELL"))
	if shell == "" {
		shell = "unknown"
	}
	if strings.Contains(shell, "zsh") {
		shell = "zsh"
	} else if strings.Contains(shell, "bash") {
		shell = "bash"
	}
	shellLine := "Shell: " + shell
	uname := osinfo.Type + " " + osinfo.Release
	if o.Platform == "win32" {
		shellLine += " (use Unix shell syntax, not Windows — e.g., /dev/null not NUL, forward slashes in paths)"
		uname = osinfo.Version + " " + osinfo.Release
	}
	items = append(items, " - Platform: "+o.Platform, " - "+shellLine, " - OS Version: "+uname)
	name := marketingName(o.Model)
	modelLine := "You are powered by the model " + o.Model + "."
	if name != "" {
		modelLine = "You are powered by the model named " + name + ". The exact model ID is " + o.Model + "."
	}
	items = append(items, " - "+modelLine)
	if cutoff := knowledgeCutoff(o.Model); cutoff != "" {
		items = append(items, " - Assistant knowledge cutoff is "+cutoff+".")
	}
	items = append(items, " - The most recent Claude models are the Claude 5 family and Haiku 4.5. Model IDs — Fable 5.1: 'claude-fable-5-1', Opus 5.5: 'claude-opus-5-5', Sonnet 5.5: 'claude-sonnet-5-5', Haiku 4.5: 'claude-haiku-4-5-20251001'. When building AI applications, default to the latest and most capable Claude models.", " - Claude Code is available as a CLI in the terminal, desktop app (Mac/Windows), web app (claude.ai/code), and IDE extensions (VS Code, JetBrains).", " - Fast mode for Claude Code uses Claude Opus with faster output (it does not downgrade to a smaller model). It can be toggled with /fast.")
	return strings.Join(items, "\n")
}

// Environment computes the main session's original simple environment section.
// Child agents use AgentEnvironment, whose original format is different.
func (s *Service) Environment(ctx context.Context) (json.RawMessage, error) {
	value, err := s.environment(ctx)
	if err != nil {
		return nil, err
	}
	return quoted(value), nil
}

// AgentEnvironment ports computeEnvInfo; it stays an independent section after
// the child agent definition and Notes sections.
func (s *Service) AgentEnvironment(ctx context.Context, model string, additional []string) (json.RawMessage, error) {
	return agentEnvironment(ctx, s.options, model, additional)
}

// AgentEnvironmentAt computes the original section inside the child's cwd
// override without mutating the parent's cached sections or shared options.
func (s *Service) AgentEnvironmentAt(ctx context.Context, model string, additional []string, cwd string, isGit func(context.Context) (bool, error)) (json.RawMessage, error) {
	if cwd == "" || isGit == nil {
		return nil, errors.New("Agent environment requires actual child cwd/git producer")
	}
	o := s.options
	o.CWD, o.IsGit = func() string { return cwd }, isGit
	return agentEnvironment(ctx, o, model, additional)
}

// ComputeAgentEnvironment supplies the same native section to entrypoints which
// have no cached main system prompt or ASK context.
func ComputeAgentEnvironment(ctx context.Context, o Options, model string, additional []string) (json.RawMessage, error) {
	if o.Environment == nil || o.CWD == nil || o.OSInfo == nil || o.IsGit == nil {
		return nil, errors.New("Agent environment requires actual environment/cwd/OS/git producers")
	}
	return agentEnvironment(ctx, o, model, additional)
}

func agentEnvironment(ctx context.Context, o Options, model string, additional []string) (json.RawMessage, error) {
	var git bool
	var nativeOS OSInfo
	var gitError, osError error
	var pending sync.WaitGroup
	pending.Add(2)
	go func() { defer pending.Done(); git, gitError = o.IsGit(ctx) }()
	go func() { defer pending.Done(); nativeOS, osError = o.OSInfo() }()
	pending.Wait()
	if err := errors.Join(gitError, osError); err != nil {
		return nil, err
	}
	yes := "No"
	if git {
		yes = "Yes"
	}
	additionalText := ""
	if len(additional) > 0 {
		additionalText = "Additional working directories: " + strings.Join(additional, ", ") + "\n"
	}
	shell := rawString(o.Environment.Get("SHELL"))
	if shell == "" {
		shell = "unknown"
	}
	if strings.Contains(shell, "zsh") {
		shell = "zsh"
	} else if strings.Contains(shell, "bash") {
		shell = "bash"
	}
	shellLine := "Shell: " + shell
	uname := nativeOS.Type + " " + nativeOS.Release
	if o.Platform == "win32" {
		shellLine += " (use Unix shell syntax, not Windows — e.g., /dev/null not NUL, forward slashes in paths)"
		uname = nativeOS.Version + " " + nativeOS.Release
	}
	modelLine := "You are powered by the model " + model + "."
	if name := marketingName(model); name != "" {
		modelLine = "You are powered by the model named " + name + ". The exact model ID is " + model + "."
	}
	cutoffText := ""
	if cutoff := knowledgeCutoff(model); cutoff != "" {
		cutoffText = "\n\nAssistant knowledge cutoff is " + cutoff + "."
	}
	return quoted("Here is useful information about the environment you are running in:\n<env>\nWorking directory: " + o.CWD() + "\nIs directory a git repo: " + yes + "\n" + additionalText + "Platform: " + o.Platform + "\n" + shellLine + "\nOS Version: " + uname + "\n</env>\n" + modelLine + cutoffText), nil
}

func knowledgeCutoff(model string) string {
	for _, entry := range []struct{ pattern, date string }{{"claude-fable-5-1", "June 2026"}, {"claude-opus-5-5", "June 2026"}, {"claude-sonnet-5-5", "June 2026"}, {"claude-opus-5", "May 2026"}, {"claude-fable-5", "January 2026"}, {"claude-opus-4-8", "January 2026"}, {"claude-opus-4-7", "January 2026"}, {"claude-sonnet-5", "January 2026"}, {"claude-sonnet-4-6", "August 2025"}, {"claude-opus-4-5", "May 2025"}, {"claude-haiku-4", "February 2025"}, {"claude-opus-4", "January 2025"}, {"claude-sonnet-4", "January 2025"}} {
		if strings.Contains(model, entry.pattern) {
			return entry.date
		}
	}
	return ""
}

func marketingName(model string) string {
	for _, entry := range []struct{ pattern, name string }{{"claude-fable-5-1", "Fable 5.1"}, {"claude-fable-5", "Fable 5"}, {"claude-opus-5-5", "Opus 5.5"}, {"claude-opus-5", "Opus 5"}, {"claude-opus-4-8", "Opus 4.8"}, {"claude-opus-4-7", "Opus 4.7"}, {"claude-opus-4-5", "Opus 4.5"}, {"claude-opus-4-1", "Opus 4.1"}, {"claude-opus-4", "Opus 4"}, {"claude-sonnet-5-5", "Sonnet 5.5"}, {"claude-sonnet-5", "Sonnet 5"}, {"claude-sonnet-4-6", "Sonnet 4.6"}, {"claude-sonnet-4-5", "Sonnet 4.5"}, {"claude-sonnet-4", "Sonnet 4"}, {"claude-3-7-sonnet", "Claude 3.7 Sonnet"}, {"claude-3-5-sonnet", "Claude 3.5 Sonnet"}, {"claude-haiku-4-5", "Haiku 4.5"}, {"claude-3-5-haiku", "Claude 3.5 Haiku"}} {
		if strings.Contains(model, entry.pattern) {
			name := entry.name
			oneMillion := strings.Contains(entry.pattern, "claude-fable-") || strings.Contains(entry.pattern, "claude-sonnet-") || entry.pattern == "claude-opus-5-5" || entry.pattern == "claude-opus-5" || entry.pattern == "claude-opus-4-8" || entry.pattern == "claude-opus-4-7"
			if strings.Contains(strings.ToLower(model), "[1m]") && oneMillion {
				name += " (with 1M context)"
			}
			return name
		}
	}
	return ""
}
