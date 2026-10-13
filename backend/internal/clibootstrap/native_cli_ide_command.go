package clibootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ideintegration"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
)

type nativeIDEPending struct {
	id, mode, currentName string
	choices               map[string]ideintegration.IDE
	current               protocol.JSONObject
	installChoices        map[string]bool
}

func nativeIDEConfig(ide ideintegration.IDE) protocol.JSONObject {
	kind := "sse-ide"
	if strings.HasPrefix(ide.URL, "ws:") {
		kind = "ws-ide"
	}
	config := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain(kind)}, protocol.JSONField{Name: "url", Value: quoteMain(ide.URL)}, protocol.JSONField{Name: "ideName", Value: ide.NameJSON()})
	if auth := ide.AuthJSON(); auth != nil {
		config.Set("authToken", auth)
	}
	config.Set("ideRunningInWindows", rawMain(ide.RunningInWindows))
	config.Set("scope", quoteMain("dynamic"))
	return config
}

func (m *MainPrint) NativeIDECommand(ctx context.Context, args string) (tui.IDEPicker, error) {
	var props tui.IDEPicker
	receipt, e := m.Owner.Submit(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if !m.options.Interactive || !m.options.Native.NativeProcess || m.closed {
			return mainCap("interactive IDE command")
		}
		env := m.nativeOpenAIEnvironment()
		cwd := m.state.Snapshot().CWD
		service := ideintegration.NewNative(env["CLAUDE_CONFIG_DIR"], cwd, m.options.Platform, env, os.Getppid())
		future, e := nativeSkillWork(m, scope, func() ([]ideintegration.IDE, error) { return service.Detect(ctx, true) })
		if e != nil {
			return e
		}
		ides, e := future.Await(scope)
		if e != nil {
			return e
		}
		mode := "select"
		if protocol.TrimJavaScriptString(args) == "open" {
			mode = "open"
		}
		pending := &nativeIDEPending{id: uuidMain(), mode: mode, choices: map[string]ideintegration.IDE{}}
		props = tui.IDEPicker{ID: pending.id, Mode: mode, Title: "Select IDE", Description: "Connect to an IDE for integrated development features.", Current: "None"}
		current := nativeInProcessObject(m.nativeDynamicMCP.Get("ide"))
		if len(ides) == 0 && mode != "open" && !ideintegration.SupportedTerminal(ideintegration.Terminal(env), env) && m.ideIntegration != nil {
			future, e := nativeSkillWork(m, scope, func() ([]string, error) { return m.ideIntegration.extensions.Running(ctx), nil })
			if e != nil {
				return e
			}
			running, e := future.Await(scope)
			if e != nil {
				return e
			}
			if len(running) > 0 {
				pending.mode = "install"
				pending.installChoices = map[string]bool{}
				props.Mode = "install"
				props.Title = "Select IDE to install extension"
				props.Description = ""
				for _, kind := range running {
					props.Options = append(props.Options, tui.IDEOption{Value: kind, Label: ideintegration.IDEName(kind)})
					pending.installChoices[kind] = true
				}
				m.idePicker = pending
				return nil
			}
		}
		if current.String("type") != "ws-ide" && current.String("type") != "sse-ide" {
			current = protocol.JSONObject{}
		}
		for _, ide := range ides {
			if !ide.Valid {
				props.Unavailable = append(props.Unavailable, ide.Name+": "+strings.Join(ide.WorkspaceFolders, ", "))
				continue
			}
			value := fmt.Sprint(ide.Port)
			pending.choices[value] = ide
			props.Options = append(props.Options, tui.IDEOption{Value: value, Label: ide.Name, Description: strings.Join(ide.WorkspaceFolders, ", ")})
			if current.String("url") == ide.URL {
				pending.current = current
				pending.currentName = ide.Name
				props.Current = value
			}
		}
		if mode == "open" {
			props.Title = "Select an IDE to open the project"
			props.Description = ""
		} else {
			if len(props.Options) > 0 {
				props.Options = append(props.Options, tui.IDEOption{Value: "None", Label: "None"})
			} else {
				props.Description = "No available IDEs detected. Make sure your IDE has the Claude Code extension or plugin installed and is running."
			}
			global, e := m.Global.Get()
			if e != nil {
				return e
			}
			terminal := ideintegration.Terminal(env)
			supported := ideintegration.SupportedTerminal(terminal, env)
			props.AskAutoConnect = !supported && string(global.Get("autoConnectIde")) != "true" && string(global.Get("hasIdeAutoConnectDialogBeenShown")) != "true"
			props.AskDisableAutoConnect = !supported && string(global.Get("autoConnectIde")) == "true"
		}
		m.idePicker = pending
		return nil
	})
	if e == nil {
		e = receipt.Wait()
	}
	return props, e
}

func (m *MainPrint) ApplyNativeIDESelection(ctx context.Context, id string, result tui.IDEPickerResult) (string, error) {
	var notice string
	active, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	receipt, e := m.Owner.Submit(active, cooperative.Read, func(scope *cooperative.Scope) error {
		if handled, e := m.applyNativeIDEOnboarding(id, result); handled || e != nil {
			return e
		}
		pending := m.idePicker
		if pending == nil || pending.id != id {
			return errors.New("stale IDE selection callback")
		}
		m.idePicker = nil
		if result.Cancelled {
			notice = "IDE selection cancelled"
			if pending.mode == "open" {
				notice = "Exited without opening IDE"
			}
			return nil
		}
		if pending.mode == "install" {
			if !pending.installChoices[result.Value] || m.ideIntegration == nil {
				return errors.New("unknown IDE extension selection")
			}
			state := m.ideIntegration
			state.mu.Lock()
			if state.closed {
				state.mu.Unlock()
				return context.Canceled
			}
			state.manualKind = result.Value
			state.work.Add(1)
			state.mu.Unlock()
			go func() {
				defer state.work.Done()
				m.startNativeIDESearch(state)
				m.runNativeIDEExtension(state.ctx, state, result.Value)
			}()
			notice = "Installed extension to " + ideintegration.IDEName(result.Value)
			if ideintegration.JetBrainsIDE(result.Value) {
				notice = "Installed plugin to " + ideintegration.IDEName(result.Value) + "\nPlease restart your IDE completely for it to take effect"
			}
			return nil
		}
		ide, selected := pending.choices[result.Value]
		if result.Value != "None" && !selected {
			return errors.New("unknown IDE selection")
		}
		if pending.mode == "open" {
			if !selected {
				notice = "No IDE selected."
				return nil
			}
			cwd := m.state.Snapshot().CWD
			env := m.nativeOpenAIEnvironment()
			name := strings.ToLower(ide.Name)
			if strings.Contains(name, "vscode") || strings.Contains(name, "cursor") || strings.Contains(name, "windsurf") {
				future, e := nativeSkillWork(m, scope, func() (worktree.ProcessResult, error) {
					return worktree.NativeRun(active, worktree.ProcessRequest{Command: "code", Arguments: []string{cwd}, CWD: cwd, Environment: env})
				})
				if e != nil {
					return e
				}
				output, e := future.Await(scope)
				if e != nil {
					return e
				}
				if output.Code == 0 {
					notice = "Opened project in " + ide.Name
				} else {
					notice = "Failed to open in " + ide.Name + ". Try opening manually: " + cwd
				}
			} else {
				notice = "Please open the project manually in " + ide.Name + ": " + cwd
			}
			return nil
		}
		if result.AutoConnect != nil || result.DisableAutoConnect {
			if e := m.Global.Save(func(global *protocol.JSONObject) (*protocol.JSONObject, error) {
				next := global.Clone()
				if result.AutoConnect != nil {
					next.Set("autoConnectIde", rawMain(*result.AutoConnect))
					next.Set("hasIdeAutoConnectDialogBeenShown", rawMain(true))
				} else {
					next.Set("autoConnectIde", rawMain(false))
				}
				return &next, nil
			}); e != nil {
				return e
			}
		}
		if pending.current.IsObject() {
			m.nativeDynamicMCP.Delete("ide")
		}
		if selected {
			m.nativeDynamicMCP.Set("ide", rawMain(nativeIDEConfig(ide)))
		}
		if e := m.mcpTools.Discover(scope); e != nil {
			return e
		}
		if e := m.Host.RefreshNativeToolCatalog(scope); e != nil {
			return e
		}
		if !selected {
			notice = "No IDE selected."
			if pending.current.IsObject() {
				notice = "Disconnected from " + pending.currentName + "."
			}
			return nil
		}
		status, e := m.mcpTools.Status(scope)
		if e != nil {
			return e
		}
		for _, peer := range status {
			if peer.String("name") == "ide" {
				if peer.String("status") == "connected" {
					notice = "Connected to " + ide.Name + "."
				} else {
					notice = "Failed to connect to " + ide.Name + "."
				}
				break
			}
		}
		if notice == "" {
			notice = "Failed to connect to " + ide.Name + "."
		}
		return nil
	})
	if e == nil {
		e = receipt.Wait()
	}
	return notice, e
}
