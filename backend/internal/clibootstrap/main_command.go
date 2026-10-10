package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliargs"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/diagnostics"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// MainCommand binds non-model CLI actions to the existing native settings,
// global writer and credential services. It does not construct an ASK session.
type MainCommand struct {
	version             string
	pid                 int
	Owner               *cooperative.Owner
	Native              *NativeBoundary
	Settings            *NativeProcessSettings
	Startup             *clistartupenv.Service
	Global              *cliglobalconfig.Service
	Secure              *securestorage.Store
	Output, Error       io.Writer
	NonInteractive      bool
	credentialPlatform  string
	credentialDirectory string
	credentialCache     map[string]string
	providerProxy       mainCommandProviderProxy
	claudeAIMCP         []*CLIClaudeAIMCP
	mcpClaudeAuth       *NativeOpenAIAuth
}

type MainCommandOptions struct {
	Arguments         []string
	Native            *NativeBoundary
	Platform, Version string
	PID               int
	NonInteractive    bool
	Output, Error     io.Writer
}

func NewMainCommand(o MainCommandOptions) (*MainCommand, error) {
	n := o.Native
	if n == nil || !n.NativeProcess || n.ResolvePolicy == nil || o.PID <= 0 || o.Output == nil || o.Error == nil {
		return nil, mainCap("non-model native command process bindings")
	}
	sources := Prepare(o.Arguments, Options{WorkingDir: n.CWD, TempDir: n.Temp})
	if sources.ExitCode != 0 {
		return nil, errors.New(strings.TrimSuffix(sources.Stderr, "\n"))
	}
	parsed := cliargs.Parse(o.Arguments, cliargs.Config{Version: o.Version})
	if parsed.Parsed == nil || parsed.Parsed.Path == "claude" {
		return nil, mainCap("non-model parsed command action: " + strings.TrimSpace(parsed.Stderr))
	}
	// These globals have actual consumers below. Other root flags require their
	// own command consumer instead of silently being treated as default values.
	defaults := cliargs.Parse([]string{"agents"}, cliargs.Config{Version: o.Version})
	if defaults.Parsed != nil {
		for key, value := range parsed.Parsed.Globals {
			if _, rootOption := defaults.Parsed.Globals[key]; !rootOption {
				continue
			}
			if jsonEqualMain(value, defaults.Parsed.Globals[key]) {
				continue
			}
			switch key {
			case "pluginDir":
				if !strings.HasPrefix(parsed.Parsed.Path, "claude plugin ") {
					return nil, mainCap("non-model active startup flag " + key)
				}
			case "settingSources", "settings", "bare", "verbose":
			default:
				return nil, mainCap("non-model active startup flag " + key)
			}
		}
	}
	if parsed.Parsed.Globals["bare"] == true {
		n.Environment["CLAUDE_CODE_SIMPLE"] = "1"
	}
	if n.Environment["CLAUDE_CODE_ENTRYPOINT"] == "" {
		n.Environment["CLAUDE_CODE_ENTRYPOINT"] = "cli"
		if o.NonInteractive {
			n.Environment["CLAUDE_CODE_ENTRYPOINT"] = "sdk-cli"
		}
		// Original initializeEntrypoint identifies mcp serve before the TTY
		// fallback. Its auxiliary requests carry the MCP billing entrypoint.
		for i := 0; i+1 < len(o.Arguments); i++ {
			if o.Arguments[i] == "mcp" && o.Arguments[i+1] == "serve" {
				n.Environment["CLAUDE_CODE_ENTRYPOINT"] = "mcp"
				break
			}
		}
	}
	n.Environment["COREPACK_ENABLE_AUTO_PIN"] = "0"
	n.Environment["NoDefaultCurrentDirectoryInExePath"] = "1"
	m := &MainCommand{version: o.Version, pid: o.PID, Owner: cooperative.New(), Native: n, Output: o.Output, Error: o.Error, NonInteractive: o.NonInteractive, credentialPlatform: o.Platform, credentialDirectory: "/home/claude/.claude/remote", credentialCache: map[string]string{}}
	m.providerProxy.ready = make(chan struct{})
	if n.CCRCredentialDirectory != "" {
		if !filepath.IsAbs(n.CCRCredentialDirectory) {
			m.Close()
			return nil, mainCap("absolute native CCR credential directory")
		}
		m.credentialDirectory = n.CCRCredentialDirectory
	}
	fail := func(err error) (*MainCommand, error) { m.Close(); return nil, err }
	config := n.Environment["CLAUDE_CONFIG_DIR"]
	diag, err := diagnostics.NewProcess(diagnostics.Options{Environment: func(k string) string { return n.Environment[k] }, Now: n.Now, NowMillis: func() int64 { return int64(n.NowMillis()) }, PID: o.PID, Platform: o.Platform, Files: mainDiagnosticFiles{n: n}})
	if err != nil {
		return fail(err)
	}
	global, err := cliglobalconfig.New(cliglobalconfig.Options{Home: n.Home, ConfigDir: originalGlobalConfigDirectory(n, config), Now: n.Now, NowMillis: n.NowMillis, PID: o.PID, FS: mainGlobalFiles{n: n}, Diagnostics: diag, Stderr: func(s string) { _, _ = io.WriteString(o.Error, s) }})
	if err != nil {
		return fail(err)
	}
	m.Global = global
	env := protocol.NewJSONObject()
	for key, value := range n.Environment {
		env.Set(key, quoteMain(value))
	}
	environment, err := clistartupenv.NewEnvironment(env)
	if err != nil {
		return fail(err)
	}
	m.Settings = &NativeProcessSettings{n: n, platform: o.Platform, sources: sources.State, environment: environment, global: global, store: storage.New(storage.Options{}), diagnostics: diag}
	m.Settings.mergeCcHahaEnv = m.mergeCLIProviderEnvironment
	if strings.HasPrefix(parsed.Parsed.Path, "claude plugin ") {
		m.Settings.pluginCowork = parsed.Parsed.Options["cowork"] == true
		if paths, ok := parsed.Parsed.Globals["addDir"].([]any); ok {
			for _, path := range paths {
				if value, ok := path.(string); ok {
					m.Settings.pluginAdditional = append(m.Settings.pluginAdditional, value)
				}
			}
		}
		if paths, ok := parsed.Parsed.Globals["pluginDir"].([]any); ok {
			for _, path := range paths {
				if value, ok := path.(string); ok {
					m.Settings.pluginInline = append(m.Settings.pluginInline, value)
				}
			}
		}
	}
	if err = m.Settings.ensureSettings(); err != nil {
		return fail(err)
	}
	m.Startup = m.Settings.startup
	if _, err = m.Settings.InitialSettings(); err != nil {
		return fail(err)
	}
	if err = global.Enable(); err != nil {
		return fail(err)
	}
	if err = m.Startup.ApplySafeConfigEnvironmentVariables(); err != nil {
		return fail(err)
	}
	if err = m.Startup.ApplyExtraCACertsFromConfig(); err != nil {
		return fail(err)
	}
	for _, property := range environment.Snapshot().OwnProperties() {
		var key, value string
		if json.Unmarshal(property.NameJSON, &key) == nil && json.Unmarshal(property.Value, &value) == nil {
			n.Environment[key] = value
		}
	}
	platform := o.Platform
	if platform == "windows" {
		platform = "win32"
	}
	if err = n.check("command secure configuration", config); err != nil {
		return fail(err)
	}
	m.Secure, err = securestorage.New(securestorage.Options{Home: n.Home, ConfigDir: config, ConfigDirExplicit: n.ConfigDirExplicit, Platform: platform, Environment: n.Environment, RunSecurity: n.RunSecurity})
	if err != nil {
		return fail(err)
	}
	close(m.providerProxy.ready)
	return m, nil
}

func jsonEqualMain(a, b any) bool {
	x, e := json.Marshal(a)
	y, f := json.Marshal(b)
	return e == nil && f == nil && string(x) == string(y)
}

func (m *MainCommand) Run(ctx context.Context, work func(*cooperative.Scope) error) error {
	if m == nil || m.Owner == nil || work == nil {
		return cooperative.ErrRestricted
	}
	return m.Owner.Run(ctx, work)
}

func (m *MainCommand) Close() {
	if m == nil {
		return
	}
	m.closeCLIProviderProxy()
	for _, binding := range m.claudeAIMCP {
		binding.Close()
	}
	if m.Owner != nil {
		m.Owner.Close()
	}
	if m.Settings != nil {
		_ = m.Settings.Close(context.Background())
	} else if m.Global != nil {
		_ = m.Global.Close()
	}
}

// File-based OAuth storage and its marker use the same guarded native files as
// model requests. A selected missing file never falls back to another account.
func (m *MainCommand) OpenAIStoragePath() string {
	path := protocol.TrimJavaScriptString(m.Native.Environment["OPENAI_CODEX_OAUTH_FILE"])
	if path != "" && !filepath.IsAbs(path) {
		path = filepath.Join(m.Native.CWD, path)
	}
	return path
}

func (m *MainCommand) ReadCredentialFile(path string) ([]byte, error) { return m.Native.readFile(path) }
func (m *MainCommand) CredentialFileExists(path string) bool {
	_, err := m.Native.stat(path)
	return err == nil
}
func (m *MainCommand) PID() int { return os.Getpid() }
