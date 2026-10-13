// Package tui owns terminal input and rendering. Agent execution, permission
// policy, transcripts and provider requests remain owned by the shared Session.
package tui

import (
	"context"
	"encoding/json"
	"time"
)

// Input is a terminal submission. TurnID is a host correlation token only; it
// must never replace an Agent message UUID or be added to model-visible input.
type Input struct {
	TurnID string
	Text   string
	// PreExpansionText is the original editable display, including paste pills.
	// Text is the Source handlePromptSubmit finalInput, expanded exactly once.
	PreExpansionText        string
	PastedContents          map[int]PastedContent
	Mode                    string
	Attachments             []json.RawMessage
	SuppressWorkflowKeyword bool
}

type PermissionDecision struct {
	// EditedPrefix is presentation input only. The actual owning permission
	// host validates its request and creates/persists the rule after approval.
	EditedPrefix      *string
	Allowed           bool
	PermissionUpdates json.RawMessage
	UpdatedInput      json.RawMessage
	Rule              json.RawMessage
	Message           json.RawMessage
	AcceptFeedback    json.RawMessage
	ContentBlocks     []json.RawMessage
	// UI rejections retain Source's ask decision when returning feedback/images.
	Behavior string
}

// PermissionRequest retains the original payload so host-specific approval
// choices can be rendered without constructing a second permission policy.
type PermissionRequest struct {
	Title          string
	DisplayOnly    bool
	ID             string
	ToolName       string
	Description    string
	Input          json.RawMessage
	Payload        json.RawMessage
	Choices        []PermissionChoice
	PermissionMode string
}

// Choices are supplied by the permission host. The terminal does not build
// rules or guess how edited tool input should be persisted.
type PermissionChoice struct {
	Editable     bool
	InitialValue string
	Key          string
	Label        string
	Decision     PermissionDecision
}

type EventKind string

const (
	TextDelta              EventKind = "text_delta"
	ThinkingDelta          EventKind = "thinking_delta"
	ToolStarted            EventKind = "tool_started"
	ToolProgress           EventKind = "tool_progress"
	ToolFinished           EventKind = "tool_finished"
	BashProgress           EventKind = "bash_progress"
	BashFinished           EventKind = "bash_finished"
	PermissionAsked        EventKind = "permission_asked"
	PermissionClosed       EventKind = "permission_closed"
	Notice                 EventKind = "notice"
	PermissionModeChanged  EventKind = "permission_mode_changed"
	TurnFinished           EventKind = "turn_finished"
	TurnFailed             EventKind = "turn_failed"
	ModelPickerOpened      EventKind = "model_picker_opened"
	ModelPickerClosed      EventKind = "model_picker_closed"
	DoctorOpened           EventKind = "doctor_opened"
	IDEPickerOpened        EventKind = "ide_picker_opened"
	CommandsChanged        EventKind = "commands_changed"
	ThinkingPickerOpened   EventKind = "thinking_picker_opened"
	ThinkingPickerClosed   EventKind = "thinking_picker_closed"
	LocalUserMessages      EventKind = "local_user_messages"
	LocalProcessedMessages EventKind = "local_processed_messages"
	LocalCommandOutput     EventKind = "local_command_output"
	MemorySaved            EventKind = "memory_saved"
	TeammateInboxChanged   EventKind = "teammate_inbox_changed"
	TeammateMessages       EventKind = "teammate_messages"
	TeammateTurnStarted    EventKind = "teammate_turn_started"
	TeammateTurnFinished   EventKind = "teammate_turn_finished"
)

type Event struct {
	Redacted       bool
	Background     bool
	TurnID         string
	Kind           EventKind
	Text           string
	ToolID         string
	ToolName       string
	Input          json.RawMessage
	Result         json.RawMessage
	IsError        bool
	Output         json.RawMessage
	Permission     *PermissionRequest
	Cancelled      bool
	Err            error
	ModelPicker    *ModelPicker
	IDEPicker      *IDEPicker
	Commands       []Completion
	ThinkingPicker *ThinkingPicker
	NoticeTimeout  time.Duration
	PermissionMode string
	UserMessages   []string
	LocalMessages  []LocalProcessedMessage
}

// These are internal presentation values from the shared processUserInput
// consumer. They are never a second SDK/API message or model result.
type LocalProcessedMessage struct {
	Kind, Text, Level string
}

type Emit func(Event) error

// Session is implemented by the production interactive host. Submit must emit
// synchronously in execution order, and return only after the turn's cleanup.
// The TUI never requests models, runs tools or writes conversation state.
type Session interface {
	Submit(context.Context, Input, Emit) error
	Cancel()
	RespondPermission(context.Context, string, PermissionDecision) error
	Close(context.Context) error
}

// ActionSession is the optional interactive host action boundary. The host
// owns command dispatch, app/session state and Agent execution. The terminal
// must not submit a command string as an ordinary model prompt.
type ActionSession interface {
	InvokeAction(context.Context, string, Emit) error
}

// CommandSource atomically subscribes and returns the current shared host
// snapshot. File changes reach an idle terminal without starting an Agent turn.
type CommandSource interface {
	SubscribeCommands(func([]Completion)) ([]Completion, func())
}

type Completion struct {
	Hidden            bool
	Name              string
	Description       string
	Kind              string
	ArgumentHint      string
	RequiresArguments bool
	ArgumentNames     []string
}

func CloneCompletions(values []Completion) []Completion {
	result := append([]Completion{}, values...)
	for i := range result {
		result[i].ArgumentNames = append([]string(nil), result[i].ArgumentNames...)
	}
	return result
}

// ForegroundTaskSource publishes only availability from the actual task owner.
// The terminal never derives task state from an in-flight model request.
type ForegroundTaskSource interface {
	SubscribeForegroundTasks(context.Context, func(bool)) (bool, func(), error)
}

// The terminal mirrors the shared Host's permission context for its footer;
// changing this string never grants a permission or changes Agent behavior.
type PermissionModeSource interface {
	SubscribePermissionMode(func(string)) (string, func())
}
