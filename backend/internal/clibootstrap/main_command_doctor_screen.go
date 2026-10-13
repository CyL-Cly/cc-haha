package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliinstallation"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/installationdiagnostics"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sandboxruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
)

func mainDoctorDescriptionLimit(environment map[string]string) int {
	value := strings.TrimSpace(environment["CLAUDE_CODE_MAX_MCP_DESCRIPTION_LENGTH"])
	if value == "" {
		return 2048
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return 2048
		}
	}
	n, e := strconv.ParseUint(value, 10, 53)
	if e != nil || n == 0 {
		return 2048
	}
	return int(n)
}

func doctorContextWarningsText(result installationdiagnostics.ContextWarnings) string {
	var out strings.Builder
	out.WriteString("\nContext diagnostics\n")
	for _, warning := range []*installationdiagnostics.ContextWarning{result.Memory, result.Agents, result.MCP, result.Rules} {
		if warning == nil {
			continue
		}
		fmt.Fprintf(&out, "Warning: %s\n", warning.Message)
		for _, detail := range warning.Details {
			fmt.Fprintf(&out, "  %s\n", detail)
		}
	}
	return out.String()
}

// The public subcommand shares settings/memory/plugin/lock producers with
// /doctor, but retains Source's standalone AppState permission context. It
// borrows this actual command's owner; no ASK, Controller or transcript exists.
func (m *MainCommand) nativeDoctorScreen(ctx context.Context, scope *cooperative.Scope, in installationdiagnostics.ContextInput) (string, error) {
	if !scope.BelongsTo(m.Owner) {
		return "", cooperative.ErrRestricted
	}
	settings, e := m.Startup.GetInitialSettings()
	if e != nil {
		return "", e
	}
	if settings == nil {
		empty := protocol.NewJSONObject()
		settings = &empty
	}
	var refs ownerasync.NativeReferences
	binding, e := refs.Bind(m.Owner)
	if e != nil {
		return "", e
	}
	state, e := bootstrapstate.New(m.Owner, uuidMain(), m.Native.CWD, func() (string, error) { return uuidMain(), nil })
	if e != nil {
		return "", e
	}
	facade := &MainPrint{Owner: m.Owner, Global: m.Global, Startup: m.Startup, state: state, environment: m.Settings.environment, commandBinding: binding, options: MainPrintOptions{Native: m.Native, Platform: m.credentialPlatform, Arguments: PrintArguments{Sources: m.Settings.sources}}}
	user, e := m.Startup.GetSettingsForSource("userSettings")
	if e != nil {
		return "", e
	}
	mode := usercontext.InstructionFilesModeFromSettings(nil, nil, user)
	memoryWork, e := nativeSkillWork(facade, scope, func() ([]usercontext.MemoryFile, error) {
		memory, _, e := facade.nativeMemory()
		if e != nil {
			return nil, e
		}
		return memory.NormalMemoryFiles(mode, nil, false)
	})
	if e != nil {
		return "", e
	}
	in.Memory, e = memoryWork.Await(scope)
	if e != nil {
		return "", e
	}
	in.Permission = emptyMCPPermissionContext()
	if len(in.MCP) > 0 {
		selector, e := modelselection.NewParsing(modelselection.Options{Environment: m.Native.Environment, Settings: settings.Clone()})
		if e != nil {
			return "", e
		}
		selected, e := selector.Select()
		if e != nil {
			return "", e
		}
		var model string
		_ = json.Unmarshal(selected, &model)
		in.Count, e = m.nativeDoctorCounter(ctx, scope, binding, state, model, selector.SmallFastModel())
		if e != nil {
			return "", e
		}
	}
	var out strings.Builder
	out.WriteString(doctorContextWarningsText(installationdiagnostics.Context(in)))
	for _, warning := range installationdiagnostics.EnvironmentWarnings(m.Native.Environment) {
		fmt.Fprintf(&out, "Environment Variables\n%s\n", warning.Issue)
	}
	if parsed := m.Settings.settingsState.GetSessionSettingsCache(); parsed != nil {
		for _, failure := range parsed.Errors {
			fmt.Fprintf(&out, "Invalid Settings\n%s\n", string(failure))
		}
	}
	pluginWork, e := nativeSkillWork(facade, scope, func() (string, error) {
		plugins, e := m.Settings.LoadPlugins(ctx, "", false)
		if e != nil {
			return "", e
		}
		var result strings.Builder
		if len(plugins.Errors) > 0 {
			result.WriteString("\nPlugin Errors\n")
			for _, failure := range plugins.Errors {
				fmt.Fprintf(&result, "  %s: %s\n", failure.String("source"), pluginsapi.ErrorMessage(failure))
			}
		}
		return result.String(), nil
	})
	if e != nil {
		return "", e
	}
	pluginText, e := pluginWork.Await(scope)
	if e != nil {
		return "", e
	}
	out.WriteString(pluginText)
	sandbox := protocol.NewJSONObject()
	_ = sandbox.UnmarshalJSON(settings.Get("sandbox"))
	supported := m.credentialPlatform == "darwin" || m.credentialPlatform == "linux"
	if supported && string(sandbox.Get("enabled")) == "true" {
		checkWork, e := nativeSkillWork(facade, scope, func() (sandboxruntime.DependencyCheck, error) {
			check := sandboxruntime.DependencyCheck{Errors: []string{}, Warnings: []string{}}
			if _, _, e := NativeRipgrepCommand(m.Native); e != nil {
				check.Errors = append(check.Errors, "ripgrep (rg) not found")
			}
			return check, nil
		})
		if e != nil {
			return "", e
		}
		check, e := checkWork.Await(scope)
		if e != nil {
			return "", e
		}
		if m.credentialPlatform == "linux" {
			versionWork, e := nativeSkillWork(facade, scope, func() (string, error) { raw, _ := m.Native.readFile("/proc/version"); return string(raw), nil })
			if e != nil {
				return "", e
			}
			version, e := versionWork.Await(scope)
			if e != nil {
				return "", e
			}
			supported = sandboxruntime.LinuxSupported(version)
			if supported {
				prepared, e := facade.prepareNativeLinuxSandbox(scope, false)
				if e != nil {
					return "", e
				}
				linux := sandboxruntime.CheckLinuxDependencies(prepared.Status)
				check.Errors = append(check.Errors, linux.Errors...)
				check.Warnings = append(check.Warnings, linux.Warnings...)
			}
		}
		out.WriteString(nativeDoctorSandboxText(supported, true, check.Errors, check.Warnings))
	}
	installer, e := cliinstallation.New(cliinstallation.Options{Home: m.Native.Home, Environment: m.Native.Environment, Platform: m.credentialPlatform})
	if e != nil {
		return "", e
	}
	lockWork, e := nativeSkillWork(facade, scope, func() (cliinstallation.LockDiagnostic, error) { return installer.DiagnosticLocks(ctx), nil })
	if e != nil {
		return "", e
	}
	locks, e := lockWork.Await(scope)
	if e != nil {
		return "", e
	}
	if locks.Enabled {
		out.WriteString("\nVersion Locks\n")
		if locks.Cleaned > 0 {
			fmt.Fprintf(&out, "Cleaned %d stale lock(s)\n", locks.Cleaned)
		}
		if len(locks.Locks) == 0 {
			out.WriteString("No active version locks\n")
		}
		for _, lock := range locks.Locks {
			fmt.Fprintf(&out, "%s: PID %d, running %t, %s\n", lock.Version, lock.PID, lock.IsProcessRunning, lock.Executable)
		}
	}
	return out.String(), nil
}
