package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/httphook"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/ignorepattern"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type TmuxFastPathIO struct {
	Input         io.Reader
	Output, Error io.Writer
}

// RunNativeTmuxWorktree is pre-CLI configuration/native work only. It never
// creates MainPrint, its Controller, model credentials or an ASK worker.
func RunNativeTmuxWorktree(ctx context.Context, args []string, n *NativeBoundary, platform, executable string, stdio TmuxFastPathIO) (worktree.TmuxStartupResult, error) {
	if n == nil || !n.NativeProcess {
		return worktree.TmuxStartupResult{}, mainCap("native tmux fast-path process")
	}
	settings, e := NewNativeProcessSettings(n, platform, State{})
	if e != nil {
		return worktree.TmuxStartupResult{}, e
	}
	defer settings.Close(context.Background())
	owner := cooperative.New()
	defer owner.Close()
	workContext, cancelWork := context.WithCancel(ctx)
	ctx = workContext
	var work sync.WaitGroup
	defer func() { cancelWork(); work.Wait() }()
	sid := uuidMain()
	factory, e := messages.NewFactory(uuidMain, n.Now)
	if e != nil {
		return worktree.TmuxStartupResult{}, e
	}
	env := map[string]string{}
	for k, v := range n.Environment {
		env[k] = v
	}
	bindings := &queryhooks.CommandBindings{Owner: owner, Factory: factory, Timestamp: func() string { return n.Now().UTC().Format("2006-01-02T15:04:05.000Z") }, NowMillis: n.NowMillis, ShellResolver: queryhooks.NewCommandShellResolver(env, platform)}
	bindings.Input = func(_ context.Context, call queryhooks.Context) (protocol.JSONObject, error) {
		if call.Scope == nil || !call.Scope.BelongsTo(owner) {
			return protocol.JSONObject{}, cooperative.ErrRestricted
		}
		p := protocol.NewJSONObject(protocol.JSONField{Name: "session_id", Value: quoteMain(sid)}, protocol.JSONField{Name: "transcript_path", Value: quoteMain(filepath.Join(env["CLAUDE_CONFIG_DIR"], "projects", projectpath.Sanitize(n.CWD), sid+".jsonl"))}, protocol.JSONField{Name: "cwd", Value: quoteMain(n.CWD)}, protocol.JSONField{Name: "hook_event_name", Value: quoteMain(call.HookEvent)})
		for _, f := range call.InputFields.OwnProperties() {
			var name string
			_ = json.Unmarshal(f.NameJSON, &name)
			p.Set(name, f.Value)
		}
		return p, nil
	}
	hookRuntime, e := newNativeTmuxHookRuntime(owner, n, env, sid, platform, &work)
	if e != nil {
		return worktree.TmuxStartupResult{}, e
	}
	defer hookRuntime.Close(context.Background())
	bindings.Async = hookRuntime.registry
	bindings.Run = hookRuntime.Run

	// cli.tsx calls this before plugin loading/registerHookCallbacks. Its
	// settings snapshot has the original empty registered-plugin channel.
	// InitialSettings() would eagerly LoadCached plugins for server callers.
	merged := func() (*protocol.JSONObject, error) {
		if e := settings.ensureSettings(); e != nil {
			return nil, e
		}
		return settings.startup.GetInitialSettings()
	}
	tlsState, e := tlsconfig.NewState(n.readFile)
	if e != nil {
		return worktree.TmuxStartupResult{}, e
	}
	httpBindings := &queryhooks.HTTPBindings{Owner: owner, Run: func(ctx context.Context, call queryhooks.Context, hook, input protocol.JSONObject) (httphook.Result, error) {
		if call.Scope == nil || !call.Scope.BelongsTo(owner) {
			return httphook.Result{}, cooperative.ErrRestricted
		}
		snapshot, e := merged()
		if e != nil {
			return httphook.Result{}, e
		}
		prepared, e := httphook.Prepare(hook, *snapshot, env)
		if e != nil {
			return httphook.Result{}, e
		}
		if prepared.Blocked != nil {
			return httphook.Result{Body: quoteMain(""), Error: prepared.Blocked}, nil
		}
		// Source cli.tsx runs this fast path before print/REPL initialize the
		// sandbox manager. waitForNetworkInitialization therefore returns false
		// and getProxyPort is undefined, even when sandbox.enabled is true.
		// The original environment proxy/SSRF transport still applies below.
		hook, snapshotCopy, body := hook.Clone(), snapshot.Clone(), rawMain(input)
		future, e := tmuxNativeWork(owner, call.Scope, &work, func() (httphook.Result, error) {
			options := httphook.NativeOptions{Environment: env}
			proxy := provider.ResolveNativeProxySelection(hook.String("url"), env)
			if strings.HasPrefix(strings.ToLower(hook.String("url")), "https:") || !proxy.Bypass && strings.HasPrefix(strings.ToLower(proxy.ProxyURL), "https:") {
				verified, e := tlsState.VerifiedConfig(env)
				if e != nil {
					return httphook.Result{}, e
				}
				options.VerifiedTLS = verified
			}
			return httphook.Execute(ctx, hook, snapshotCopy, body, options)
		})
		if e != nil {
			return httphook.Result{}, e
		}
		return future.Await(call.Scope)
	}}
	var hooks *queryhooks.Service
	ensureHooks := func() error {
		if hooks != nil {
			return nil
		}
		if _, e := merged(); e != nil {
			return e
		}
		var e error
		hooks, e = queryhooks.New(queryhooks.Options{Profile: queryhooks.ResolvedSettingsProfile, MergedSettings: merged, Settings: settings.settings, SettingsState: settings.settingsState, OriginalEnv: map[string]string{"CLAUDE_CODE_SIMPLE": env["CLAUDE_CODE_SIMPLE"]}, MainSessionID: sid, UUID: uuidMain, Commands: bindings, HTTP: httpBindings, PluginBaseAbsent: true, PolicySettings: func() (*protocol.JSONObject, error) { return settings.startup.GetSettingsForSource("policySettings") }})
		return e
	}
	yellow, e := nativeTmuxYellow(args, env, platform, stdio.Output)
	if e != nil {
		return worktree.TmuxStartupResult{}, e
	}
	files := nativeWorktreeFiles{n: n}
	var matcherMu sync.Mutex
	matchers := map[string]*ignorepattern.Matcher{}
	service, e := worktree.New(worktree.Options{Files: files, Environment: env, GitExecutable: worktree.NativeGitExecutable(env), Run: worktree.NativeRun, Now: n.Now, Log: func(level, message string) {
		if level == "error" {
			fmt.Fprintln(stdio.Error, message)
		}
	}, IncludeMatcher: func(content, path string) (bool, error) {
		matcherMu.Lock()
		defer matcherMu.Unlock()
		m := matchers[content]
		if m == nil {
			var e error
			m, e = ignorepattern.New(platform)
			if e == nil {
				e = m.Add(content)
			}
			if e != nil {
				return false, e
			}
			matchers[content] = m
		}
		result, e := m.Test(path)
		return result.Ignored, e
	}})
	if e != nil {
		return worktree.TmuxStartupResult{}, e
	}
	config := func() (worktree.Settings, error) {
		value, e := merged()
		if e != nil {
			return worktree.Settings{}, e
		}
		var c struct{ SparsePaths, SymlinkDirectories []string }
		if value != nil && value.Get("worktree") != nil {
			if e = json.Unmarshal(value.Get("worktree"), &c); e != nil {
				return worktree.Settings{}, e
			}
		}
		return worktree.Settings{SparsePaths: c.SparsePaths, SymlinkDirectories: c.SymlinkDirectories}, nil
	}
	return worktree.ExecIntoTmuxWorktree(ctx, args, worktree.TmuxStartupOptions{Platform: platform, CWD: n.CWD, Executable: executable, Environment: env, Output: stdio.Output, Yellow: yellow, CanonicalRoot: func(cwd string) string {
		return usercontext.CanonicalGitRoot(mainSettingsFiles{n: n}, filepath.EvalSymlinks, cwd)
	}, HasCreateHook: func() (bool, error) {
		if e := ensureHooks(); e != nil {
			return false, e
		}
		return hooks.HasWorktreeCreateHooks(sid)
	}, CreateHook: func(ctx context.Context, name string) (string, error) {
		var path string
		e := owner.Run(ctx, func(scope *cooperative.Scope) error {
			var e error
			path, e = hooks.ExecuteWorktreeCreateHook(ctx, name, sid, queryhooks.Context{Scope: scope, TrustAccepted: true})
			return e
		})
		return path, e
	}, GetOrCreate: func(ctx context.Context, root string, r worktree.Request) (worktree.GitWorktree, error) {
		c, e := config()
		if e != nil {
			return worktree.GitWorktree{}, e
		}
		r.Settings = c
		return service.GetOrCreateGitWorktree(ctx, root, r)
	}, PostSetup: func(ctx context.Context, root, path string) error {
		c, e := config()
		if e != nil {
			return e
		}
		return service.PostSetup(ctx, root, path, c)
	}, Run: func(ctx context.Context, r worktree.TmuxProcessRequest) (worktree.ProcessResult, error) {
		if !r.Inherit {
			return worktree.NativeRun(ctx, r.ProcessRequest)
		}
		p := ""
		for _, d := range filepath.SplitList(r.Environment["PATH"]) {
			x := filepath.Join(d, r.Command)
			if st, e := os.Stat(x); e == nil && !st.IsDir() && st.Mode()&0111 != 0 {
				p = x
				break
			}
		}
		if p == "" {
			return worktree.ProcessResult{Code: 1}, nil
		}
		cmd := exec.CommandContext(ctx, p, r.Arguments...)
		cmd.Dir = r.CWD
		keys := []string{}
		for k := range r.Environment {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			cmd.Env = append(cmd.Env, k+"="+r.Environment[k])
		}
		cmd.Stdin, cmd.Stdout, cmd.Stderr = stdio.Input, stdio.Output, stdio.Error
		e := cmd.Run()
		code := 0
		if e != nil {
			code = 1
			if x, ok := e.(*exec.ExitError); ok {
				code = x.ExitCode()
			}
		}
		return worktree.ProcessResult{Code: code}, nil
	}})
}

// Physical command/network work owns a future and a join reference until its
// immutable result is delivered. Await yields this outside-REPL actor; no ASK
// or Controller is created, and shutdown joins all native work before closing it.
func tmuxNativeWork[T any](owner *cooperative.Owner, scope *cooperative.Scope, work *sync.WaitGroup, run func() (T, error)) (*ownerasync.OwnerFuture[T], error) {
	if scope == nil || !scope.BelongsTo(owner) {
		return nil, cooperative.ErrRestricted
	}
	future, e := ownerasync.NewOwnerFuture[T](owner)
	if e != nil {
		return nil, e
	}
	work.Add(1)
	go func() {
		defer work.Done()
		value, failure := run()
		receipt, e := owner.SubmitIndependent(context.Background(), cooperative.Read, func(next *cooperative.Scope) error {
			if failure != nil {
				return future.Reject(next, failure)
			}
			return future.Resolve(next, value)
		})
		if e == nil {
			e = receipt.Wait()
		}
		if e != nil {
			_ = future.RejectAdministrative(e)
		}
	}()
	return future, nil
}
