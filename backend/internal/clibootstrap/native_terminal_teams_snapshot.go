package clibootstrap

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// NativeTerminalTeamsSnapshot is the immutable file projection that supplied
// one committed dialog render. Only the actual native dialog can construct it;
// later polling cannot replace an accepted key handler's member or pane.
type NativeTerminalTeamsSnapshot struct {
	dialog  *NativeTerminalTeams
	value   protocol.JSONObject
	members []protocol.JSONObject
}

func (d *NativeTerminalTeams) CaptureSnapshot(ctx context.Context) (*NativeTerminalTeamsSnapshot, error) {
	value, err := d.Snapshot(ctx, "")
	if err != nil {
		return nil, err
	}
	var members []protocol.JSONObject
	if err := json.Unmarshal(value.Get("members"), &members); err != nil {
		return nil, err
	}
	return &NativeTerminalTeamsSnapshot{dialog: d, value: value.Clone(), members: cloneTerminalCachedTeams(members)}, nil
}

func (view *NativeTerminalTeamsSnapshot) Projection() protocol.JSONObject {
	if view == nil {
		return protocol.JSONObject{}
	}
	return view.value.Clone()
}

func (view *NativeTerminalTeamsSnapshot) OwnsDialog(dialog *NativeTerminalTeams) bool {
	return view != nil && dialog != nil && view.dialog == dialog
}

// Index is resolved from the application's opaque row identity, never from a
// supplied member JSON. A negative index selects the captured whole list.
func (view *NativeTerminalTeamsSnapshot) CaptureAction(action string, index int) *NativeTerminalTeamsAction {
	if view == nil || view.dialog == nil {
		return nil
	}
	result := &NativeTerminalTeamsAction{dialog: view.dialog, action: action, members: cloneTerminalCachedTeams(view.members)}
	if index >= 0 && index < len(view.members) {
		result.member = view.members[index].Clone()
		result.memberID = result.member.String("agentId")
	}
	return result
}

func (view *NativeTerminalTeamsSnapshot) Tasks(ctx context.Context, index int) ([]protocol.JSONObject, error) {
	if view == nil || view.dialog == nil {
		return nil, cooperative.ErrRestricted
	}
	if index < 0 || index >= len(view.members) {
		return []protocol.JSONObject{}, nil
	}
	return view.dialog.tasksForMember(ctx, view.members[index])
}
