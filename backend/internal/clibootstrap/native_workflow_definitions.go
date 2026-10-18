package clibootstrap

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/gitbranch"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflow"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflowcommands"
)

type nativeWorkflowFiles struct{ n *NativeBoundary }

func (f nativeWorkflowFiles) Stat(path string) (fs.FileInfo, error) {
	return f.n.stat(f.n.nativePhysicalPath(path))
}
func (f nativeWorkflowFiles) ReadFile(path string) ([]byte, error) {
	return f.n.readFile(f.n.nativePhysicalPath(path))
}
func (f nativeWorkflowFiles) ReadDir(path string) ([]workflowcommands.Entry, error) {
	entries, err := f.n.readDir(f.n.nativePhysicalPath(path))
	if err != nil {
		return nil, err
	}
	result := make([]workflowcommands.Entry, 0, len(entries))
	for _, entry := range entries {
		result = append(result, workflowcommands.Entry{Name: entry.Name(), File: entry.Type().IsRegular(), SymbolicLink: entry.Type()&fs.ModeSymlink != 0})
	}
	return result, nil
}

// WorkflowDefinitions is the native API producer. It borrows complete settings
// and the supplied actor. Discovery and compilation do not allocate a Harness.
func (p *NativeProcessSettings) WorkflowDefinitions(owner *cooperative.Owner, cwd string, history ...*workflow.History) (*workflow.Service, error) {
	if len(history) > 1 {
		return nil, mainCap("one Workflow history producer")
	}
	if owner == nil || !filepath.IsAbs(cwd) {
		return nil, mainCap("Workflow shared native settings/owner/cwd")
	}
	state, err := bootstrapstate.New(owner, uuidMain(), cwd, func() (string, error) { return uuidMain(), nil })
	if err != nil {
		return nil, err
	}
	n := p.n.agentScopedBoundary(cwd)
	git, err := gitbranch.New(gitbranch.Options{Owner: owner, CWD: func() string { return cwd }, Now: func() int64 { return int64(n.NowMillis()) }, StatSync: n.stat, Stat: n.stat, ReadFile: n.readFile, WatchFileOwned: (&gitbranch.NativePollWatch{Owner: owner, Stat: n.stat}).WatchOwned, ScrollDraining: func() bool { return false }, RegisterCleanup: func(func(*cooperative.Scope) error) {
		panic("Workflow definition discovery cannot initialize branch watchers")
	}})
	if err != nil {
		return nil, err
	}
	var records *workflow.History
	if len(history) == 1 {
		records = history[0]
	}
	return p.workflowDefinitions(owner, state, git, cwd, records)
}

func (p *NativeProcessSettings) workflowDefinitions(owner *cooperative.Owner, state *bootstrapstate.State, git *gitbranch.Service, cwd string, history *workflow.History) (*workflow.Service, error) {
	if _, err := p.InitialSettings(); err != nil {
		return nil, err
	}
	n := p.n.agentScopedBoundary(cwd)
	resolver, err := bootstrapstate.NewConfigResolver(owner, bootstrapstate.ConfigHost{Environment: func(key string) (string, bool) { value, exists := n.Environment[key]; return value, exists }, HomeDir: func() (string, error) { return n.Home, nil }})
	if err != nil {
		return nil, err
	}
	gate, err := workflowcommands.NewGate(owner, p.startup, p.environment)
	if err != nil {
		return nil, err
	}
	var service *workflow.Service
	err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		commands, err := workflowcommands.New(scope, workflowcommands.Options{Owner: owner, State: state, Config: resolver, Gate: gate, Git: git, Home: n.Home, FS: nativeWorkflowFiles{n}, CanonicalGitRoot: func(scope *cooperative.Scope, start string) (string, error) {
			if !scope.BelongsTo(owner) {
				return "", cooperative.ErrRestricted
			}
			return usercontext.CanonicalGitRoot(mainSettingsFiles{n: n}, func(path string) (string, error) {
				if e := n.check("workflow-realpath", path); e != nil {
					return "", e
				}
				return filepath.EvalSymlinks(path)
			}, start), nil
		}, Diagnostic: func(*cooperative.Scope, string) {}, PluginDirectories: func(ctx context.Context) ([]workflowcommands.PluginDirectory, error) {
			plugins, e := p.LoadPlugins(ctx, "", false)
			if e != nil {
				return nil, e
			}
			raw, e := json.Marshal(plugins)
			if e != nil {
				return nil, e
			}
			var cache protocol.JSONObject
			if e := json.Unmarshal(raw, &cache); e != nil {
				return nil, e
			}
			// Original pluginWorkflows reads the legacy `plugins` field. The
			// actual loader returns enabled/disabled/errors, so that field is
			// absent. Preserve the observed consumer contract instead of
			// silently changing plugin availability during the migration.
			if cache.Get("plugins") == nil {
				return []workflowcommands.PluginDirectory{}, nil
			}
			return nil, mainCap("legacy plugins cache field consumer")
		}})
		if err != nil {
			return err
		}
		metadata, err := commands.MetadataOnOwner(scope)
		if err != nil {
			return err
		}
		config, err := resolver.ConfigHome(scope)
		if err != nil {
			return err
		}
		files := workflow.DefinitionFiles{Lstat: n.lstat, MkdirAll: func(path string) error {
			if err := n.check("workflow-mkdir", path); err != nil {
				return err
			}
			return os.MkdirAll(path, 0777)
		}, WriteFile: func(path string, value []byte) error {
			if err := n.check("workflow-write", path); err != nil {
				return err
			}
			return os.WriteFile(path, value, 0666)
		}, Remove: func(path string) error {
			if err := n.check("workflow-remove", path); err != nil {
				return err
			}
			return os.Remove(path)
		}}
		service, err = workflow.New(workflow.DefinitionOptions{Owner: owner, Metadata: metadata, Gate: gate, BeginDefinitions: commands.BeginDefinitions, BeginCommands: commands.BeginCommands, ClearCommandCache: commands.ClearCommandCache, ProjectDirectory: commands.ProjectWorkflowsDirectory, Compile: func(scope *cooperative.Scope, body []uint16) error {
			if !scope.BelongsTo(owner) {
				return cooperative.ErrRestricted
			}
			_, err := workflow.CompileBody(body)
			return err
		}, ConfigDir: n.nativePhysicalPath(config), OriginalCWD: cwd, ProcessCWD: cwd, Files: files, History: history})
		return err
	})
	return service, err
}

func (m *MainPrint) newNativeWorkflowDefinitions() error {
	if !m.options.Native.NativeProcess {
		return nil
	}
	var err error
	m.workflowDefinitions, err = m.nativeSettings.workflowDefinitions(m.Owner, m.state, m.Git, m.options.Native.CWD, nil)
	return err
}
