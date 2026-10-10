package clibootstrap

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/NanmiCoder/cc-haha/backend/internal/computeruse"
	"github.com/NanmiCoder/cc-haha/backend/internal/computerusemcp"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/curepl"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillwatcher"
)

// NativeComputerUseOptions supplies original process/native primitives. The
// shared desktop service is borrowed; an ordinary CLI owns its private service.
// RuntimeOptions and NativeCall replace only worker/OS primitives in hermetic
// tests. Production uses the same Go executable and its native helper daemon.
type NativeComputerUseOptions struct {
	Service             *computeruse.Service
	AppRoot, Executable string
	RuntimeOptions      *curepl.RuntimeOptions
	NativeCall          func(context.Context, string, json.RawMessage) (json.RawMessage, error)
	SkillWatchTiming    *skillwatcher.Timing // Timer primitive, hermetic fixtures only.
}
type nativeComputerUse struct {
	service     *computeruse.Service
	binder      *computerusemcp.Binder
	ownsService bool
}

func computerUseEnvironmentEnabled(environment map[string]string) bool {
	switch strings.ToLower(strings.TrimFunc(environment["CLAUDE_COMPUTER_USE_ENABLED"], func(r rune) bool { return unicode.IsSpace(r) && r != 0x85 || r == 0xfeff })) {
	case "false", "0", "off", "no":
		return false
	}
	return true
}
func (m *MainPrint) newNativeComputerUse() error {
	n := m.options.Native
	if !n.NativeProcess || m.agentChild {
		return nil
	}
	options := m.options.ComputerUse
	if options == nil {
		options = &NativeComputerUseOptions{}
	}
	service := options.Service
	owns := service == nil
	var e error
	executable := options.Executable
	if executable == "" {
		executable, e = os.Executable()
		if e != nil {
			return e
		}
	}
	if owns {
		service, e = computeruse.New(computeruse.Options{HomeDir: n.Home, ConfigDir: n.Environment["CLAUDE_CONFIG_DIR"], WorkDir: n.CWD, AppRoot: options.AppRoot, TempDir: n.Temp, Executable: executable, Environment: n.Environment, Platform: m.options.Platform})
		if e != nil {
			return e
		}
	}
	m.computerUse = &nativeComputerUse{service: service, ownsService: owns}
	// This is Source CLI setup's separate gate. The shipped skill has its own
	// config/environment visibility and does not depend on helper launchability.
	if m.options.Arguments.ComputerUse != nil && !*m.options.Arguments.ComputerUse || !computerUseEnvironmentEnabled(n.Environment) {
		return nil
	}
	if m.options.Platform != "darwin" && m.options.Platform != "windows" && m.options.Platform != "win32" {
		return nil
	}
	config, e := service.Config(context.Background())
	if e != nil || string(config["enabled"]) != "true" {
		return nil
	}
	if m.options.Platform != "darwin" {
		return mainCap("Windows Computer Use SDK tools and native compatibility transport")
	}
	launchable, e := service.ResolveLaunchableHelper(context.Background())
	if e != nil || launchable == "" {
		return nil
	}
	kernelOptions := curepl.RuntimeOptions{Executable: executable, TempDir: n.Temp, Platform: m.options.Platform}
	if options.RuntimeOptions != nil {
		kernelOptions = *options.RuntimeOptions
	}
	kernel, e := curepl.NewRuntime(kernelOptions)
	if e != nil {
		return e
	}
	facadeOptions := computeruse.NativeToolsOptions{Service: service, CurrentSessionID: func() string {
		if m.Controller != nil {
			return m.Controller.SessionID()
		}
		return m.state.SessionID()
	}, Now: n.Now, OnNotification: m.options.OnComputerUseNotification}
	if options.NativeCall != nil {
		facadeOptions.NativeCall = func(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
			return options.NativeCall(ctx, name, args)
		}
	}
	facade, e := computeruse.NewNativeTools(facadeOptions)
	if e != nil {
		kernel.Close()
		return e
	}
	binder, e := computerusemcp.New(facade, kernel)
	if e != nil {
		kernel.Close()
		_ = facade.Close(context.Background())
		return e
	}
	m.computerUse.binder = binder
	// setupComputerUseMCP appends these CLI rules after user-provided rules.
	m.options.Arguments.AllowedTools = append(append([]string{}, m.options.Arguments.AllowedTools...), "mcp__computer-use__js", "mcp__computer-use__js_reset")
	return nil
}
func (m *MainPrint) registerNativeComputerUse(scope *cooperative.Scope, binding *ownerasync.NativeBinding) error {
	if binding == nil || !binding.Owns(m.Owner) || !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	if m.computerUse == nil || m.computerUse.binder == nil {
		return nil
	}
	if m.mcpTools == nil {
		return mainCap("Computer Use requires shared native MCP tools and owner binding")
	}
	executable := m.options.ComputerUse
	command := ""
	if executable != nil {
		command = executable.Executable
	}
	if command == "" {
		command, _ = os.Executable()
	}
	config := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("stdio")}, protocol.JSONField{Name: "command", Value: quoteMain(command)}, protocol.JSONField{Name: "args", Value: json.RawMessage(`["--computer-use-mcp"]`)}, protocol.JSONField{Name: "scope", Value: quoteMain("dynamic")})
	return m.mcpTools.RegisterComputerUse(scope, m.computerUse.binder, config)
}
func (m *MainPrint) closeNativeComputerUse() {
	if m.computerUse == nil || m.agentChild {
		return
	}
	if m.computerUse.binder != nil {
		_ = m.computerUse.binder.Close(context.Background())
	}
	if m.computerUse.ownsService {
		_ = m.computerUse.service.Close(context.Background())
	}
	m.computerUse = nil
}

// Source paths remain explicit. No runtime config field points to a simulated
// stdio peer or old language business implementation.
func (m *MainPrint) computerUseConfigPath() string {
	return filepath.Join(m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "cc-haha", "computer-use-config.json")
}
