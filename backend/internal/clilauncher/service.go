// Package clilauncher installs the ordinary Go CLI from the actual desktop sidecar.
package clilauncher

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const command = "claude-haha"
const pathStart = "# >>> Claude Code Haha PATH >>>"
const pathEnd = "# <<< Claude Code Haha PATH <<<"

//go:embed windows-path.ps1
var windowsPathScript string

type Status struct {
	Supported               bool    `json:"supported"`
	Command                 string  `json:"command"`
	Installed               bool    `json:"installed"`
	LauncherPath            string  `json:"launcherPath"`
	BinDir                  string  `json:"binDir"`
	PathConfigured          bool    `json:"pathConfigured"`
	PathInCurrentShell      bool    `json:"pathInCurrentShell"`
	AvailableInNewTerminals bool    `json:"availableInNewTerminals"`
	NeedsTerminalRestart    bool    `json:"needsTerminalRestart"`
	ConfigTarget            *string `json:"configTarget"`
	LastError               *string `json:"lastError"`
}
type ExecResult struct {
	Code           int
	Stdout, Stderr string
	Error          error
}
type Options struct {
	HomeDir, Executable, Platform string
	Environment                   func() map[string]string
	Run                           func(context.Context, string, []string) ExecResult
	Now                           func() time.Time
	PID                           int
}
type ensureFlight struct {
	done   chan struct{}
	result Status
}
type Service struct {
	o        Options
	mu       sync.Mutex
	inflight *ensureFlight
	startup  sync.Once
	jobs     sync.WaitGroup
}

func New(o Options) (*Service, error) {
	if !filepath.IsAbs(o.HomeDir) || o.Environment == nil {
		return nil, errors.New("CLI launcher requires explicit home and environment")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.PID == 0 {
		o.PID = os.Getpid()
	}
	if o.Run == nil {
		o.Run = func(ctx context.Context, name string, args []string) ExecResult {
			cmd := exec.CommandContext(ctx, name, args...)
			env := o.Environment()
			for k, v := range env {
				cmd.Env = append(cmd.Env, k+"="+v)
			}
			var out, errout strings.Builder
			cmd.Stdout = &out
			cmd.Stderr = &errout
			e := cmd.Run()
			code := 0
			if e != nil {
				code = -1
				var exit *exec.ExitError
				if errors.As(e, &exit) {
					code = exit.ExitCode()
				}
			}
			return ExecResult{code, out.String(), errout.String(), e}
		}
	}
	return &Service{o: o}, nil
}
func (s *Service) Ensure(ctx context.Context) Status {
	s.mu.Lock()
	if pending := s.inflight; pending != nil {
		s.mu.Unlock()
		<-pending.done
		return pending.result
	}
	pending := &ensureFlight{done: make(chan struct{})}
	s.inflight = pending
	s.mu.Unlock()
	result := s.ensure(context.WithoutCancel(ctx))
	s.mu.Lock()
	pending.result = result
	s.inflight = nil
	close(pending.done)
	s.mu.Unlock()
	return result
}
func sourcePath(env map[string]string, exe string) string {
	path := env["CLAUDE_CLI_PATH"]
	if path == "" {
		name := filepath.Base(exe)
		if strings.HasPrefix(name, "claude-sidecar") {
			path = exe
		} else if strings.HasPrefix(name, "claude-server") {
			candidate := filepath.Join(filepath.Dir(exe), strings.Replace(name, "claude-server", "claude-cli", 1))
			if _, e := os.Stat(candidate); e == nil {
				path = candidate
			}
		}
	}
	if strings.HasPrefix(filepath.Base(path), "claude-sidecar") && !regexp.MustCompile(`(?i)\.(?:[cm]?[jt]s|tsx?)$`).MatchString(path) {
		return path
	}
	return ""
}
func (s *Service) ensure(ctx context.Context) Status {
	env := s.o.Environment()
	home := env["HOME"]
	if home == "" {
		home = env["USERPROFILE"]
	}
	if home == "" {
		home = s.o.HomeDir
	}
	bin := filepath.Join(home, ".local", "bin")
	name := command
	if s.o.Platform == "windows" {
		name += ".cmd"
	}
	result := Status{Command: command, LauncherPath: filepath.Join(bin, name), BinDir: bin, PathInCurrentShell: PathPresent(env["PATH"], bin, s.o.Platform, home)}
	source := sourcePath(env, s.o.Executable)
	if source == "" {
		return result
	}
	result.Supported = true
	if e := s.syncLauncher(source, result.LauncherPath, env); e != nil {
		message := e.Error()
		result.LastError = &message
	}
	if stat, e := os.Stat(result.LauncherPath); e == nil {
		result.Installed = stat.Mode().IsRegular() && stat.Size() > 0
	}
	configured, target, last := s.ensurePath(ctx, env, bin, home)
	result.PathConfigured = result.PathInCurrentShell || configured
	result.ConfigTarget = target
	if result.LastError == nil {
		result.LastError = last
	}
	result.AvailableInNewTerminals = result.Installed && (result.PathInCurrentShell || result.PathConfigured)
	result.NeedsTerminalRestart = result.AvailableInNewTerminals && !result.PathInCurrentShell
	return result
}
func (s *Service) syncLauncher(source, target string, env map[string]string) error {
	if e := os.MkdirAll(filepath.Dir(target), 0755); e != nil {
		return e
	}
	wrapper := UnixWrapper(source, env["CLAUDE_CONFIG_DIR"])
	if s.o.Platform == "windows" {
		wrapper = WindowsWrapper(source, env["CLAUDE_CONFIG_DIR"])
	}
	old, e := os.ReadFile(target)
	if e == nil && string(old) == wrapper {
		if s.o.Platform != "windows" {
			return os.Chmod(target, 0755)
		}
		return nil
	}
	temp := fmt.Sprintf("%s.tmp.%d.%d", target, s.o.PID, s.o.Now().UnixMilli())
	if e = os.WriteFile(temp, []byte(wrapper), 0755); e != nil {
		return e
	}
	defer os.Remove(temp)
	if s.o.Platform == "windows" {
		_ = os.Remove(target)
	}
	if e = os.Rename(temp, target); e != nil {
		if s.o.Platform != "windows" {
			return e
		}
		backup := fmt.Sprintf("%s.old.%d", target, s.o.Now().UnixMilli())
		_ = os.Rename(target, backup)
		if e = os.Rename(temp, target); e != nil {
			return e
		}
		_ = os.Remove(backup)
	}
	if s.o.Platform == "windows" {
		legacy := filepath.Join(filepath.Dir(target), command+".exe")
		if stat, e := os.Stat(legacy); e == nil && stat.Mode().IsRegular() {
			if e = os.Remove(legacy); e != nil {
				if renamed := os.Rename(legacy, fmt.Sprintf("%s.old.%d", legacy, s.o.Now().UnixMilli())); renamed != nil {
					return fmt.Errorf("failed to remove legacy Windows launcher %s: %v", legacy, renamed)
				}
			}
		}
		return nil
	}
	return os.Chmod(target, 0755)
}
func (s *Service) ensurePath(ctx context.Context, env map[string]string, bin, home string) (bool, *string, *string) {
	if s.o.Platform == "windows" {
		target := "Windows User PATH"
		r := s.o.Run(ctx, "powershell.exe", []string{"-NoProfile", "-NonInteractive", "-Command", "[Environment]::GetEnvironmentVariable('Path', 'User')"})
		userPath := ""
		if r.Code == 0 {
			userPath = trimJS(r.Stdout)
		}
		if PathPresent(userPath, bin, "windows", home) {
			return true, &target, nil
		}
		r = s.o.Run(ctx, "powershell.exe", []string{"-NoProfile", "-NonInteractive", "-Command", windowsPathScript})
		if r.Code == 0 {
			return true, &target, nil
		}
		failure := trimJS(r.Stderr)
		if failure == "" {
			failure = trimJS(r.Stdout)
		}
		if failure == "" && r.Error != nil {
			failure = r.Error.Error()
		}
		if failure == "" {
			failure = "Failed to update Windows user PATH"
		}
		return false, &target, &failure
	}
	shell := ""
	for _, candidate := range []string{"fish", "bash", "zsh"} {
		if strings.Contains(env["SHELL"], candidate) {
			shell = candidate
			break
		}
	}
	if shell == "" {
		shell = "bash"
		if s.o.Platform == "darwin" {
			shell = "zsh"
		}
	}
	target := filepath.Join(home, ".bashrc")
	if shell == "fish" {
		target = filepath.Join(home, ".config", "fish", "config.fish")
	}
	if shell == "zsh" {
		root := env["ZDOTDIR"]
		if root == "" {
			root = home
		}
		target = filepath.Join(root, ".zshrc")
	}
	old, _ := os.ReadFile(target)
	next := UpsertPathBlock(string(old), ManagedPathBlock(shell, bin, home))
	if next != string(old) {
		e := os.MkdirAll(filepath.Dir(target), 0755)
		if e == nil {
			e = os.WriteFile(target, []byte(next), 0666)
		}
		if e != nil {
			failure := e.Error()
			return false, &target, &failure
		}
	}
	return true, &target, nil
}

// Start owns the same one asynchronous bundled-launcher preparation as Source.
func (s *Service) Start() {
	s.startup.Do(func() { s.jobs.Add(1); go func() { defer s.jobs.Done(); s.Ensure(context.Background()) }() })
}
func (s *Service) Close(ctx context.Context) error {
	done := make(chan struct{})
	go func() { s.jobs.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
