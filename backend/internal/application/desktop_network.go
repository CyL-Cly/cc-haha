package application

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/migrations"
	"github.com/NanmiCoder/cc-haha/backend/internal/networksettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

type desktopNetworkState struct {
	read                                func(context.Context) (storage.Object, error)
	base                                map[string]string
	fingerprint                         string
	firstTokenDerived, streamMaxDerived bool
}

func desktopNetworkFingerprint(settings networksettings.Settings, env map[string]string) string {
	manual, system := "", ""
	if settings.Proxy.Mode == "manual" {
		manual = protocol.TrimJavaScriptString(settings.Proxy.URL)
	}
	if settings.Proxy.Mode == "system" {
		system = protocol.TrimJavaScriptString(env["CC_HAHA_SYSTEM_PROXY_URL"])
	}
	noProxy := env["no_proxy"]
	if noProxy == "" {
		noProxy = env["NO_PROXY"]
	}
	encoded, _ := json.Marshal([]any{settings.TimeoutMillis, settings.Proxy.Mode, manual, system, noProxy})
	return string(encoded)
}
func mergeDesktopNetworkEnvironment(base, network map[string]string) map[string]string {
	next := map[string]string{}
	for key, value := range base {
		next[key] = value
	}
	for key, value := range network {
		next[key] = value
	}
	return next
}
func initializeDesktopNetwork(ctx context.Context, o Options, native *clibootstrap.NativeBoundary) (*desktopNetworkState, error) {
	if o.desktopTeamLaunch == nil || native == nil || !native.NativeProcess {
		return nil, nil
	}
	environment := map[string]string{}
	for key, value := range native.Environment {
		environment[key] = value
	}
	service := (*settings.Service)(nil)
	if o.ProviderOptions != nil {
		service = o.ProviderOptions.Settings
	}
	if service == nil {
		store := o.Store
		if store == nil {
			store = storage.New(storage.Options{})
		}
		home := environment["HOME"]
		if runtime.GOOS == "windows" && environment["USERPROFILE"] != "" {
			home = environment["USERPROFILE"]
		}
		config := environment["CLAUDE_CONFIG_DIR"]
		if config == "" {
			config = filepath.Join(home, ".claude")
		}
		upgrader, err := migrations.New(migrations.Options{Store: store})
		if err != nil {
			return nil, err
		}
		service, err = settings.New(settings.Options{Store: store, HomeDir: home, ConfigDir: config, ProjectRoot: o.WorkDir, EnsureMigrated: upgrader.SettingsCallback, Environment: func() map[string]string { return environment }, ShellEnvironment: newTerminalEnvironment(environment).Get, GitExecutable: "git"})
		if err != nil {
			return nil, err
		}
	}
	state := &desktopNetworkState{read: service.GetUserSettings, base: environment, firstTokenDerived: environment["CLAUDE_STREAM_FIRST_TOKEN_TIMEOUT_MS"] == "", streamMaxDerived: environment["CLAUDE_STREAM_MAX_DURATION_MS"] == ""}
	user, err := state.read(ctx)
	if err != nil {
		return nil, err
	}
	normalized := networksettings.Normalize(raw(user))
	network, err := networksettings.BuildEnvironment(normalized, environment)
	if err != nil {
		return nil, err
	}
	next := map[string]string{}
	for key, value := range environment {
		next[key] = value
	}
	for key, value := range network {
		next[key] = value
	}
	defaults := map[string]string{"CLAUDE_ENABLE_STREAM_WATCHDOG": "1", "CLAUDE_STREAM_IDLE_TIMEOUT_MS": "240000", "CLAUDE_STREAM_TOOL_INPUT_MAX_DURATION_MS": "120000", "CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK": "1"}
	for key, value := range defaults {
		if next[key] == "" {
			next[key] = value
		}
	}
	if state.firstTokenDerived {
		next["CLAUDE_STREAM_FIRST_TOKEN_TIMEOUT_MS"] = network["API_TIMEOUT_MS"]
	}
	if state.streamMaxDerived {
		next["CLAUDE_STREAM_MAX_DURATION_MS"] = strconv.FormatInt(max(networksettings.MinimumStreamMillis, normalized.TimeoutMillis), 10)
	}
	state.fingerprint = desktopNetworkFingerprint(normalized, next)
	native.Environment = next
	return state, nil
}
func (state *desktopNetworkState) nextVariables(ctx context.Context, user json.RawMessage) (map[string]string, error) {
	if ctx.Err() != nil {
		return nil, context.Cause(ctx)
	}
	normalized := networksettings.Normalize(user)
	network, err := networksettings.BuildEnvironment(normalized, state.base)
	if err != nil {
		return nil, err
	}
	fingerprint := desktopNetworkFingerprint(normalized, mergeDesktopNetworkEnvironment(state.base, network))
	if fingerprint == state.fingerprint {
		return nil, nil
	}
	noProxy := network["no_proxy"]
	if noProxy == "" {
		noProxy = network["NO_PROXY"]
	}
	network["NO_PROXY"], network["no_proxy"] = noProxy, noProxy
	if state.firstTokenDerived {
		network["CLAUDE_STREAM_FIRST_TOKEN_TIMEOUT_MS"] = network["API_TIMEOUT_MS"]
	}
	if state.streamMaxDerived {
		network["CLAUDE_STREAM_MAX_DURATION_MS"] = strconv.FormatInt(max(networksettings.MinimumStreamMillis, normalized.TimeoutMillis), 10)
	}
	return network, nil
}
func (s *Session) refreshDesktopNetwork(ctx context.Context) error {
	state := s.desktopNetwork
	if state == nil {
		return nil
	}
	user, err := state.read(ctx)
	if err != nil {
		return err
	}
	network, err := state.nextVariables(ctx, raw(user))
	if err != nil || network == nil {
		return err
	}
	if err = s.process.SetDesktopNetworkEnvironment(ctx, network); err != nil {
		return err
	}
	normalized := networksettings.Normalize(raw(user))
	fresh, err := networksettings.BuildEnvironment(normalized, state.base)
	if err != nil {
		return err
	}
	state.fingerprint = desktopNetworkFingerprint(normalized, mergeDesktopNetworkEnvironment(state.base, fresh))
	return nil
}
