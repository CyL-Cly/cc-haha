package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/installationdiagnostics"
)

// NativeInstallationDiagnostic snapshots configuration on its owner, then
// joins the native read/process probes before publishing the Doctor result.
func (m *MainCommand) NativeInstallationDiagnostic(ctx context.Context, scope *cooperative.Scope) (installationdiagnostics.Diagnostic, error) {
	if m == nil || !scope.BelongsTo(m.Owner) || !m.Native.NativeProcess {
		return installationdiagnostics.Diagnostic{}, cooperative.ErrRestricted
	}
	global, e := m.Global.Get()
	if e != nil {
		return installationdiagnostics.Diagnostic{}, e
	}
	settings, e := m.Settings.InitialSettings()
	if e != nil {
		return installationdiagnostics.Diagnostic{}, e
	}
	env := map[string]string{}
	for key, value := range m.Native.Environment {
		env[key] = value
	}
	platform := m.credentialPlatform
	if platform == "darwin" {
		platform = "macos"
	}
	executable, e := os.Executable()
	if e != nil {
		return installationdiagnostics.Diagnostic{}, e
	}
	packageName, specified := env["CLAUDE_CODE_LOCAL_PACKAGE_URL"]
	if !specified {
		packageName = "claude-code-local"
	}
	options := installationdiagnostics.Options{PackageOptions: installationdiagnostics.PackageOptions{Platform: platform, Executable: executable, Argv0: os.Args[0], ReadFile: m.Native.readFile, Run: installationdiagnostics.NativeRunner(m.Native.CWD, env)}, Home: m.Native.Home, CWD: m.Native.CWD, ConfigDir: env["CLAUDE_CONFIG_DIR"], ManagedRoot: m.Native.ManagedRoot, Version: m.version, PackageName: packageName, Bundled: true, Environment: env, Stat: m.Native.stat, Realpath: filepath.EvalSymlinks, Which: installationdiagnostics.NativeWhich(env, m.Native.CWD)}
	if m.doctorPackages == nil {
		m.doctorPackages = installationdiagnostics.NewPackages(options.PackageOptions)
	}
	options.Packages = m.doctorPackages
	if global != nil {
		options.Global = global.Clone()
	}
	if settings != nil {
		options.LinuxGlobPatterns = installationdiagnostics.LinuxGlobPatterns(settings.Clone())
	}
	data, e := scope.AwaitBytes(cooperative.TranscriptOpenStat, func(worker context.Context) ([]byte, error) {
		command, args, err := NativeRipgrepCommand(m.Native)
		options.Ripgrep = installationdiagnostics.RipgrepStatus{Working: err == nil, Mode: "system"}
		if err == nil {
			if len(args) > 0 && args[0] == "--no-config" {
				options.Ripgrep.Mode = "embedded"
			} else if strings.Contains(filepath.ToSlash(command), "/vendor/ripgrep/") {
				options.Ripgrep.Mode = "builtin"
			} else {
				options.Ripgrep.SystemPath = &command
			}
		}
		return json.Marshal(installationdiagnostics.New(options).Diagnose(worker))
	})
	if e != nil {
		return installationdiagnostics.Diagnostic{}, e
	}
	var result installationdiagnostics.Diagnostic
	e = json.Unmarshal(data, &result)
	return result, e
}
func (m *MainCommand) nativeDoctorDiagnosticText(ctx context.Context, scope *cooperative.Scope) (string, error) {
	d, e := m.NativeInstallationDiagnostic(ctx, scope)
	if e != nil {
		return "", e
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Diagnostics\n\nCurrently running: %s (%s)\nInstallation path: %s\nInvoked binary: %s\nConfig install method: %s\nAuto-updates: %s\n", map[string]string{"native": "Go native", "package-manager": "Go package-manager", "development": "Go development"}[d.InstallationType], d.Version, d.InstallationPath, d.InvokedBinary, d.ConfigInstallMethod, d.AutoUpdates)
	if d.PackageManager != "" {
		fmt.Fprintf(&out, "Package manager: %s\n", d.PackageManager)
	}
	if d.HasUpdatePermissions != nil {
		fmt.Fprintf(&out, "Update permissions: %t\n", *d.HasUpdatePermissions)
	}
	fmt.Fprintf(&out, "Ripgrep: %s (%s)\n", map[bool]string{true: "OK", false: "Not working"}[d.Ripgrep.Working], d.Ripgrep.Mode)
	if d.Ripgrep.SystemPath != nil {
		fmt.Fprintf(&out, "Ripgrep path: %s\n", *d.Ripgrep.SystemPath)
	}
	for _, i := range d.MultipleInstallations {
		fmt.Fprintf(&out, "Other installation: %s at %s\n", i.Type, i.Path)
	}
	for _, warning := range d.Warnings {
		fmt.Fprintf(&out, "Warning: %s\nFix: %s\n", warning.Issue, warning.Fix)
	}
	return out.String(), nil
}
