// Package server is the desktop HTTP/WebSocket transport. Agent execution is
// injected from the application's shared CLI/desktop runtime, never a second
// harness or a TypeScript subprocess.
package server

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamworker"
)

type SessionOptions struct {
	CanContinueRuntime            func() bool
	OnRuntimeClosed               func(context.Context, LiveSession) error
	SideChat                      *SideChatLaunch
	TeamWorker                    *teamworker.Start
	ID, WorkDir, PermissionMode   string
	Resume                        bool
	Metadata                      json.RawMessage
	UpdateLaunchMetadata          func(context.Context, json.RawMessage) error
	OnCanonicalTranscriptMutation func(string) error
	Environment                   map[string]string
	OnSDK                         func(json.RawMessage)
	ResolveSessionReferences      func(context.Context, json.RawMessage, json.RawMessage) (json.RawMessage, error)
	PrepareRepositoryLaunch       func(context.Context) (RepositorySessionLaunch, error)
	FinalizeRepositoryLaunch      func(context.Context, string, json.RawMessage) error
}

// Emit receives complete Source ServerMessage frames, with optional and future
// fields intact. The shared application adapter owns SDK-to-desktop projection.
type Emit func(json.RawMessage)
type SessionFactory func(context.Context, SessionOptions, Emit) (LiveSession, error)

// Handle admits original ClientMessage bytes. A turn can run concurrently with
// permission/cancel controls. Snapshot is authoritative for reconnect; neither
// the WebSocket transport nor the renderer guesses the agent's state.
type LiveSession interface {
	Handle(context.Context, json.RawMessage) error
	Snapshot(context.Context) ([]json.RawMessage, error)
	Close(context.Context) error
}

// Only the original pending plan approval owns the interrupt/result/restart
// wait. Its socket reader remains available for EOF and ordinary controls.
type PlanRuntimeApprovalSession interface {
	IsPlanRuntimeApproval(json.RawMessage) bool
	CaptureRuntimeInput(context.Context) context.Context
}

// ComponentSession is the in-process counterpart of the original session's
// reload_plugins/mcp_reconnect/mcp_status control protocol.
type ComponentSession interface {
	ControlComponents(context.Context, string, string) (protocol.JSONObject, error)
}

type CollaborationSession interface {
	EnqueueCollaboration(context.Context, protocol.JSONObject, bool, func() bool) (protocol.JSONObject, error)
	CollaborationRuntime() protocol.JSONObject
}
