package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliargs"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpcli"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/charmbracelet/x/term"
)

func runMCPCommand(ctx context.Context, arguments []string, parsed cliargs.Parsed) int {
	native, err := newMainNativeBoundary()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	code, err := runNativeMCPCommand(ctx, arguments, parsed, native, os.Stdin, os.Stdout, os.Stderr, runtime.GOOS)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return code
}
func runNativeMCPCommand(ctx context.Context, arguments []string, parsed cliargs.Parsed, native *clibootstrap.NativeBoundary, input *os.File, output, diagnostics io.Writer, platform string) (int, error) {
	stdoutTTY := false
	if file, ok := output.(*os.File); ok {
		stdoutTTY = term.IsTerminal(file.Fd())
	}
	command, err := clibootstrap.NewMainCommand(clibootstrap.MainCommandOptions{Arguments: arguments, Native: native, Platform: platform, Version: version, PID: os.Getpid(), NonInteractive: !stdoutTTY, Output: output, Error: diagnostics})
	if err != nil {
		return 1, err
	}
	defer command.Close()
	claudeAI, err := command.NewCLIClaudeAIMCP(ctx, nil)
	if err != nil {
		return 1, err
	}
	pluginOptions, err := command.Settings.PluginOptions(command.Secure)
	if err != nil {
		return 1, err
	}
	plugins, _, err := command.Settings.PluginMCP(pluginOptions, command.Secure)
	if err != nil {
		return 1, err
	}
	configurations, err := command.Settings.MCPConfigurations(plugins, !stdoutTTY)
	if err != nil {
		return 1, err
	}
	editor, err := command.Settings.MCPEditor(configurations)
	if err != nil {
		return 1, err
	}
	settings := func(ctx context.Context) (protocol.JSONObject, error) {
		if err := context.Cause(ctx); err != nil {
			return protocol.JSONObject{}, err
		}
		value, err := command.Settings.InitialSettings()
		if value == nil {
			return protocol.NewJSONObject(), err
		}
		return value.Clone(), err
	}
	authPlatform := platform
	if authPlatform == "windows" {
		authPlatform = "win32"
	}
	auth, err := mcpauth.New(mcpauth.Options{Secure: command.Secure, Home: native.Home, ConfigDir: native.Environment["CLAUDE_CONFIG_DIR"], CWD: native.CWD, Platform: authPlatform, Environment: native.Environment, Settings: settings})
	if err != nil {
		return 1, err
	}
	peers, err := mcpruntime.New(mcpruntime.Options{PrepareHTTP: claudeAI.PrepareHTTP, CWD: native.CWD, Version: version, Environment: native.Environment, Enabled: command.Settings.MCPExecutionEnabled, Authenticate: auth.Authenticate, ClearOAuth: auth.Clear, Auth: func(ctx context.Context, name string, cfg protocol.JSONObject) (mcpclient.AuthProvider, error) {
		if cfg.String("type") == "sse" || cfg.String("type") == "http" {
			return auth.Provider(ctx, name, cfg)
		}
		return nil, nil
	}})
	if err != nil {
		return 1, err
	}
	defer peers.Close(context.Background())
	all := func(ctx context.Context) (mcpconfig.Result, error) { return claudeAI.All(ctx, configurations) }
	reset := func(ctx context.Context) error {
		if err := context.Cause(ctx); err != nil {
			return err
		}
		err := command.Global.SaveProject(editor.ProjectKey(), cliglobalconfig.Updater(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
			next := current.Clone()
			next.Set("enabledMcpjsonServers", json.RawMessage(`[]`))
			next.Set("disabledMcpjsonServers", json.RawMessage(`[]`))
			next.Set("enableAllProjectMcpServers", json.RawMessage(`false`))
			return &next, nil
		}))
		if err == nil {
			configurations.ClearCaches()
		}
		return err
	}
	service, err := mcpcli.New(mcpcli.Options{Configurations: configurations, Editor: editor, Auth: auth, Runtime: peers, Environment: native.Environment, Output: output, Error: diagnostics, All: all, Settings: settings, ResetChoices: reset, ReadSecret: func(ctx context.Context) (string, error) {
		return readMCPClientSecret(ctx, native.Environment, input, diagnostics)
	}, ReadSecretJSON: func(ctx context.Context) (json.RawMessage, error) {
		return readMCPClientSecretJSON(ctx, native.Environment, input, diagnostics)
	}, ImportDesktop: func(ctx context.Context, scope string) error {
		return importMCPDesktop(ctx, platform, native.Home, native.Environment, input, output, scope, all, editor)
	}, Serve: func(ctx context.Context, debug, verbose bool) error {
		return serveNativeMCP(ctx, command, platform, input, output, diagnostics, debug, verbose)
	}})
	if err != nil {
		return 1, err
	}
	return service.Run(ctx, parsed)
}

func readMCPClientSecret(ctx context.Context, environment map[string]string, input *os.File, diagnostics io.Writer) (string, error) {
	raw, err := readMCPClientSecretJSON(ctx, environment, input, diagnostics)
	if err != nil {
		return "", err
	}
	units, err := protocol.DecodeJavaScriptJSONString(raw)
	return string(utf16.Decode(units)), err
}

func readMCPClientSecretJSON(ctx context.Context, environment map[string]string, input *os.File, diagnostics io.Writer) (value json.RawMessage, failure error) {
	if value := environment["MCP_CLIENT_SECRET"]; value != "" {
		encoded, _ := json.Marshal(value)
		return encoded, nil
	}
	if input == nil || !term.IsTerminal(input.Fd()) {
		return nil, errors.New("No TTY available to prompt for client secret. Set MCP_CLIENT_SECRET env var instead.")
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	state, err := term.MakeRaw(input.Fd())
	if err != nil {
		return nil, err
	}
	defer func() { failure = errors.Join(failure, term.Restore(input.Fd(), state)) }()
	fmt.Fprint(diagnostics, "Enter OAuth client secret: ")
	secret := []uint16{}
	buffer := make([]byte, 4096)
	for {
		if err := context.Cause(ctx); err != nil {
			return nil, err
		}
		n, err := input.Read(buffer)
		if err != nil {
			return nil, err
		}
		text := string(protocol.DecodeJavaScriptUTF8(buffer[:n]))
		switch text {
		case "\n", "\r":
			fmt.Fprintln(diagnostics)
			return protocol.QuoteJavaScriptUTF16(secret), nil
		case "\x03":
			return nil, errors.New("Cancelled")
		case "\x7f", "\b":
			if len(secret) > 0 {
				secret = secret[:len(secret)-1]
			}
		default:
			secret = append(secret, utf16.Encode([]rune(text))...)
		}
	}
}
