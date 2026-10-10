package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentbuiltinmetadata"
	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/systemprompt"
)

type nativeAgentFiles struct{ n *NativeBoundary }

func (f nativeAgentFiles) Stat(path string) (fs.FileInfo, error)      { return f.n.stat(path) }
func (f nativeAgentFiles) Lstat(path string) (fs.FileInfo, error)     { return f.n.lstat(path) }
func (f nativeAgentFiles) ReadFile(path string) ([]byte, error)       { return f.n.readFile(path) }
func (f nativeAgentFiles) ReadDir(path string) ([]fs.DirEntry, error) { return f.n.readDir(path) }
func (f nativeAgentFiles) Realpath(path string) (string, error) {
	if err := f.n.check("agent realpath", path); err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(path)
}
func (f nativeAgentFiles) Identity(info fs.FileInfo) string {
	return (mainAgentsFiles{f.n}).Identity(info)
}

func nativeAgentMarkdown(n *NativeBoundary, directory string) ([]string, error) {
	if err := n.check("agent file search", directory); err != nil {
		return nil, err
	}
	// Source maps inaccessible directories to empty before propagating other
	// process failures. Missing managed/user agent trees are normal startup.
	if _, err := n.stat(directory); err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
			return []string{}, nil
		}
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	config, err := nativeAgentRipgrepConfig(n, executable, filepath.Join(filepath.Dir(executable), "vendor", "ripgrep"))
	if err != nil {
		return nil, err
	}
	args := append(append([]string{}, config.Args...), "--files", "--hidden", "--follow", "--no-ignore", "--glob", "*.md", directory)
	command := exec.CommandContext(ctx, config.Command, args...)
	command.Dir = n.CWD
	command.Env = nativeAgentProcessEnvironment(n)
	output, err := command.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return []string{}, nil
		}
		return nil, err
	}
	files := []string{}
	for _, path := range strings.Split(strings.TrimSuffix(string(output), "\n"), "\n") {
		if path != "" {
			files = append(files, path)
		}
	}
	sort.Strings(files)
	return files, nil
}

func (m *MainPrint) newNativeAgentCatalog() error {
	var err error
	m.agents, err = m.nativeSettings.AgentCatalog(m.Owner, m.options.Native.CWD, m.options.Native.Environment["CLAUDE_CODE_ENTRYPOINT"] != "cli")
	if err == nil && m.teamWorker != nil && m.teamWorker.pane != nil {
		err = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			defs, err := m.agents.List(scope)
			if err != nil {
				return err
			}
			for _, definition := range defs.Active {
				if definition.Name == m.teamWorker.pane.AgentType && definition.Source != "built-in" {
					prompt, err := m.agents.Prompt(scope, definition, agentbuiltinmetadata.GuideContext{})
					if err != nil {
						return err
					}
					m.options.Arguments.AppendSystemPrompt += "\n\n\n# Custom Agent Instructions\n" + prompt
					break
				}
			}
			return nil
		})
	}
	if err == nil && m.teamWorker != nil && m.teamWorker.pane == nil {
		err = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			if err := m.agents.InstallCLIDefinitions(scope, []agentcatalog.Definition{m.teamWorker.preset.Definition}); err != nil {
				return err
			}
			definitions, err := m.agents.List(scope)
			if err != nil {
				return err
			}
			for _, definition := range definitions.Active {
				if definition.Name != m.teamWorker.preset.Start.Name {
					continue
				}
				m.teamWorker.preset.Definition = definition
				prompt, err := m.agents.Prompt(scope, definition, agentbuiltinmetadata.GuideContext{})
				if err != nil {
					return err
				}
				m.options.Arguments.SystemPrompt = prompt
				return nil
			}
			return mainCap("Approved team worker preset is unavailable")
		})
	}
	return err
}

// AgentCatalog creates the one reusable definition producer without requiring
// a conversation, selected provider, Controller or model request.
func (p *NativeProcessSettings) AgentCatalog(owner *cooperative.Owner, cwd string, nonInteractive bool) (*agentcatalog.Service, error) {
	if owner == nil || p == nil || !filepath.IsAbs(cwd) {
		return nil, mainCap("Agent catalog shared native settings/owner/cwd")
	}
	if _, err := p.InitialSettings(); err != nil {
		return nil, err
	}
	n := p.n.agentScopedBoundary(cwd)
	return agentcatalog.New(agentcatalog.Options{Owner: owner, Files: nativeAgentFiles{n}, Home: n.Home, CWD: cwd, ProjectRoot: cwd, ConfigHome: n.Environment["CLAUDE_CONFIG_DIR"], ManagedRoot: n.ManagedRoot, EnabledSources: p.startup.EnabledSettingSources(), Environment: func(key string) string { return n.Environment[key] }, Settings: p.startup.GetSettingsForSource, Now: n.Now, NonInteractive: nonInteractive, FindMarkdown: func(path string) ([]string, error) { return nativeAgentMarkdown(n, path) }, AutoMemoryEnabled: func(scope *cooperative.Scope) (bool, error) {
		if !scope.BelongsTo(owner) {
			return false, cooperative.ErrRestricted
		}
		settings, err := p.startup.GetInitialSettings()
		if settings == nil {
			return false, err
		}
		return systemprompt.AutoMemoryEnabled(p.environment, *settings), err
	}, Plugins: func(scope *cooperative.Scope) ([]agentcatalog.Definition, error) {
		return p.nativePluginAgentDefinitions(owner, scope, n)
	}, MemoryPrompt: func(scope *cooperative.Scope, name, memory string) (string, error) {
		return nativeAgentMemoryPrompt(owner, n, p.startup, scope, name, memory)
	}})
}

func (m *MainPrint) nativeAgentPrompt(scope *cooperative.Scope, definition agentcatalog.Definition) (string, error) {
	quoted, err := m.nativeAgentPromptJSON(scope, definition)
	if err != nil {
		return "", err
	}
	var display string
	if err = json.Unmarshal(quoted, &display); err != nil {
		return "", err
	}
	return display, nil
}

func (m *MainPrint) nativeAgentPromptJSON(scope *cooperative.Scope, definition agentcatalog.Definition) (json.RawMessage, error) {
	settings, err := m.Startup.GetInitialSettings()
	if err != nil {
		return nil, err
	}
	using3P := false
	for _, name := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_AZURE_OPENAI"} {
		using3P = using3P || nativeEnvTruthy(m.options.Native.Environment[name])
	}
	guide := agentbuiltinmetadata.GuideContext{Using3P: using3P, IssuesExplainer: "https://github.com/NanmiCoder/claude-code-haha/issues"}
	if settings != nil {
		guide.Settings = settings.Clone()
	}
	guide.Commands = m.Registry.AgentGuideCommands()
	definitions, err := m.agents.List(scope)
	if err != nil {
		return nil, err
	}
	for _, agent := range definitions.Active {
		guide.Agents = append(guide.Agents, agent.Fields.Clone())
	}
	if m.mcpTools != nil {
		guide.MCPServers, err = m.mcpTools.Status(scope)
		if err != nil {
			return nil, err
		}
	}
	return m.agents.PromptJSON(scope, definition, guide)
}

// AgentDefinitions exposes the same owner-bound producer to existing sessions
// and API integration. The returned declaration records are detached copies.
func (m *MainPrint) AgentDefinitions(ctx context.Context) (*agentcatalog.Result, error) {
	var output *agentcatalog.Result
	receipt, err := m.Owner.Submit(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if m.agents == nil {
			return mainCap("actual configured agent producer")
		}
		current, err := m.agents.List(scope)
		if err != nil {
			return err
		}
		copyDefinitions := func(values []agentcatalog.Definition) []agentcatalog.Definition {
			result := append([]agentcatalog.Definition{}, values...)
			for i := range result {
				result[i].Fields = result[i].Fields.Clone()
				result[i].Frontmatter = result[i].Frontmatter.Clone()
			}
			return result
		}
		output = &agentcatalog.Result{All: copyDefinitions(current.All), Active: copyDefinitions(current.Active), FailedFiles: append([]agentcatalog.Failure{}, current.FailedFiles...)}
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return output, err
}

// Ensure the production filesystem adapter is implemented against real OS
// file identity, including hard links and Source's platform fallback.
var _ agentcatalog.Files = nativeAgentFiles{}
