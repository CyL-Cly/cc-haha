package application

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// The concrete carrier is created only when this actual mount publishes a
// projection. Tea returns it with the row from that rendered frame. It cannot
// be supplied by SDK JSON, an arbitrary callback or another mount's dialog.
type terminalTeamsFrame struct {
	id, level string
	view      *clibootstrap.NativeTerminalTeamsSnapshot
	rows      map[string]int
	members   []protocol.JSONObject
}

func (frame *terminalTeamsFrame) Projection() protocol.JSONObject {
	return frame.view.Projection()
}

func (s *Session) captureTerminalTeamsFrame(state *terminalLocalPanelState) *terminalTeamsFrame {
	value := state.teamsPanel
	if value.snapshot == nil {
		return nil
	}
	frame := &terminalTeamsFrame{id: state.props.ID, level: value.level, view: value.snapshot, rows: map[string]int{}, members: make([]protocol.JSONObject, len(value.members))}
	for i, member := range value.members {
		frame.members[i] = member.Clone()
	}
	for row, index := range value.memberIndices {
		frame.rows[row] = index
	}
	return frame
}
