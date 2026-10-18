package clibootstrap

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sort"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ideintegration"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
	"github.com/NanmiCoder/cc-haha/backend/internal/terminalcapabilities"
	"github.com/NanmiCoder/cc-haha/backend/internal/terminalsetup"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func nativeTerminalSetupInitialTerminal(native *NativeBoundary) string {
	return ideintegration.Terminal(native.nativeEnvironmentSnapshot())
}

type nativeTerminalSetupPresentation struct {
	colorLevel        float64
	libraryHyperlinks bool
}

func nativeTerminalSetupInitialPresentation(options MainPrintOptions) nativeTerminalSetupPresentation {
	environment := options.Native.nativeEnvironmentSnapshot()
	release := ""
	if options.Platform == "win32" || options.Platform == "windows" {
		if information, err := nativeOSInfo(); err == nil {
			release = information.Release
		}
	}
	color := terminalcapabilities.ColorLibrary(environment, options.Platform, release, options.ForegroundShellProgress, options.Arguments.DebugArguments, false)
	hyperlinkColor := terminalcapabilities.ColorLibrary(environment, options.Platform, release, options.ForegroundShellProgress, options.Arguments.DebugArguments, true)
	return nativeTerminalSetupPresentation{
		colorLevel:        terminalcapabilities.InkColorLevel(environment, color.Level),
		libraryHyperlinks: terminalcapabilities.LibraryHyperlinks(environment, options.Platform, options.ForegroundShellProgress, hyperlinkColor.Supported, options.Arguments.DebugArguments),
	}
}

func (presentation nativeTerminalSetupPresentation) style(theme, role, text string) string {
	if role == "dim" {
		return terminalcapabilities.WrapColor(text, "\x1b[2m", "\x1b[22m", presentation.colorLevel)
	}
	value := tui.TerminalThemeRole(theme, role)
	open := tui.TerminalThemeRoleEscape(theme, role, false)
	var red, green, blue int
	if _, err := fmt.Sscanf(value, "rgb(%d,%d,%d)", &red, &green, &blue); err == nil {
		open = terminalcapabilities.RGBColorEscape(red, green, blue, presentation.colorLevel)
	}
	return terminalcapabilities.WrapColor(text, open, "\x1b[39m", presentation.colorLevel)
}

func (m *MainPrint) NativeTerminalSetupCompletion() tui.Completion {
	return tui.Completion{Name: "terminal-setup", Description: terminalsetup.Description(m.nativeTerminalSetupTerminal), Kind: "local-jsx", Hidden: terminalsetup.Hidden(m.nativeTerminalSetupTerminal)}
}

func (m *MainPrint) nativeTerminalSetupCommand(ctx context.Context, scope *cooperative.Scope, _ string) (string, error) {
	if m == nil || !scope.BelongsTo(m.Owner) || m.options.Native == nil || !m.options.Native.NativeProcess || !m.options.Interactive || m.agentChild || m.teamWorker != nil || m.Global == nil {
		return "", cooperative.ErrRestricted
	}
	native := m.options.Native
	environment := native.nativeEnvironmentSnapshot()
	cwd := m.state.Snapshot().CWD
	global, err := m.Global.Get()
	if err != nil {
		return "", err
	}
	theme := global.String("theme")
	configuration := terminalsetup.Options{Home: native.Home, Platform: m.options.Platform, Terminal: m.nativeTerminalSetupTerminal, Environment: environment, Files: mainResumeFiles{n: native}, Stat: native.stat, Onboarding: func() error { return nil }}
	configuration.GetFields = func() (protocol.JSONObject, error) {
		var result protocol.JSONObject
		receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(next *cooperative.Scope) error {
			if !next.BelongsTo(m.Owner) {
				return cooperative.ErrRestricted
			}
			current, err := m.Global.Get()
			if err == nil {
				result = current.Clone()
			}
			return err
		})
		if err == nil {
			err = receipt.Wait()
		}
		return result, err
	}
	configuration.SaveFields = func(fields protocol.JSONObject) error {
		receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(next *cooperative.Scope) error {
			if !next.BelongsTo(m.Owner) {
				return cooperative.ErrRestricted
			}
			return m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
				updated := current.Clone()
				updated.Assign(fields)
				return &updated, nil
			})
		})
		if err == nil {
			err = receipt.Wait()
		}
		return err
	}
	configuration.LogError = func(failure error) {
		receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(next *cooperative.Scope) error {
			return m.nativeLogError(context.WithValue(ctx, nativeOpenAIAuthScopeKey{}, next), failure)
		})
		if err == nil {
			_ = receipt.Wait()
		}
	}
	configuration.Execute = func(parent context.Context, name string, args []string) terminalsetup.Execution {
		ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
		defer cancel()
		if err := native.check("execFileNoThrow", name); err != nil {
			return terminalsetup.Execution{Code: 1, Stderr: err.Error()}
		}
		executable, err := ideintegration.ResolveNativeExecutable(name, cwd, environment)
		if err != nil {
			return terminalsetup.Execution{Code: 1, Stderr: err.Error()}
		}
		command := exec.CommandContext(ctx, executable, args...)
		command.Dir = cwd
		for key, value := range environment {
			command.Env = append(command.Env, key+"="+value)
		}
		sort.Strings(command.Env)
		var stdout, stderr bytes.Buffer
		command.Stdout, command.Stderr = &stdout, &stderr
		code := 0
		if err := command.Run(); err != nil {
			code = 1
			if exit, ok := err.(*exec.ExitError); ok {
				code = exit.ExitCode()
			}
		}
		return terminalsetup.Execution{Code: code, Stdout: string(protocol.DecodeJavaScriptUTF8(stdout.Bytes())), Stderr: string(protocol.DecodeJavaScriptUTF8(stderr.Bytes()))}
	}
	configuration.Style = func(role, text string) string { return m.nativeTerminalSetupPresentation.style(theme, role, text) }
	configuration.Hyperlinks = terminalcapabilities.SupportsHyperlinks(environment, m.nativeTerminalSetupPresentation.libraryHyperlinks)
	future, err := nativeSkillWork(m, scope, func() (string, error) { return terminalsetup.Run(ctx, configuration) })
	if err != nil {
		return "", err
	}
	output, err := future.Await(scope)
	if err == nil && terminalsetup.Offered(configuration.Terminal, configuration.Platform) {
		err = m.nativeMaybeMarkProjectOnboardingComplete(ctx, skillcommands.Invocation{NativeScope: scope})
	}
	return output, err
}
