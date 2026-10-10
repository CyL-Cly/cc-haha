package worktree

import (
	"context"
	"errors"
	"fmt"
	"github.com/dop251/goja"
	"io"
	"math"
	"math/rand/v2"
	"path/filepath"
	"regexp"
	"strings"
)

type TmuxProcessRequest struct {
	ProcessRequest
	Inherit bool
}
type TmuxStartupOptions struct {
	Platform, CWD, Executable string
	Environment               map[string]string
	Output                    io.Writer
	Run                       func(context.Context, TmuxProcessRequest) (ProcessResult, error)
	CanonicalRoot             func(string) string
	HasCreateHook             func() (bool, error)
	CreateHook                func(context.Context, string) (string, error)
	GetOrCreate               func(context.Context, string, Request) (GitWorktree, error)
	PostSetup                 func(context.Context, string, string) error
	Random                    func() float64
	ITerm2                    func() bool
	Yellow                    func(string) string
}
type TmuxStartupResult struct {
	Handled bool
	Error   string
}

func HasTmuxWorktreeFastPath(args []string) bool {
	tmux, tree := false, false
	for _, arg := range args {
		if arg == "--tmux" || arg == "--tmux=classic" {
			tmux = true
		}
		if arg == "-w" || arg == "--worktree" || strings.HasPrefix(arg, "--worktree=") {
			tree = true
		}
	}
	return tmux && tree
}

var tmuxPrefixPattern = regexp.MustCompile(`prefix\s+(\S+)`)

func randomTmuxSlug(random func() float64) (string, error) {
	adjectives := []string{"swift", "bright", "calm", "keen", "bold"}
	nouns := []string{"fox", "owl", "elm", "oak", "ray"}
	a, b, c := random(), random(), random()
	for _, v := range []float64{a, b, c} {
		if v < 0 || v >= 1 || math.IsNaN(v) {
			return "", errors.New("actual Math.random domain required")
		}
	}
	vm := goja.New()
	value := vm.ToValue(c)
	fn, ok := goja.AssertFunction(value.ToObject(vm).Get("toString"))
	if !ok {
		return "", errors.New("number radix formatter unavailable")
	}
	text, e := fn(value, vm.ToValue(36))
	if e != nil {
		return "", e
	}
	suffix := text.String()
	if len(suffix) > 2 {
		suffix = suffix[2:]
	} else {
		suffix = ""
	}
	if len(suffix) > 4 {
		suffix = suffix[:4]
	}
	return adjectives[int(a*5)] + "-" + nouns[int(b*5)] + "-" + suffix, nil
}

// ExecIntoTmuxWorktree is the original pre-CLI fast path. It creates no Agent,
// Controller, session transcript, ASK, authentication or model request.
func ExecIntoTmuxWorktree(ctx context.Context, args []string, o TmuxStartupOptions) (TmuxStartupResult, error) {
	failed := func(message string) (TmuxStartupResult, error) { return TmuxStartupResult{Error: message}, nil }
	if o.Platform == "win32" || o.Platform == "windows" {
		return failed("Error: --tmux is not supported on Windows")
	}
	if o.Run == nil || o.Output == nil || o.CanonicalRoot == nil || o.HasCreateHook == nil || o.CreateHook == nil || o.GetOrCreate == nil || o.PostSetup == nil || o.Executable == "" {
		return TmuxStartupResult{}, &CapabilityError{"actual tmux fast-path producers"}
	}
	if o.Yellow == nil {
		o.Yellow = func(text string) string { return text }
	}
	if o.Random == nil {
		o.Random = rand.Float64
	}
	if o.ITerm2 == nil {
		o.ITerm2 = func() bool {
			return IsTmuxITerm2(o.Environment)
		}
	}
	run := func(arguments []string, cwd string, env map[string]string, inherit bool) (ProcessResult, error) {
		return o.Run(ctx, TmuxProcessRequest{ProcessRequest: ProcessRequest{Command: "tmux", Arguments: arguments, CWD: cwd, Environment: env}, Inherit: inherit})
	}
	checked, e := run([]string{"-V"}, o.CWD, o.Environment, false)
	if e != nil {
		return TmuxStartupResult{}, e
	}
	if checked.Code != 0 {
		hint := "Install tmux with: sudo apt install tmux"
		if o.Platform == "darwin" {
			hint = "Install tmux with: brew install tmux"
		}
		return failed("Error: tmux is not installed. " + hint)
	}
	name, classic := "", false
	for i, arg := range args {
		switch {
		case arg == "-w" || arg == "--worktree":
			if i+1 < len(args) && args[i+1] != "" && !strings.HasPrefix(args[i+1], "-") {
				name = args[i+1]
			}
		case strings.HasPrefix(arg, "--worktree="):
			name = strings.TrimPrefix(arg, "--worktree=")
		case arg == "--tmux=classic":
			classic = true
		}
	}
	pr, e := ParsePRReference(name)
	if e != nil {
		return TmuxStartupResult{}, e
	}
	if pr != nil {
		name = "pr-" + PRNumberString(*pr)
	}
	if name == "" {
		name, e = randomTmuxSlug(o.Random)
		if e != nil {
			return TmuxStartupResult{}, e
		}
	}
	if e = ValidateSlug(name); e != nil {
		return failed("Error: " + e.Error())
	}
	hook, e := o.HasCreateHook()
	if e != nil {
		return TmuxStartupResult{}, e
	}
	var dir, repoName string
	if hook {
		dir, e = o.CreateHook(ctx, name)
		if e != nil {
			return failed("Error: " + e.Error())
		}
		root := o.CanonicalRoot(o.CWD)
		if root == "" {
			root = o.CWD
		}
		repoName = filepath.Base(root)
		fmt.Fprintln(o.Output, "Using worktree via hook: "+dir)
	} else {
		root := o.CanonicalRoot(o.CWD)
		if root == "" {
			return failed("Error: --worktree requires a git repository")
		}
		repoName = filepath.Base(root)
		dir = PathFor(root, name)
		result, err := o.GetOrCreate(ctx, root, Request{CWD: root, Slug: name, PRNumber: pr})
		if err != nil {
			return failed("Error: " + err.Error())
		}
		if !result.Existed {
			fmt.Fprintf(o.Output, "Created worktree: %s (based on %s)\n", dir, result.BaseBranch)
			if e = o.PostSetup(ctx, root, dir); e != nil {
				return failed("Error: " + e.Error())
			}
		}
	}
	session := strings.NewReplacer("/", "_", ".", "_").Replace(repoName + "_" + BranchName(name))
	inner := []string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "" || arg == "--tmux" || arg == "--tmux=classic" || strings.HasPrefix(arg, "--worktree=") {
			continue
		}
		if arg == "-w" || arg == "--worktree" {
			if i+1 < len(args) && args[i+1] != "" && !strings.HasPrefix(args[i+1], "-") {
				i++
			}
			continue
		}
		inner = append(inner, arg)
	}
	prefix := "C-b"
	value, e := run([]string{"show-options", "-g", "prefix"}, o.CWD, o.Environment, false)
	if e != nil {
		return TmuxStartupResult{}, e
	}
	if value.Code == 0 && value.Stdout != "" {
		if match := tmuxPrefixPattern.FindStringSubmatch(value.Stdout); len(match) > 1 {
			prefix = match[1]
		}
	}
	conflicts := false
	for _, binding := range []string{"C-b", "C-c", "C-d", "C-t", "C-o", "C-r", "C-s", "C-g", "C-e"} {
		if prefix == binding {
			conflicts = true
		}
	}
	env := map[string]string{}
	for k, v := range o.Environment {
		env[k] = v
	}
	env["CLAUDE_CODE_TMUX_SESSION"] = session
	env["CLAUDE_CODE_TMUX_PREFIX"] = prefix
	env["CLAUDE_CODE_TMUX_PREFIX_CONFLICTS"] = ""
	if conflicts {
		env["CLAUDE_CODE_TMUX_PREFIX_CONFLICTS"] = "1"
	}
	exists, e := run([]string{"has-session", "-t", session}, o.CWD, o.Environment, false)
	if e != nil {
		return TmuxStartupResult{}, e
	}
	present := exists.Code == 0
	nested := o.Environment["TMUX"] != ""
	control := o.ITerm2() && !classic && !nested
	global := []string{}
	if control {
		global = append(global, "-CC")
	}
	if control && !present {
		y := o.Yellow
		fmt.Fprintln(o.Output, "\n"+y("╭─ iTerm2 Tip ────────────────────────────────────────────────────────╮")+"\n"+y("│")+" To open as a tab instead of a new window:                           "+y("│")+"\n"+y("│")+" iTerm2 > Settings > General > tmux > \"Tabs in attaching window\"     "+y("│")+"\n"+y("╰─────────────────────────────────────────────────────────────────────╯")+"\n")
	}
	launch := func(arguments []string, cwd string, env map[string]string, inherit bool) error {
		_, e := run(arguments, cwd, env, inherit)
		return e
	}
	create := append([]string{"new-session", "-d", "-s", session, "-c", dir, "--", o.Executable}, inner...)
	if o.Environment["USER_TYPE"] == "ant" && repoName == "claude-cli-internal" && !present {
		if e = launch(create, dir, env, false); e != nil {
			return TmuxStartupResult{}, e
		}
		for _, command := range [][]string{{"split-window", "-h", "-t", session, "-c", dir}, {"send-keys", "-t", session, "bun run watch", "Enter"}, {"split-window", "-v", "-t", session, "-c", dir}, {"send-keys", "-t", session, "bun run start"}, {"select-pane", "-t", session + ":0.0"}} {
			if e = launch(command, dir, o.Environment, false); e != nil {
				return TmuxStartupResult{}, e
			}
		}
		if nested {
			e = launch([]string{"switch-client", "-t", session}, o.CWD, o.Environment, true)
		} else {
			e = launch(append(global, "attach-session", "-t", session), dir, o.Environment, true)
		}
	} else if nested {
		if !present {
			if e = launch(create, dir, env, false); e != nil {
				return TmuxStartupResult{}, e
			}
		}
		e = launch([]string{"switch-client", "-t", session}, o.CWD, o.Environment, true)
	} else {
		command := append(append([]string{}, global...), "new-session", "-A", "-s", session, "-c", dir, "--", o.Executable)
		command = append(command, inner...)
		e = launch(command, dir, env, true)
	}
	if e != nil {
		return TmuxStartupResult{}, e
	}
	return TmuxStartupResult{Handled: true}, nil
}
