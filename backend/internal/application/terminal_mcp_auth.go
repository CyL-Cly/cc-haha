package application

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/mcpauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

type terminalMCPAuthJob struct {
	cancel context.CancelFunc
	done   chan struct{}
}
type terminalMCPAuthRuntime struct {
	mu     sync.Mutex
	closed bool
	jobs   map[*terminalMCPAuthJob]struct{}
}

type terminalMCPAuthPanelState struct {
	job                      *terminalMCPAuthJob
	url                      string
	submit                   func(string)
	clearing                 bool
	effectivelyAuthenticated bool
	proxy                    bool
	browserOpened            bool
	complete                 chan struct{}
	agent                    bool
}

func refreshTerminalMCPAuthentication(state *terminalLocalPanelState) {
	value := state.mcpMenu.auth
	p := &state.props
	p.MCPAuthentication = true
	p.MCPAuthenticationCopy = !value.agent
	p.MCPCatalog = false
	p.SubmitAction = "mcp-auth-submit"
	p.PreserveFields = true
	p.Rows = nil
	p.Fields = nil
	p.Actions = nil
	p.RawInputKeys = nil
	p.HideDetails = true
	p.DismissAction = "mcp-auth-cancel"
	p.Title = "Authenticating with " + state.mcpMenu.server.String("name") + "…"
	p.Description = "A browser window will open for authentication"
	if string(state.mcpMenu.server.Get("usesIdentityProvider")) == "true" {
		p.Description = "Authenticating via your identity provider"
	}
	if value.clearing {
		p.Title = "Clear authentication for " + state.mcpMenu.server.String("name")
		p.Description = "Clearing authentication…"
	}
	if value.proxy && value.clearing {
		p.Description = "Find the MCP server in the browser and click \"Disconnect\"."
		if !value.browserOpened {
			p.Description = "Press Enter to open connector settings in your browser."
		}
	}
	if value.url != "" {
		copyHint := " (c copy)"
		if value.agent {
			copyHint = ""
		}
		p.Description += "\nIf your browser doesn't open automatically, copy this URL manually" + copyHint + "\n" + value.url
	}
	if value.url != "" && value.submit != nil {
		p.Description += "\nIf the redirect page shows a connection error, paste the URL from your browser's address bar:"
		p.Fields = []tui.LocalPanelField{{Name: "callback", Label: "URL >"}}
	}
	if value.proxy {
		label := "Press Enter after authenticating in your browser."
		if value.clearing {
			label = "Press Enter to open the browser."
			if value.browserOpened {
				label = "Press Enter when done."
			}
		}
		p.Description += "\n" + label
		p.RawInputKeys = []string{"enter"}
		p.Actions = []tui.LocalPanelAction{{Key: "enter", Name: "mcp-auth-submit", Label: "continue"}}
	}
	p.Description += "\nReturn here after authenticating in your browser. Press Esc to go back."
}

func (s *Session) closeTerminalMCPAuthPanel(state *terminalLocalPanelState) {
	if state == nil {
		return
	}
	state.mu.Lock()
	var job *terminalMCPAuthJob
	if menu := state.mcpMenu; menu != nil && menu.auth != nil {
		job = menu.auth.job
		menu.auth = nil
	}
	state.mu.Unlock()
	if job != nil {
		job.cancel()
		<-job.done
	}
}

// Authentication is a mounted local UI job, separate from a completed submit.
// Cancellation retires its exact mount before joining the actual native flow.
func (s *Session) applyTerminalMCPAuthentication(ctx context.Context, result tui.LocalPanelResult, emit tui.Emit) (bool, error) {
	authAction := result.Action == "mcp-auth" || result.Action == "mcp-reauth" || result.Action == "mcp-clear-auth" || result.Action == "mcp-claudeai-auth" || result.Action == "mcp-claudeai-clear-auth" || result.Action == "mcp-agent-auth"
	if !authAction && result.Action != "mcp-auth-submit" && result.Action != "mcp-auth-copy" && result.Action != "mcp-auth-cancel" && result.Action != "close" {
		return false, nil
	}
	s.mu.Lock()
	state := s.terminalPanel
	s.mu.Unlock()
	if state == nil || state.props.ID != result.ID {
		return false, nil
	}
	if result.Action == "close" {
		s.closeTerminalMCPAuthPanel(state)
		return false, nil
	}
	state.mu.Lock()
	menu := state.mcpMenu
	if menu == nil {
		state.mu.Unlock()
		return true, errors.New("MCP server menu is no longer active")
	}
	if result.Action == "mcp-auth-copy" {
		value := menu.auth
		address := ""
		if value != nil {
			address = value.url
		}
		state.mu.Unlock()
		if address == "" {
			return true, nil
		}
		if err := s.terminalCopyToClipboard(ctx, emit, address); err != nil {
			return true, err
		}
		state.mu.Lock()
		if menu.auth == value {
			state.props.Notice = "Copied!"
			state.props.NoticeUntil = time.Now().Add(2 * time.Second)
		}
		props := terminalPanelProjection(state.props)
		state.mu.Unlock()
		if emit != nil {
			return true, emit(tui.Event{Kind: tui.LocalPanelRefreshed, LocalPanel: &props, Background: true})
		}
		return true, nil
	}
	if result.Action == "mcp-auth-submit" {
		value := menu.auth
		var submit func(string)
		if value != nil {
			submit = value.submit
		}
		state.mu.Unlock()
		if value != nil && value.proxy {
			select {
			case value.complete <- struct{}{}:
			default:
			}
			return true, nil
		}
		if submit != nil {
			submit(protocol.TrimJavaScriptString(result.Fields["callback"]))
		}
		return true, nil
	}
	if result.Action == "mcp-auth-cancel" {
		state.mu.Unlock()
		s.closeTerminalMCPAuthPanel(state)
		state.mu.Lock()
		state.props.Notice = ""
		state.props.NoticeUntil = time.Time{}
		err := s.refreshTerminalPanel(ctx, state)
		props := terminalPanelProjection(state.props)
		state.mu.Unlock()
		if err != nil {
			return true, err
		}
		if emit != nil {
			return true, emit(tui.Event{Kind: tui.LocalPanelRefreshed, LocalPanel: &props, Background: true})
		}
		return true, nil
	}
	if menu.auth != nil {
		state.mu.Unlock()
		return true, nil
	}
	runtime := s.terminalMCPAuth
	runtime.mu.Lock()
	if runtime.closed {
		runtime.mu.Unlock()
		state.mu.Unlock()
		return true, context.Canceled
	}
	worker, cancel := context.WithCancel(context.WithoutCancel(ctx))
	job := &terminalMCPAuthJob{cancel: cancel, done: make(chan struct{})}
	value := &terminalMCPAuthPanelState{job: job, clearing: result.Action == "mcp-clear-auth" || result.Action == "mcp-claudeai-clear-auth", effectivelyAuthenticated: string(menu.server.Get("isAuthenticated")) == "true" || menu.server.String("status") == "connected" && len(array(menu.server.Get("toolMetadata"))) > 0, proxy: result.Action == "mcp-claudeai-auth" || result.Action == "mcp-claudeai-clear-auth", complete: make(chan struct{}, 1)}
	menu.auth = value
	value.agent = result.Action == "mcp-agent-auth"
	state.props.Notice = ""
	state.props.NoticeUntil = time.Time{}
	runtime.jobs[job] = struct{}{}
	runtime.mu.Unlock()
	refreshTerminalMCPAuthentication(state)
	initial := terminalPanelProjection(state.props)
	state.mu.Unlock()
	if emit != nil {
		if err := emit(tui.Event{Kind: tui.LocalPanelRefreshed, LocalPanel: &initial, Background: true}); err != nil {
			cancel()
		}
	}
	publish := func(update func()) {
		state.mu.Lock()
		if worker.Err() != nil || state.mcpMenu != menu || menu.auth != value {
			state.mu.Unlock()
			return
		}
		update()
		refreshTerminalMCPAuthentication(state)
		props := terminalPanelProjection(state.props)
		s.mu.Lock()
		active := !s.closed && s.terminalPanel == state
		s.mu.Unlock()
		state.mu.Unlock()
		if active && emit != nil {
			_ = emit(tui.Event{Kind: tui.LocalPanelRefreshed, LocalPanel: &props, Background: true})
		}
	}
	go func() {
		defer func() { cancel(); runtime.mu.Lock(); delete(runtime.jobs, job); close(job.done); runtime.mu.Unlock() }()
		var err error
		if value.agent {
			err = s.process.StartTerminalAgentMCPAuthentication(worker, menu.server.String("name"), func(address string) { publish(func() { value.url = address }) })
		} else if value.proxy {
			waitEnter := func() error {
				select {
				case <-worker.Done():
					return context.Cause(worker)
				case <-value.complete:
					return nil
				}
			}
			if value.clearing {
				err = waitEnter()
			}
			if err == nil {
				var address string
				address, err = s.process.TerminalMCPClaudeAIAuthURL(worker, menu.server.String("name"), value.clearing)
				if err == nil {
					publish(func() { value.url = address; value.browserOpened = true })
					_, err = s.process.OpenTerminalPluginLink(worker, address)
				}
			}
			if err == nil {
				err = waitEnter()
			}
			if err == nil {
				err = s.process.CompleteTerminalMCPClaudeAIAuthentication(worker, menu.server.String("name"), value.clearing)
			}
		} else if value.clearing {
			err = s.process.ClearTerminalMCPAuthentication(worker, menu.server.String("name"))
		} else {
			err = s.process.StartTerminalMCPAuthenticationWithState(worker, menu.server.String("name"), string(menu.server.Get("isAuthenticated")) == "true", func(url string) { publish(func() { value.url = url }) }, func(submit func(string)) { publish(func() { value.submit = submit }) })
		}
		if worker.Err() != nil {
			return
		}
		state.mu.Lock()
		if state.mcpMenu != menu || menu.auth != value {
			state.mu.Unlock()
			return
		}
		menu.auth = nil
		state.props.NoticeUntil = time.Time{}
		var cancelled mcpauth.AuthenticationCancelledError
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) && !errors.As(err, &cancelled) {
			state.props.Notice = err.Error()
		} else if err == nil {
			name := menu.server.String("name")
			message := "Authentication cleared for " + name + "."
			if value.proxy && value.clearing {
				message = "Disconnected from " + name + "."
			}
			if !value.clearing {
				current, e := s.process.TerminalMCPMenuSnapshot(worker)
				status := ""
				if e == nil {
					for _, server := range array(current.Get("servers")) {
						if server.String("name") == name {
							status = server.String("status")
						}
					}
				}
				switch status {
				case "connected":
					message = "Authentication successful. Connected to " + name + "."
					if value.effectivelyAuthenticated && !value.proxy {
						message = "Authentication successful. Reconnected to " + name + "."
					}
				case "needs-auth":
					message = "Authentication successful, but server still requires authentication. You may need to manually restart Claude Code."
				default:
					message = "Authentication successful, but server reconnection failed. You may need to manually restart Claude Code for the changes to take effect."
				}
				if value.agent {
					message = "Authentication successful for " + name + ". The server will connect when the agent runs."
				}
			}
			state.props.Notice = message
			state.mcpMenu = nil
			if state.pluginPanel != nil {
				state.pluginPanel.level = ""
			}
		}
		_ = s.refreshTerminalPanel(worker, state)
		props := terminalPanelProjection(state.props)
		s.mu.Lock()
		active := !s.closed && s.terminalPanel == state
		s.mu.Unlock()
		state.mu.Unlock()
		if active && emit != nil {
			_ = emit(tui.Event{Kind: tui.LocalPanelRefreshed, LocalPanel: &props, Background: true})
		}
	}()
	return true, nil
}

func (s *Session) closeTerminalMCPAuth() {
	runtime := s.terminalMCPAuth
	if runtime == nil {
		return
	}
	runtime.mu.Lock()
	runtime.closed = true
	jobs := []*terminalMCPAuthJob{}
	for job := range runtime.jobs {
		job.cancel()
		jobs = append(jobs, job)
	}
	runtime.mu.Unlock()
	for _, job := range jobs {
		<-job.done
	}
}
