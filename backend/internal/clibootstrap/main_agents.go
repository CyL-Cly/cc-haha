package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliargs"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
)

// AgentsArguments preserves existing pre-action parser diagnostics and does not
// run a handler for help/errors. Root -p still selects Source's print profile.
func AgentsArguments(args []string, version string) (cliargs.Result, bool) {
	candidate := false
	for _, arg := range args {
		if arg == "agents" {
			candidate = true
		}
	}
	if !candidate {
		return cliargs.Result{}, false
	}
	if _, settings := EagerValue(args, "--settings"); !settings {
		eager := Prepare(args, Options{})
		if eager.ExitCode != 0 {
			return cliargs.Result{ExitCode: eager.ExitCode, Stderr: eager.Stderr}, true
		}
	}
	parsed := cliargs.Parse(args, cliargs.Config{Version: version})
	if parsed.Parsed != nil && parsed.Parsed.Path != "claude agents" {
		return cliargs.Result{}, false
	}
	return parsed, true
}

type MainAgents struct {
	Owner   *cooperative.Owner
	Catalog *agentcatalog.Service
	Native  *NativeBoundary
	Startup *clistartupenv.Service
	Global  *cliglobalconfig.Service
	Output  io.Writer
	command *MainCommand
}

func NewMainAgents(args []string, n *NativeBoundary, pid int, nonInteractive bool, out io.Writer) (*MainAgents, error) {
	if n != nil && n.NativeProcess {
		command, err := NewMainCommand(MainCommandOptions{Arguments: args, Native: n, Platform: runtime.GOOS, Version: "999.0.0-local", PID: pid, NonInteractive: nonInteractive, Output: out, Error: os.Stderr})
		if err != nil {
			return nil, err
		}
		catalog, err := command.Settings.AgentCatalog(command.Owner, n.CWD, nonInteractive)
		if err != nil {
			command.Close()
			return nil, err
		}
		return &MainAgents{Owner: command.Owner, Catalog: catalog, Native: n, Startup: command.Startup, Global: command.Global, Output: out, command: command}, nil
	}
	if n == nil || !n.ExternalAbsent || pid <= 0 || out == nil {
		return nil, mainCap("agents native/stdio/process bindings")
	}
	parsed := cliargs.Parse(args, cliargs.Config{})
	if parsed.Parsed == nil || parsed.Parsed.Path != "claude agents" {
		return nil, mainCap("agents admitted parser action")
	}
	defaults := cliargs.Parse([]string{"agents"}, cliargs.Config{})
	for key, value := range parsed.Parsed.Globals {
		if reflect.DeepEqual(value, defaults.Parsed.Globals[key]) {
			continue
		}
		if key != "settingSources" && key != "settings" && key != "bare" && key != "verbose" {
			return nil, mainCap("agents active startup flag " + key)
		}
		_ = value
	}
	switch strings.ToLower(jsTrim(n.Environment["CLAUDE_CODE_SYNC_PLUGIN_INSTALL"])) {
	case "1", "true", "yes", "on":
		return nil, mainCap("agents active plugin install sync")
	}
	// The existing eager producer uses real OS APIs. Admit its exact inputs through
	// this same native guard before any read or inline temporary settings write.
	if value, found := EagerValue(args, "--settings"); found && value != "" {
		v := jsTrim(value)
		if !(strings.HasPrefix(v, "{") && strings.HasSuffix(v, "}")) {
			path := value
			if !filepath.IsAbs(path) {
				path = filepath.Join(n.CWD, path)
			}
			if e := n.check("eager settings", path); e != nil {
				return nil, e
			}
		} else if e := n.check("inline settings directory", n.Temp); e != nil {
			return nil, e
		}
	}
	sources := Prepare(args, Options{WorkingDir: n.CWD, TempDir: n.Temp})
	if sources.ExitCode != 0 {
		return nil, errors.New(strings.TrimSuffix(sources.Stderr, "\n"))
	}
	for _, arg := range args {
		if arg == "--bare" {
			n.Environment["CLAUDE_CODE_SIMPLE"] = "1"
		}
	}
	entry := n.Environment["CLAUDE_CODE_ENTRYPOINT"]
	if entry == "" {
		entry = "cli"
		if nonInteractive {
			entry = "sdk-cli"
		}
		n.Environment["CLAUDE_CODE_ENTRYPOINT"] = entry
	}
	m := &MainAgents{Owner: cooperative.New(), Native: n, Output: out}
	fail := func(e error) (*MainAgents, error) { m.Close(); return nil, e }
	state, e := clisettings.NewState(n.NowMillis)
	if e != nil {
		return fail(e)
	}
	settings, e := clisettings.NewService(clisettings.Options{State: state, ConfigHome: n.Environment["CLAUDE_CONFIG_DIR"], OriginalCWD: n.CWD, Profile: clisettings.ExternalReleaseProfile, Now: n.NowMillis, PID: pid, FS: mainSettingsFiles{n: n}})
	if e != nil {
		return fail(e)
	}
	config := n.Environment["CLAUDE_CONFIG_DIR"]
	global, e := cliglobalconfig.New(cliglobalconfig.Options{Home: n.Home, ConfigDir: &config, Now: n.Now, NowMillis: n.NowMillis, PID: pid, FS: mainGlobalFiles{n: n}})
	if e != nil {
		return fail(e)
	}
	m.Global = global
	env := protocol.NewJSONObject()
	for k, v := range n.Environment {
		env.Set(k, quoteMain(v))
	}
	environment, e := clistartupenv.NewEnvironment(env)
	if e != nil {
		return fail(e)
	}
	m.Startup, e = clistartupenv.New(clistartupenv.Options{Global: global, Settings: settings, SettingsState: state, Environment: environment, ConfigHome: config, FS: mainSettingsFiles{n: n}, NowMillis: n.NowMillis, Host: clistartupenv.Host{Profile: clistartupenv.EmptyPolicyReleaseProfile, RemoteCacheAbsent: true, MDMCacheAbsent: true, HKCUCacheAbsent: true, PolicyFilesAbsent: true, PluginBaseAbsent: true, AllowedSources: sources.State.AllowedSettingSources, FlagPath: sources.State.FlagSettingsPath}})
	if e != nil {
		return fail(e)
	}
	if _, e = m.Startup.GetInitialSettings(); e != nil {
		return fail(e)
	}
	if e = global.Enable(); e != nil {
		return fail(e)
	}
	if e = m.Startup.ApplySafeConfigEnvironmentVariables(); e != nil {
		return fail(e)
	}
	if e = m.Startup.ApplyExtraCACertsFromConfig(); e != nil {
		return fail(e)
	}
	for _, property := range environment.Snapshot().OwnProperties() {
		var key, value string
		if json.Unmarshal(property.NameJSON, &key) == nil && json.Unmarshal(property.Value, &value) == nil {
			n.Environment[key] = value
		}
	}
	files := mainAgentsFiles{n: n}
	plugins := func(scope *cooperative.Scope) ([]agentcatalog.Definition, error) {
		// Inspect real private plugin inputs before admitting the actual absent
		// cache branch. Any active loader/settings capability stays explicitly red.
		for _, p := range []string{filepath.Join(config, "plugins"), filepath.Join(n.CWD, ".claude/plugins")} {
			entries, e := n.readDir(p)
			if e != nil && !os.IsNotExist(e) {
				return nil, &agentcatalog.CapabilityError{Boundary: e.Error()}
			}
			if len(entries) > 0 {
				return nil, &agentcatalog.CapabilityError{Boundary: "active plugin cache loader"}
			}
		}
		for _, source := range m.Startup.EnabledSettingSources() {
			v, e := m.Startup.GetSettingsForSource(source)
			if e != nil {
				return nil, e
			}
			if v != nil && (v.Get("enabledPlugins") != nil || v.Get("extraKnownMarketplaces") != nil) {
				return nil, &agentcatalog.CapabilityError{Boundary: "configured plugin cache producer"}
			}
		}
		return []agentcatalog.Definition{}, nil
	}
	m.Catalog, e = agentcatalog.New(agentcatalog.Options{Owner: m.Owner, Files: files, Home: n.Home, CWD: n.CWD, ProjectRoot: n.CWD, ConfigHome: config, ManagedRoot: n.ManagedRoot, EnabledSources: m.Startup.EnabledSettingSources(), Environment: func(k string) string { return n.Environment[k] }, Settings: m.Startup.GetSettingsForSource, Now: n.Now, NonInteractive: nonInteractive, Plugins: plugins})
	if e != nil {
		return fail(e)
	}
	return m, nil
}
func (m *MainAgents) Run(ctx context.Context) (int, error) {
	e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		r, e := m.Catalog.List(scope)
		if e != nil {
			return e
		}
		_, e = io.WriteString(m.Output, agentcatalog.Render(r.All))
		return e
	})
	if e != nil {
		return 1, e
	}
	return 0, nil
}
func (m *MainAgents) Close() {
	if m != nil && m.command != nil {
		m.command.Close()
		return
	}
	if m != nil && m.Owner != nil {
		if m.Global != nil {
			_ = m.Global.Close()
		}
		m.Owner.Close()
	}
}

// Schema support is shared, but print has not migrated the configured-agent
// consumer. Retain that capability until the actual consumer is ported. This is
// a Go migration gate, not an assertion that Source rejects the same settings.
func checkPrintAgentSettings(startup interface {
	GetInitialSettings() (*protocol.JSONObject, error)
	GetSettingsForSource(string) (*protocol.JSONObject, error)
}) error {
	merged, e := startup.GetInitialSettings()
	if e != nil {
		return e
	}
	if merged != nil {
		var overrides protocol.JSONObject
		if overrides.UnmarshalJSON(merged.Get("builtInAgentOverrides")) == nil {
			for _, property := range overrides.OwnProperties() {
				var entry protocol.JSONObject
				if entry.UnmarshalJSON(property.Value) == nil && (entry.Get("model") != nil || entry.Get("effort") != nil) {
					return mainCap("print active agent settings consumer")
				}
			}
		}
	}
	// Source pluginOnlyPolicy reads the admin policy source, never an arbitrary
	// merged user/project value. Other surface restrictions have other consumers.
	policy, e := startup.GetSettingsForSource("policySettings")
	if e != nil {
		return e
	}
	if policy != nil {
		raw := policy.Get("strictPluginOnlyCustomization")
		if string(raw) == "true" {
			return mainCap("print active agent settings consumer")
		}
		var values []string
		if json.Unmarshal(raw, &values) == nil {
			for _, v := range values {
				if v == "agents" {
					return mainCap("print active agent settings consumer")
				}
			}
		}
	}
	return nil
}
