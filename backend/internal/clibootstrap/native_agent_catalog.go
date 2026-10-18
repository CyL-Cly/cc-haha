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
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginoptions"
	"github.com/NanmiCoder/cc-haha/backend/internal/systemprompt"
)

type nativeAgentFiles struct{ n *NativeBoundary }

func (f nativeAgentFiles) Stat(path string) (fs.FileInfo, error) {
	info, err := f.n.stat(path)
	if err == nil {
		info = withAgentsNativeIdentity(path, info, false)
	}
	return info, err
}
func (f nativeAgentFiles) Lstat(path string) (fs.FileInfo, error) {
	info, err := f.n.lstat(path)
	if err == nil {
		info = withAgentsNativeIdentity(path, info, true)
	}
	return info, err
}
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
	m.agents, err = m.nativeSettings.AgentCatalog(m.Owner, m.options.Native.CWD, m.options.Native.Environment["CLAUDE_CODE_ENTRYPOINT"] != "cli", m.pluginOptions)
	if err == nil && m.options.Arguments.AgentsJSON != "" {
		err = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			return m.agents.InstallCLIJSON(scope, json.RawMessage(m.options.Arguments.AgentsJSON))
		})
	}
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
func (p *NativeProcessSettings) AgentCatalog(owner *cooperative.Owner, cwd string, nonInteractive bool, options ...*pluginoptions.Service) (*agentcatalog.Service, error) {
	bindings, err := p.nativeAgentCatalogOptions(owner, cwd, nonInteractive, options...)
	if err != nil {
		return nil, err
	}
	return agentcatalog.New(bindings)
}

func (p *NativeProcessSettings) nativeAgentCatalogOptions(owner *cooperative.Owner, cwd string, nonInteractive bool, options ...*pluginoptions.Service) (agentcatalog.Options, error) {
	if owner == nil || p == nil || !filepath.IsAbs(cwd) {
		return agentcatalog.Options{}, mainCap("Agent catalog shared native settings/owner/cwd")
	}
	if _, err := p.InitialSettings(); err != nil {
		return agentcatalog.Options{}, err
	}
	settingsReader, err := p.nativeAgentSettingsReader()
	if err != nil {
		return agentcatalog.Options{}, err
	}
	memorySettingsReader, checkMemoryControls, err := p.startup.NativeAgentMemorySettingsReader(p.settings, p.settingsState, p.environment)
	if err != nil {
		return agentcatalog.Options{}, err
	}
	n := p.n.agentScopedBoundary(cwd)
	var savedOptions *pluginoptions.Service
	if len(options) > 1 {
		return agentcatalog.Options{}, mainCap("one Agent catalog saved options producer")
	}
	if len(options) == 1 {
		savedOptions = options[0]
	}
	return agentcatalog.Options{Owner: owner, Files: nativeAgentDirectoryFiles{nativeAgentFiles{n}, p.n}, NativeConfigHome: p.n.nativeConfigHomePath, Home: n.Home, CWD: cwd, ProjectRoot: cwd, ConfigHome: n.ConfigHomeDirectory(), ManagedRoot: n.ManagedRoot, EnabledSources: p.startup.EnabledSettingSources(), Environment: func(key string) string {
		var value string
		_ = json.Unmarshal(p.environment.Get(key), &value)
		return value
	}, Settings: settingsReader, Now: n.Now, NonInteractive: nonInteractive, FindMarkdown: func(path string) ([]string, error) { return p.nativeAgentFindMarkdown(n, path) }, AutoMemoryEnabled: func(scope *cooperative.Scope) (bool, error) {
		if !scope.BelongsTo(owner) {
			return false, cooperative.ErrRestricted
		}
		if err := checkMemoryControls(); err != nil {
			return false, err
		}
		var disabled string
		_ = json.Unmarshal(p.environment.Get("CLAUDE_CODE_DISABLE_AUTO_MEMORY"), &disabled)
		if nativeEnvTruthy(disabled) {
			return false, nil
		}
		// Source's defined-falsy override and SIMPLE/remote fast paths do not
		// read settings. The cached merged reader is reached only afterward.
		var simple, remote, remoteMemory string
		_ = json.Unmarshal(p.environment.Get("CLAUDE_CODE_SIMPLE"), &simple)
		_ = json.Unmarshal(p.environment.Get("CLAUDE_CODE_REMOTE"), &remote)
		_ = json.Unmarshal(p.environment.Get("CLAUDE_CODE_REMOTE_MEMORY_DIR"), &remoteMemory)
		if nativeAgentDefinedFalsy(disabled) {
			return true, nil
		}
		if nativeEnvTruthy(simple) || nativeEnvTruthy(remote) && remoteMemory == "" {
			return false, nil
		}
		settings, err := memorySettingsReader()
		if settings == nil {
			return false, err
		}
		return systemprompt.AutoMemoryEnabled(p.environment, *settings), err
	}, Plugins: func(scope *cooperative.Scope) ([]agentcatalog.Definition, error) {
		return p.nativePluginAgentDefinitions(owner, scope, n, savedOptions)
	}, MemoryPrompt: func(scope *cooperative.Scope, name, memory string) (string, error) {
		if err := checkMemoryControls(); err != nil {
			return "", err
		}
		return nativeAgentMemoryPrompt(owner, p.n.agentScopedBoundary(cwd), p.startup, scope, name, memory)
	}}, nil
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
