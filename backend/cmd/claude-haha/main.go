package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"github.com/NanmiCoder/cc-haha/backend/internal/application"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliargs"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/curepl"
	"github.com/NanmiCoder/cc-haha/backend/internal/recovery"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/adapterlauncher"
	"github.com/NanmiCoder/cc-haha/backend/internal/storagemetadata"
	"github.com/NanmiCoder/cc-haha/backend/internal/voice"
)

// Production CLI and desktop server entry points execute this Go binary.
// Explicit capability errors remain for runtime behavior still being migrated.
var exit = os.Exit

// Release builds inject the same frozen version as the Source launcher.
var version = "999.0.0-local"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	code := run(ctx, os.Args[1:])
	exit(code)
}

func run(ctx context.Context, args []string) int {
	if code, handled := runChromeFastPath(ctx, args); handled {
		return code
	}
	if len(args) > 0 && args[0] == "--computer-use-mcp" {
		return runComputerUseMCP(ctx)
	}
	if len(args) == 1 && args[0] == "--storage-relocation-worker" {
		if err := storagemetadata.RunWorker(ctx, os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}
	if len(args) == 1 && args[0] == "--voice-worker" {
		environment := processEnvironment()
		_ = os.Unsetenv(voice.WorkerConfigEnv)
		_ = os.Unsetenv(voice.WorkerTokenEnv)
		if err := voice.RunWorker(ctx, environment, os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}
	if len(args) == 1 && args[0] == "--computer-use-repl-worker" {
		if err := curepl.RunWorker(ctx, os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}
	if len(args) > 0 && args[0] == "adapters" {
		env := processEnvironment()
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		home := env["HOME"]
		if runtime.GOOS == "windows" && env["USERPROFILE"] != "" {
			home = env["USERPROFILE"]
		}
		code, err := adapterlauncher.Run(ctx, args[1:], adapterlauncher.Options{Environment: env, HomeDir: home, WorkDir: cwd, Stderr: os.Stderr, Stdout: os.Stdout, StartTelegram: startNativeTelegram, StartSlack: startNativeSlack, StartWechat: startNativeWechat, StartWecom: startNativeWecom, StartQQ: startNativeQQ, StartDingTalk: startNativeDingTalk, StartFeishu: startNativeFeishu, StartWhatsApp: startNativeWhatsApp})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return code
	}
	if len(args) > 0 && args[0] == "server" {
		env := processEnvironment()
		cwd, err := os.Getwd()
		if err == nil {
			err = application.RunServer(ctx, args[1:], env, cwd, version, os.Stderr, newNativeBoundaryForEnvironment)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}
	if len(args) > 0 && (args[0] == "cli" || args[0] == "agent") {
		args = args[1:]
		if len(args) > 0 && args[0] == "--app-root" {
			if len(args) < 2 {
				fmt.Fprintln(os.Stderr, "--app-root requires a path")
				return 1
			}
			args = args[2:]
		}
	}
	if os.Getenv("CLAUDE_CODE_FORCE_RECOVERY_CLI") != "1" {
		// The Source single-argument version route precedes main imports,
		// configuration, managed policy, keychain and preAction initialization.
		if len(args) == 1 && (args[0] == "--version" || args[0] == "-v" || args[0] == "-V") {
			fmt.Fprintln(os.Stdout, version+" (Claude Code)")
			return 0
		}
		if code, handled := runTmuxWorktreeFastPath(ctx, args); handled {
			return code
		}
		if code, handled := earlySettingsFailure(args); handled {
			return code
		}
		if parsed, handled := clibootstrap.AgentsArguments(args, version); handled && !isPrintInvocation(args) {
			if parsed.Parsed == nil {
				fmt.Fprint(os.Stdout, parsed.Stdout)
				fmt.Fprint(os.Stderr, parsed.Stderr)
				return parsed.ExitCode
			}
			native, e := newMainAgentsNativeBoundary()
			if e != nil {
				fmt.Fprintln(os.Stderr, e)
				return 1
			}
			stat, e := os.Stdout.Stat()
			nonInteractive := e != nil || stat.Mode()&os.ModeCharDevice == 0
			process, e := clibootstrap.NewMainAgentsContextWithInput(ctx, args, native, os.Getpid(), nonInteractive, os.Stdout, os.Stdin)
			if e != nil {
				fmt.Fprintln(os.Stderr, e)
				return 1
			}
			defer process.Close()
			if e := process.BeginNativeAPIPreconnect(ctx); e != nil {
				fmt.Fprintln(os.Stderr, e)
				return 1
			}
			code, e := process.Run(ctx)
			if e != nil {
				fmt.Fprintln(os.Stderr, e)
				return 1
			}
			return code
		}
		parsedCLI := cliargs.Parse(args, cliargs.Config{Version: version})
		if parsedCLI.Parsed == nil {
			fmt.Fprint(os.Stdout, parsedCLI.Stdout)
			fmt.Fprint(os.Stderr, parsedCLI.Stderr)
			return parsedCLI.ExitCode
		}
		defer holdInstalledCLIVersion()()
		if strings.HasPrefix(parsedCLI.Parsed.Path, "claude plugin ") {
			return runPluginCommand(ctx, args, parsedCLI.Parsed)
		}
		if parsedCLI.Parsed.Path == "claude setup-token" {
			return runSetupTokenCommand(ctx, args)
		}
		if strings.HasPrefix(parsedCLI.Parsed.Path, "claude auth ") {
			return runAuthCommand(ctx, args, parsedCLI.Parsed)
		}
		if strings.HasPrefix(parsedCLI.Parsed.Path, "claude mcp ") {
			return runMCPCommand(ctx, args, *parsedCLI.Parsed)
		}
		if strings.HasPrefix(parsedCLI.Parsed.Path, "claude auto-mode ") {
			return runAutoModeCommand(ctx, args, parsedCLI.Parsed)
		}
		if parsedCLI.Parsed.Path == "claude install" || parsedCLI.Parsed.Path == "claude update" || parsedCLI.Parsed.Path == "claude doctor" {
			return runInstallationCommand(ctx, args, parsedCLI.Parsed)
		}
		if parsedCLI.Parsed.Path == "claude" {
			cwd, err := os.Getwd()
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			command, _, e := application.ParseCommand(args, version, cwd, os.TempDir(), processEnvironment())
			if e != nil {
				_ = application.WriteCommandError(os.Stderr, e)
				return 1
			}
			native, err := newMainNativeBoundary()
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			if native.NativeProcess {
				code, e := application.RunCommand(ctx, command, application.Options{Native: native, Environment: native.Environment, WorkDir: native.CWD, Version: version, Platform: runtime.GOOS, Stderr: os.Stderr}, os.Stdin, os.Stdout, os.Environ())
				if e != nil {
					_ = application.WriteCommandError(os.Stderr, e)
					return 1
				}
				return code
			}
			parsed, err := clibootstrap.ParsePrintArguments(args, version)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			process, err := clibootstrap.NewMainPrint(clibootstrap.MainPrintOptions{PublicPrintLoadErrors: true, Arguments: parsed, Native: native, PID: os.Getpid(), Version: version, Platform: runtime.GOOS, Input: os.Stdin, Output: os.Stdout, Error: os.Stderr})
			if err != nil {
				writeNativeObservation(native)
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			defer process.Close()
			code, err := process.Run(ctx)
			writeNativeObservation(native)
			if handled, failure := clibootstrap.WritePrintLoadError(os.Stdout, os.Stderr, "stream-json", err); handled {
				if failure != nil {
					fmt.Fprintln(os.Stderr, failure)
				}
				return 1
			}
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			return code
		}
		fmt.Fprintln(os.Stderr, "Go CLI action is not migrated: "+parsedCLI.Parsed.Path)
		return 1
	}
	stat, err := os.Stdin.Stat()
	isTTY := err == nil && stat.Mode()&os.ModeCharDevice != 0
	code, err := recovery.Run(ctx, args, recovery.IO{In: os.Stdin, Out: os.Stdout, Err: os.Stderr, InputIsTTY: isTTY})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return code
}

func processEnvironment() map[string]string {
	env := map[string]string{}
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			env[key] = value
		}
	}
	return env
}

func isPrintInvocation(args []string) bool {
	for _, s := range args {
		if s == "--print" || s == "-p" {
			return true
		}
	}
	return false
}
