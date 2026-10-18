package clibootstrap

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sandboxruntime"
)

type nativeSandboxRuntime struct {
	linux               nativeLinuxSandboxPreparation
	manager             *sandboxruntime.Manager
	owner               *MainPrint
	platform            string
	supported           bool
	ripgrep             protocol.JSONObject
	dependencies        []string
	initial             protocol.JSONObject
	temp                string
	mainRepository      string
	cancel              context.CancelFunc
	dependenciesChecked bool
	repositoryChecked   bool
}

func nativeSandboxExecutable(environment map[string]string, name string) (string, error) {
	if filepath.IsAbs(name) {
		info, e := os.Stat(name)
		if e == nil && !info.IsDir() && info.Mode()&0111 != 0 {
			return name, nil
		}
		return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
	}
	for _, dir := range filepath.SplitList(environment["PATH"]) {
		path := filepath.Join(dir, name)
		info, e := os.Stat(path)
		if e == nil && !info.IsDir() && info.Mode()&0111 != 0 {
			return path, nil
		}
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}
func (m *MainPrint) nativeSandboxConfig(scope *cooperative.Scope, runtime *nativeSandboxRuntime) (sandboxruntime.ConvertedConfig, error) {
	if scope == nil || !scope.BelongsTo(m.Owner) || runtime == nil {
		return sandboxruntime.ConvertedConfig{}, cooperative.ErrRestricted
	}
	settings, e := m.Startup.GetInitialSettings()
	if e != nil {
		return sandboxruntime.ConvertedConfig{}, e
	}
	state := m.state.Snapshot()
	sources := []sandboxruntime.SettingsSource{}
	for _, name := range []string{"userSettings", "projectSettings", "localSettings", "flagSettings", "policySettings"} {
		root := state.OriginalCWD
		path := ""
		switch name {
		case "userSettings":
			root = m.options.Native.ConfigHomeDirectory()
			path, e = m.settings.Path(name, "")
		case "projectSettings", "localSettings":
			path, e = m.settings.Path(name, "")
		case "flagSettings":
			if flag := m.options.Arguments.Sources.FlagSettingsPath; flag != nil {
				path = *flag
				root = filepath.Dir(path)
			}
		case "policySettings":
			path = filepath.Join(m.options.Native.ManagedRoot, "managed-settings.json")
		}
		if e != nil {
			return sandboxruntime.ConvertedConfig{}, e
		}
		value, e := m.Startup.GetSettingsForSource(name)
		if e != nil {
			return sandboxruntime.ConvertedConfig{}, e
		}
		v := protocol.NewJSONObject()
		if value != nil {
			v = value.Clone()
		}
		sources = append(sources, sandboxruntime.SettingsSource{Name: name, Root: root, File: path, Settings: v})
	}
	converted, e := sandboxruntime.Convert(sandboxruntime.ConfigInput{Settings: settings.Clone(), Sources: sources, Home: m.options.Native.Home, CWD: state.CWD, OriginalCWD: state.OriginalCWD, TemporaryDirectory: runtime.temp + string(filepath.Separator), ManagedDropInDirectory: filepath.Join(m.options.Native.ManagedRoot, "managed-settings.d"), MainWorktreeRepository: runtime.mainRepository, AdditionalDirectories: append([]string{}, m.options.Arguments.AdditionalDirectories...), Ripgrep: runtime.ripgrep, Stat: func(path string) error { _, e := m.options.Native.stat(path); return e }})
	if e == nil && (runtime.platform == "linux" || runtime.platform == "wsl") && runtime.linux.Apply != "" {
		converted.Config.Filesystem.DenyWrite = append(converted.Config.Filesystem.DenyWrite, runtime.linux.AssetRoot)
	}
	return converted, e
}
func (m *MainPrint) initializeNativeSandbox(scope *cooperative.Scope) error {
	if scope == nil || !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	if !m.options.Native.NativeProcess {
		return nil
	}
	if m.sandbox != nil && (m.sandbox.manager != nil || m.sandbox.owner != m) {
		return nil
	}
	if detached := m.options.DetachedAgent; detached != nil && detached.authParent != nil {
		m.sandbox = detached.authParent.sandbox
		return nil
	}
	settings, e := m.Startup.GetInitialSettings()
	if e != nil {
		return e
	}
	platform := m.options.Platform
	if platform == "darwin" {
		platform = "macos"
	}
	environment := m.nativeOpenAIEnvironment()
	state := m.sandbox
	if state == nil {
		base := environment["CLAUDE_CODE_TMPDIR"]
		if base == "" {
			base = "/tmp"
		}
		if canonical, e := filepath.EvalSymlinks(base); e == nil {
			base = canonical
		}
		state = &nativeSandboxRuntime{owner: m, platform: platform, initial: settings.Clone(), temp: filepath.Join(base, "claude-"+strconv.Itoa(os.Getuid())), ripgrep: protocol.NewJSONObject(protocol.JSONField{Name: "command", Value: quoteMain("rg")})}
		state.supported = platform == "macos" || platform == "linux"
		if platform == "linux" || platform == "wsl" {
			type support struct {
				supported bool
				platform  string
			}
			future, e := nativeSkillWork(m, scope, func() (support, error) {
				version, e := m.options.Native.readFile("/proc/version")
				if e != nil {
					return support{true, "linux"}, nil
				}
				return support{sandboxruntime.LinuxSupported(string(version)), sandboxruntime.LinuxCLIPlatform(string(version))}, nil
			})
			if e != nil {
				return e
			}
			result, e := future.Await(scope)
			if e != nil {
				return e
			}
			state.supported, state.platform = result.supported, result.platform
		}
		m.sandbox = state
	}
	platform = state.platform
	// isSandboxingEnabled checks supported-platform dependencies before reading
	// enabled. This memo belongs to the process even if it starts disabled.
	if !state.dependenciesChecked && state.supported {
		type probe struct {
			command string
			args    []string
			failure error
		}
		future, e := nativeSkillWork(m, scope, func() (probe, error) {
			command, args, e := NativeRipgrepCommand(m.options.Native)
			return probe{command, args, e}, nil
		})
		if e != nil {
			return e
		}
		result, e := future.Await(scope)
		if e != nil {
			return e
		}
		if result.failure != nil {
			state.dependencies = []string{"ripgrep (rg) not found"}
		} else {
			state.ripgrep = protocol.NewJSONObject(protocol.JSONField{Name: "command", Value: quoteMain(result.command)}, protocol.JSONField{Name: "args", Value: rawMain(result.args)})
		}
		if platform == "linux" || platform == "wsl" {
			result, e := m.prepareNativeLinuxSandbox(scope, false)
			if e != nil {
				return e
			}
			state.linux = result
			check := sandboxruntime.CheckLinuxDependencies(result.Status)
			state.dependencies = append(state.dependencies, check.Errors...)
		}
		state.dependenciesChecked = true
	}
	availability := sandboxruntime.ResolveAvailability(sandboxruntime.AvailabilityInput{Settings: *settings, InitialSettings: state.initial, Platform: platform, Supported: state.supported, DependencyErrors: state.dependencies})
	if availability.Reason != "" {
		if availability.Required {
			return errors.New(sandboxruntime.RequiredUnavailableMessage(availability.Reason))
		}
		_, e = fmt.Fprint(m.options.Error, sandboxruntime.DisabledUnavailableMessage(availability.Reason))
		return e
	}
	if !availability.Enabled {
		return nil
	}
	// Source resolves this once per process even if a later initialization fails.
	if !state.repositoryChecked {
		future, e := nativeSkillWork(m, scope, func() (string, error) {
			return sandboxruntime.BareGitMainRepository(m.options.Native.CWD, m.options.Native.readFile), nil
		})
		if e != nil {
			return e
		}
		state.mainRepository, e = future.Await(scope)
		if e != nil {
			return e
		}
		state.repositoryChecked = true
	}
	failed := func(failure error) error {
		// Adapter initialize clears the failed promise and debug-logs without
		// terminating the Harness. A selected Bash still fails before spawning.
		if state.cancel != nil {
			state.cancel()
			state.cancel = nil
		}
		if e := m.initializeNativeErrorLog(); e == nil && m.errorLogSink != nil {
			_ = m.errorLogSink.Debug("Failed to initialize sandbox: "+failure.Error(), "DEBUG")
		}
		return nil
	}
	converted, e := m.nativeSandboxConfig(scope, state)
	if e != nil {
		return failed(e)
	}
	if platform == "linux" || platform == "wsl" {
		linux, e := m.prepareNativeLinuxSandbox(scope, true)
		if e != nil {
			return failed(e)
		}
		state.linux = linux
		if linux.Apply != "" {
			// The pinned Source helper is immutable vendor material. This Go
			// distribution extracts the same bytes into its own asset directory.
			converted.Config.Filesystem.DenyWrite = append(converted.Config.Filesystem.DenyWrite, linux.AssetRoot)
		}
	}
	life, cancel := context.WithCancel(context.Background())
	state.cancel = cancel
	env := map[string]string{}
	for key, value := range environment {
		env[key] = value
	}
	// Runtime initialization creates listeners; Node TLS material is consumed
	// only when a real HTTPS request or HTTPS parent proxy is selected.
	tlsConfig := func() (*tls.Config, error) {
		if m.tls == nil {
			return nil, errors.New("sandbox HTTPS requires original verified TLS")
		}
		tlsEnv := map[string]string{}
		for _, key := range []string{"NODE_EXTRA_CA_CERTS", "NODE_OPTIONS", "NODE_TLS_REJECT_UNAUTHORIZED", "SSL_CERT_FILE", "SSL_CERT_DIR", "OPENSSL_CONF", "OPENSSL_MODULES"} {
			if value, ok := env[key]; ok {
				tlsEnv[key] = value
			}
		}
		return m.tls.VerifiedConfig(tlsEnv)
	}
	type creation struct {
		manager *sandboxruntime.Manager
		failure error
	}
	future, e := nativeSkillWork(m, scope, func() (creation, error) {
		suffix, e := sandboxruntime.SessionSuffix(nativeMathRandom())
		if e != nil {
			return creation{nil, e}, nil
		}
		linux := state.linux
		manager, e := sandboxruntime.NewManager(life, converted, sandboxruntime.ManagerOptions{LinuxTemporaryDirectory: linux.TemporaryDirectory, Socat: linux.Socat, SeccompBPFPath: linux.BPF, ApplySeccompPath: linux.Apply, Platform: m.options.Platform, Home: m.options.Native.Home, CWD: m.options.Native.CWD, Environment: env, Realpath: filepath.EvalSymlinks, SessionSuffix: suffix, FindExecutable: func(name string) (string, error) { return nativeSandboxExecutable(env, name) }, Network: sandboxruntime.NetworkOptions{Environment: env, TLSConfig: tlsConfig, Ask: state.ask}})
		return creation{manager, e}, nil
	})
	if e != nil {
		cancel()
		return e
	}
	result, e := future.Await(scope)
	if e != nil {
		cancel()
		return e
	}
	if result.failure != nil {
		return failed(result.failure)
	}
	state.manager = result.manager
	return nil
}

func (runtime *nativeSandboxRuntime) ask(ctx context.Context, host string, port int) (bool, error) {
	m := runtime.owner
	if m == nil || runtime.manager == nil {
		return false, nil
	}
	var future *ownerasync.OwnerFuture[bool]
	receipt, e := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if m.closed || m.shuttingDown || m.Host == nil {
			return nil
		}
		policy, e := m.Startup.GetSettingsForSource("policySettings")
		if e != nil {
			return e
		}
		if policy != nil {
			sandbox, _ := policy.Object("sandbox")
			network, _ := sandbox.Object("network")
			if string(network.Get("allowManagedDomainsOnly")) == "true" {
				return nil
			}
		}
		future, e = m.Host.StartNativeSandboxNetwork(ctx, scope, host)
		return e
	})
	if e != nil {
		return false, e
	}
	e = receipt.Wait()
	if e != nil || future == nil {
		return false, e
	}
	return future.Wait(context.Background())
}
func (m *MainPrint) refreshNativeSandbox(scope *cooperative.Scope) error {
	state := m.sandbox
	if state == nil || state.owner != m || state.manager == nil {
		return nil
	}
	converted, e := m.nativeSandboxConfig(scope, state)
	if e != nil {
		return e
	}
	state.manager.Update(converted)
	return nil
}
func (m *MainPrint) closeNativeSandbox(ctx context.Context) error {
	state := m.sandbox
	if state == nil || state.owner != m {
		return nil
	}
	if state.cancel != nil {
		state.cancel()
	}
	if state.manager != nil {
		return state.manager.Close(ctx)
	}
	return nil
}

// NativeSandboxProxyPorts is Source's read-only initialized proxy lookup. It
// reports the actual owner's listening ports, never a configured/default port.
func (m *MainPrint) NativeSandboxProxyPorts(ctx context.Context) (int, int, error) {
	var http, socks int
	receipt, e := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if !scope.BelongsTo(m.Owner) || m.closed {
			return cooperative.ErrClosed
		}
		if state := m.sandbox; state != nil && state.manager != nil {
			h, s := state.manager.ProxyPorts()
			http, socks = int(h), int(s)
		}
		return nil
	})
	if e != nil {
		return 0, 0, e
	}
	select {
	case <-receipt.Done():
		return http, socks, receipt.Wait()
	case <-ctx.Done():
		return 0, 0, ctx.Err()
	}
}

func (m *MainPrint) nativeSandboxBindings() *builtintools.BashSandboxBindings {
	if !m.options.Native.NativeProcess {
		return nil
	}
	return &builtintools.BashSandboxBindings{Owner: m.Owner, PowerShellPolicy: m.nativePowerShellSandboxPolicy, WritePaths: func(scope *cooperative.Scope) (*builtintools.BashSandboxWritePaths, error) {
		if scope == nil || !scope.BelongsTo(m.Owner) || m.sandbox == nil {
			return nil, cooperative.ErrRestricted
		}
		settings, err := m.Startup.GetInitialSettings()
		if err != nil {
			return nil, err
		}
		state := m.sandbox
		availability := sandboxruntime.ResolveAvailability(sandboxruntime.AvailabilityInput{Settings: *settings, InitialSettings: state.initial, Platform: state.platform, Supported: state.supported, DependencyErrors: state.dependencies})
		if !availability.Enabled {
			return nil, nil
		}
		write := sandboxruntime.FilesystemWriteRestriction(sandboxruntime.FilesystemConfig{}, state.platform, m.options.Native.Home)
		if state.manager != nil {
			write = state.manager.WriteConfig()
		}
		return &builtintools.BashSandboxWritePaths{AllowOnly: write.AllowOnly, DenyWithinAllow: write.DenyWithinAllow}, nil
	}, AutoAllow: func(scope *cooperative.Scope, in protocol.JSONObject) (bool, error) {
		if scope == nil || !scope.BelongsTo(m.Owner) || m.sandbox == nil {
			return false, cooperative.ErrRestricted
		}
		settings, e := m.Startup.GetInitialSettings()
		if e != nil {
			return false, e
		}
		sandbox, _ := settings.Object("sandbox")
		if string(sandbox.Get("autoAllowBashIfSandboxed")) == "false" {
			return false, nil
		}
		state := m.sandbox
		availability := sandboxruntime.ResolveAvailability(sandboxruntime.AvailabilityInput{Settings: *settings, InitialSettings: state.initial, Platform: state.platform, Supported: state.supported, DependencyErrors: state.dependencies})
		return sandboxruntime.ShouldUse(*settings, availability.Enabled, in.String("command"), string(in.Get("dangerouslyDisableSandbox")) == "true")
	}, Select: func(scope *cooperative.Scope, in protocol.JSONObject) (*builtintools.BashSandboxCommand, error) {
		if scope == nil || !scope.BelongsTo(m.Owner) {
			return nil, cooperative.ErrRestricted
		}
		state := m.sandbox
		if state == nil {
			return nil, cooperative.ErrRestricted
		}
		settings, e := m.Startup.GetInitialSettings()
		if e != nil {
			return nil, e
		}
		availability := sandboxruntime.ResolveAvailability(sandboxruntime.AvailabilityInput{Settings: *settings, InitialSettings: state.initial, Platform: state.platform, Supported: state.supported, DependencyErrors: state.dependencies})
		selected, e := sandboxruntime.ShouldUse(*settings, availability.Enabled, in.String("command"), string(in.Get("dangerouslyDisableSandbox")) == "true")
		if e != nil || !selected {
			return nil, e
		}
		if state.manager == nil {
			return nil, errors.New("Sandbox failed to initialize. ")
		}
		if state.platform == "linux" || state.platform == "wsl" {
			state.manager.UpdateEnvironment(m.nativeOpenAIEnvironment())
		}
		return &builtintools.BashSandboxCommand{TemporaryDirectory: state.temp, Wrap: state.manager.Wrap, WrapContext: state.manager.WrapContext, Cleanup: state.manager.CleanupAfterCommand}, nil
	}}
}
