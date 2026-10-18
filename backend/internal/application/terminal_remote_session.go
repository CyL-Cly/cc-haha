package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/ccrsessions"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

// terminalRemoteSession keeps the normal Session and its sole native Owner for
// local JSX commands and presentation. Remote prompts are CCR /events inputs;
// they never enter the local model ASK or the RemoteIO worker transport.
type terminalRemoteSession struct {
	*Session
	service          *ccrsessions.Service
	manager          *ccrsessions.Manager
	sid              string
	hasInitialPrompt bool
	hasUpdatedTitle  bool
	state            tui.TerminalRemoteState
	projection       *Projection
	pending          map[string]tui.PermissionRequest
	pendingOrder     []string
	sent             map[string]bool
	sentOrder        []string
	tasks            map[string]bool
	commands         []tui.Completion
	responseTimer    *time.Timer
	compacting       bool
	closed           bool
	listenersMu      sync.Mutex
	listeners        map[uint64]func(tui.Event)
	listenerID       uint64
}

func newTerminalRemoteSession(ctx context.Context, local *Session, startup *cliRemoteStartup) (*terminalRemoteSession, error) {
	if local == nil || local.process == nil || !local.native.NativeProcess || startup == nil || startup.created == nil || startup.service == nil || !startup.service.RemoteBackendEnabled() {
		return nil, cooperative.ErrRestricted
	}
	r := &terminalRemoteSession{Session: local, service: startup.service, sid: startup.created.ID, hasInitialPrompt: startup.prompt != "", projection: NewProjection(), pending: map[string]tui.PermissionRequest{}, sent: map[string]bool{}, tasks: map[string]bool{}, listeners: map[uint64]func(tui.Event){}}
	r.state = tui.TerminalRemoteState{Connection: "connecting", Loading: r.hasInitialPrompt, SessionURL: ccrsessions.RemoteSessionURL(local.native.Environment, r.sid) + "?m=0"}
	commands, _ := local.SubscribeCommands(nil)
	for _, command := range commands {
		if sdkhost.IsRemoteSafeCommand(command.Name) {
			r.commands = append(r.commands, command)
		}
	}
	r.commands = append(r.commands, tui.Completion{Name: "session", Kind: "local-jsx", Description: "Show remote session URL"})
	manager, err := r.service.NewRemoteManager(ctx, r.sid, ccrsessions.ManagerCallbacks{Message: func(message protocol.JSONObject) {
		_ = r.onOwner(context.Background(), cooperative.Input, func(*cooperative.Scope) error { return r.receive(message) })
	}, State: func(state string) {
		_ = r.onOwner(context.Background(), cooperative.Input, func(*cooperative.Scope) error {
			r.state.Connection = state
			if state == "reconnecting" || state == "disconnected" {
				clear(r.tasks)
				r.state.BackgroundTasks = 0
			}
			if state == "disconnected" {
				r.state.Loading = false
			}
			return r.publishState()
		})
	}})
	if err != nil {
		return nil, err
	}
	if err := r.onOwner(ctx, cooperative.Input, func(*cooperative.Scope) error { r.manager = manager; return nil }); err != nil {
		manager.Close()
		return nil, err
	}
	return r, nil
}
func (r *terminalRemoteSession) StartTerminalRemote(ctx context.Context) error {
	return r.onOwner(ctx, cooperative.Input, func(*cooperative.Scope) error { r.manager.Start(); return nil })
}
func (r *terminalRemoteSession) initialRemoteState(ctx context.Context) (*tui.TerminalRemoteState, error) {
	var result tui.TerminalRemoteState
	err := r.onOwner(ctx, cooperative.Read, func(*cooperative.Scope) error { result = r.state; return nil })
	return &result, err
}
func (r *terminalRemoteSession) onOwner(ctx context.Context, kind cooperative.Class, work func(*cooperative.Scope) error) error {
	receipt, err := r.process.Owner.SubmitIndependent(ctx, kind, func(scope *cooperative.Scope) error {
		if r.closed {
			return cooperative.ErrClosed
		}
		return work(scope)
	})
	if err == nil {
		err = receipt.Wait()
	}
	return err
}
func (r *terminalRemoteSession) publish(event tui.Event) error {
	// CCR subscription messages are process-owned external events. They can
	// arrive before any human input or after its HTTP POST has returned, so
	// they do not borrow a local model ASK turn identity.
	event.Background = true
	r.listenersMu.Lock()
	listeners := []func(tui.Event){}
	for _, listener := range r.listeners {
		listeners = append(listeners, listener)
	}
	r.listenersMu.Unlock()
	for _, listener := range listeners {
		listener(event)
	}
	return nil
}
func (r *terminalRemoteSession) publishState() error {
	state := r.state
	return r.publish(tui.Event{Kind: tui.TerminalRemoteStateChanged, RemoteState: &state})
}
func (r *terminalRemoteSession) SubscribeEnvironmentHookNotices(listener func(tui.Event)) func() {
	stopLocal := r.Session.SubscribeEnvironmentHookNotices(listener)
	r.listenersMu.Lock()
	r.listenerID++
	id := r.listenerID
	if listener != nil {
		r.listeners[id] = listener
	}
	r.listenersMu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() { stopLocal(); r.listenersMu.Lock(); delete(r.listeners, id); r.listenersMu.Unlock() })
	}
}
func (r *terminalRemoteSession) SubscribeCommands(listener func([]tui.Completion)) ([]tui.Completion, func()) {
	commands := []tui.Completion{}
	_ = r.onOwner(context.Background(), cooperative.Read, func(*cooperative.Scope) error { commands = tui.CloneCompletions(r.commands); return nil })
	// Source starts from the already pre-filtered remote-safe local set. Init
	// can only remove entries outside that set, so this catalog stays stable.
	return commands, func() {}
}
func (r *terminalRemoteSession) InvokeAction(ctx context.Context, action string, emit tui.Emit) error {
	if strings.HasPrefix(action, "command:panel:") {
		parts := strings.SplitN(strings.TrimPrefix(action, "command:panel:"), "\x00", 2)
		text := "/" + parts[0]
		if len(parts) > 1 && parts[1] != "" {
			text += " " + parts[1]
		}
		return r.Submit(ctx, tui.Input{TurnID: newID(), Text: text, Mode: "prompt"}, emit)
	}
	if strings.HasPrefix(action, "command:") {
		return r.Submit(ctx, tui.Input{TurnID: newID(), Text: "/" + strings.TrimPrefix(action, "command:"), Mode: "prompt"}, emit)
	}
	return r.Session.InvokeAction(ctx, action, emit)
}
func (r *terminalRemoteSession) Submit(ctx context.Context, input tui.Input, emit tui.Emit) error {
	if command := tui.ParseSlashCommand(input.Text); input.Mode != "bash" && command != nil {
		local := false
		if err := r.onOwner(ctx, cooperative.Read, func(*cooperative.Scope) error {
			for _, known := range r.commands {
				local = local || known.Name == command.CommandName && known.Kind == "local-jsx"
			}
			return nil
		}); err != nil {
			return err
		}
		if local {
			if command.CommandName == "session" {
				if emit != nil {
					return emit(tui.Event{Kind: tui.LocalCommandOutput, Text: r.state.SessionURL})
				}
				return nil
			}
			return r.Session.Submit(ctx, input, emit)
		}
	}
	text, err := terminalInputText(input)
	if err != nil {
		return err
	}
	units, _ := protocol.DecodeJavaScriptJSONString(text)
	for len(units) > 0 && protocol.IsJavaScriptWhitespace(rune(units[0])) {
		units = units[1:]
	}
	for len(units) > 0 && protocol.IsJavaScriptWhitespace(rune(units[len(units)-1])) {
		units = units[:len(units)-1]
	}
	text = protocol.QuoteJavaScriptUTF16(units)
	content := text
	if len(input.PastedContents) > 0 || len(input.Attachments) > 0 {
		// Remote REPL sends the original pasted blocks, rather than local image
		// resize/attachment annotations intended for a local model request.
		blocks := []protocol.JSONObject{}
		if string(text) != `""` {
			blocks = append(blocks, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("text")}, protocol.JSONField{Name: "text", Value: text}))
		}
		for _, pasted := range remotePastedContents(input) {
			blocks = append(blocks, pasted)
		}
		content, err = json.Marshal(blocks)
		if err != nil {
			return err
		}
	}
	id := newID()
	if err := r.onOwner(ctx, cooperative.Input, func(*cooperative.Scope) error {
		r.sent[id] = true
		r.sentOrder = append(r.sentOrder, id)
		if len(r.sentOrder) > 50 {
			delete(r.sent, r.sentOrder[0])
			r.sentOrder = r.sentOrder[1:]
		}
		if r.responseTimer != nil {
			r.responseTimer.Stop()
		}
		r.state.Loading = true
		return r.publishState()
	}); err != nil {
		return err
	}
	success, failure := r.manager.SendMessage(ctx, content, id)
	var updateDescription json.RawMessage
	settled := r.onOwner(ctx, cooperative.Input, func(*cooperative.Scope) error {
		if !success {
			r.state.Loading = false
			if failure != nil {
				_ = r.publish(tui.Event{Kind: tui.Notice, IsError: true, Text: failure.Error()})
			}
			return r.publishState()
		}
		if !r.hasInitialPrompt && !r.hasUpdatedTitle {
			r.hasUpdatedTitle = true
			updateDescription = remoteTextContent(content)
		}
		timeout := 60 * time.Second
		if r.compacting {
			timeout = 180 * time.Second
		}
		r.responseTimer = time.AfterFunc(timeout, func() {
			_ = r.onOwner(context.Background(), cooperative.Input, func(*cooperative.Scope) error {
				_ = r.publish(tui.Event{Kind: tui.Notice, IsError: true, Text: "Remote session may be unresponsive. Attempting to reconnect…"})
				r.manager.Reconnect()
				return nil
			})
		})
		return nil
	})
	if settled == nil && updateDescription != nil {
		_ = r.manager.QueueTitleUpdate(updateDescription)
	}
	return settled
}

func remoteTextContent(content json.RawMessage) json.RawMessage {
	if _, err := protocol.DecodeJavaScriptJSONString(content); err == nil {
		return append(json.RawMessage(nil), content...)
	}
	var blocks []protocol.JSONObject
	if json.Unmarshal(content, &blocks) != nil {
		return nil
	}
	result := []uint16{}
	count := 0
	for _, block := range blocks {
		if block.String("type") != "text" {
			continue
		}
		units, err := protocol.DecodeJavaScriptJSONString(block.Get("text"))
		if err != nil {
			continue
		}
		if count > 0 {
			result = append(result, ' ')
		}
		result = append(result, units...)
		count++
	}
	return protocol.QuoteJavaScriptUTF16(result)
}
func remotePastedContents(input tui.Input) []protocol.JSONObject {
	blocks := []protocol.JSONObject{}
	ids := []int{}
	for id := range input.PastedContents {
		ids = append(ids, id)
	}
	// Object.values follows integer-index order for the original paste map.
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			if ids[j] < ids[i] {
				ids[i], ids[j] = ids[j], ids[i]
			}
		}
	}
	for _, id := range ids {
		value := input.PastedContents[id]
		if value.Type == "image" {
			media := value.MediaType
			if media == "" {
				media = "image/png"
			}
			blocks = append(blocks, obj(raw(map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": media, "data": value.Content}})))
		} else {
			blocks = append(blocks, obj(raw(map[string]any{"type": "text", "text": value.Content})))
		}
	}
	for _, block := range input.Attachments {
		blocks = append(blocks, obj(block))
	}
	return blocks
}
func (r *terminalRemoteSession) receive(message protocol.JSONObject) error {
	if r.responseTimer != nil {
		r.responseTimer.Stop()
		r.responseTimer = nil
	}
	kind := message.String("type")
	if kind == "control_request" {
		request, _ := message.Object("request")
		id := message.String("request_id")
		if request.String("subtype") != "can_use_tool" {
			return r.manager.QueueControl(obj(raw(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "error", "request_id": id, "error": "Unsupported control request subtype: " + request.String("subtype")}})))
		}
		description := request.String("description")
		if description == "" {
			description = request.String("tool_name") + " requires permission"
		}
		permission := tui.PermissionRequest{ID: id, ToolName: request.String("tool_name"), Input: request.Get("input"), Description: description, Payload: raw(map[string]any{"toolUseId": request.Get("tool_use_id"), "description": description, "permissionSuggestions": request.Get("permission_suggestions"), "blockedPath": request.Get("blocked_path")}), Choices: []tui.PermissionChoice{{Key: "y", Label: "Yes", Decision: tui.PermissionDecision{Allowed: true, UpdatedInput: request.Get("input")}}, {Key: "n", Label: "No", Decision: tui.PermissionDecision{Message: raw("User denied permission")}}}}
		r.pending[id] = permission
		r.pendingOrder = append(r.pendingOrder, id)
		r.state.Loading = false
		_ = r.publish(tui.Event{Kind: tui.PermissionAsked, Permission: &permission})
		return r.publishState()
	}
	if kind == "control_cancel_request" {
		if permission, ok := r.pending[message.String("request_id")]; ok {
			r.removeRemotePermission(permission.ID)
			_ = r.publish(tui.Event{Kind: tui.PermissionClosed, Permission: &permission, Cancelled: true})
		}
		r.state.Loading = true
		return r.publishState()
	}
	if kind == "control_response" || kind == "user" {
		// CCR mode ignores user messages; the local REPL already displays the
		// submitted prompt. Bounded UUID entries survive repeated echoes.
		return nil
	}
	if kind == "result" {
		r.state.Loading = false
		if message.String("subtype") != "success" {
			var failures []string
			_ = json.Unmarshal(message.Get("errors"), &failures)
			text := strings.Join(failures, ", ")
			if text == "" {
				text = "Unknown error"
			}
			_ = r.publish(tui.Event{Kind: tui.Notice, IsError: true, Text: text})
		}
		return r.publishState()
	}
	if kind == "system" {
		switch message.String("subtype") {
		case "init":
			return r.publish(tui.Event{Kind: tui.Notice, Text: "Remote session initialized (model: " + message.String("model") + ")"})
		case "task_started":
			r.tasks[message.String("task_id")] = true
		case "task_notification":
			delete(r.tasks, message.String("task_id"))
		case "task_progress":
			return nil
		case "status":
			wasCompacting := r.compacting
			r.compacting = message.String("status") == "compacting"
			if wasCompacting && r.compacting || message.String("status") == "" {
				return nil
			}
			text := "Status: " + message.String("status")
			if r.compacting {
				text = "Compacting conversation…"
			}
			return r.publish(tui.Event{Kind: tui.Notice, Text: text})
		case "compact_boundary":
			r.compacting = false
			return r.publish(tui.Event{Kind: tui.Notice, Text: "Conversation compacted"})
		default:
			return nil
		}
		r.state.BackgroundTasks = len(r.tasks)
		return r.publishState()
	}
	if kind == "tool_progress" {
		return r.publish(tui.Event{Kind: tui.Notice, Text: fmt.Sprintf("Tool %s running for %ss…", message.String("tool_name"), message.Get("elapsed_time_seconds"))})
	}
	frames, err := r.projection.Translate(raw(message))
	if err != nil {
		return err
	}
	for _, frame := range frames {
		value := obj(frame)
		event := tui.Event{}
		switch value.String("type") {
		case "content_delta":
			event.Kind, event.Text = tui.TextDelta, value.String("text")
		case "thinking":
			event.Kind, event.Text = tui.ThinkingDelta, value.String("text")
		case "tool_use_complete":
			event.Kind, event.ToolID, event.ToolName, event.Input = tui.ToolStarted, value.String("toolUseId"), value.String("toolName"), value.Get("input")
		default:
			continue
		}
		_ = r.publish(event)
	}
	return nil
}
func (r *terminalRemoteSession) RespondPermission(ctx context.Context, id string, decision tui.PermissionDecision) error {
	var response protocol.JSONObject
	err := r.onOwner(ctx, cooperative.Input, func(*cooperative.Scope) error {
		request, ok := r.pending[id]
		if !ok {
			return errors.New("permission request is no longer pending")
		}
		r.removeRemotePermission(id)
		payload := map[string]any{"behavior": "deny", "message": "User denied permission"}
		if decision.Allowed {
			payload = map[string]any{"behavior": "allow", "updatedInput": request.Input}
			if decision.UpdatedInput != nil {
				payload["updatedInput"] = decision.UpdatedInput
			}
			r.state.Loading = true
		} else if decision.Message != nil {
			payload["message"] = decision.Message
		}
		response = obj(raw(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": id, "response": payload}}))
		_ = r.publish(tui.Event{Kind: tui.PermissionClosed, Permission: &request, Cancelled: !decision.Allowed})
		return r.publishState()
	})
	if err != nil {
		return err
	}
	// The container owns permission updates and tool execution. No local rule
	// or local tool can be applied by responding to this remote request.
	return r.manager.QueueControl(response)
}
func (r *terminalRemoteSession) Cancel() {
	_, _ = r.process.Owner.SubmitIndependent(context.Background(), cooperative.Cancel, func(*cooperative.Scope) error {
		if r.closed || !r.state.Loading && len(r.pendingOrder) == 0 {
			return nil
		}
		if r.responseTimer != nil {
			r.responseTimer.Stop()
			r.responseTimer = nil
		}
		r.state.Loading = false
		_ = r.publishState()
		if len(r.pendingOrder) > 0 {
			id := r.pendingOrder[0]
			r.pendingOrder = nil
			if _, ok := r.pending[id]; ok {
				delete(r.pending, id)
				_ = r.manager.QueueControl(obj(raw(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": id, "response": map[string]any{"behavior": "deny", "message": "User aborted"}}})))
			}
			return nil
		}
		_ = r.manager.QueueControl(obj(raw(map[string]any{"type": "control_request", "request_id": newID(), "request": map[string]string{"subtype": "interrupt"}})))
		return nil
	})
}
func (r *terminalRemoteSession) Close(ctx context.Context) error {
	return r.CloseWithReason(ctx, "other")
}
func (r *terminalRemoteSession) CloseWithReason(ctx context.Context, reason string) error {
	_ = r.onOwner(context.Background(), cooperative.Cancel, func(*cooperative.Scope) error {
		r.closed = true
		if r.responseTimer != nil {
			r.responseTimer.Stop()
		}
		clear(r.pending)
		r.pendingOrder = nil
		return nil
	})
	r.manager.Close()
	return r.Session.closeWithReason(ctx, reason)
}

func (r *terminalRemoteSession) initialHistory(prompt string) []protocol.JSONObject {
	stamp := r.native.Now().UTC().Format(time.RFC3339Nano)
	history := []protocol.JSONObject{obj(raw(map[string]any{"type": "system", "subtype": "informational", "level": "info", "content": "/remote-control is active. Code in CLI or at " + r.state.SessionURL, "uuid": newID(), "timestamp": stamp}))}
	if prompt != "" {
		history = append(history, obj(raw(map[string]any{"type": "user", "uuid": newID(), "timestamp": stamp, "message": map[string]string{"role": "user", "content": prompt}})))
	}
	return history
}

func (r *terminalRemoteSession) removeRemotePermission(id string) {
	delete(r.pending, id)
	retained := r.pendingOrder[:0]
	for _, pending := range r.pendingOrder {
		if pending != id {
			retained = append(retained, pending)
		}
	}
	r.pendingOrder = retained
}
