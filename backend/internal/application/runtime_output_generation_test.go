package application

import (
	"encoding/json"
	"testing"
)

func TestDesktopReplacementEndsLostIdleAndFencesOldHostOutput(t *testing.T) {
	frames := []json.RawMessage{}
	sdk := []json.RawMessage{}
	d := &DesktopSession{projection: NewProjection(), emit: func(data json.RawMessage) { frames = append(frames, append(json.RawMessage(nil), data...)) }, onSDK: func(data json.RawMessage) { sdk = append(sdk, append(json.RawMessage(nil), data...)) }}
	old := d.nextRuntimeOutput()
	if err := old(json.RawMessage(`{"type":"system","subtype":"session_state_changed","state":"running"}`)); err != nil {
		t.Fatal(err)
	}
	if !d.cliRunning {
		t.Fatal("original CLI marker absent")
	}
	current := d.nextRuntimeOutput()
	if d.cliRunning || obj(sdk[len(sdk)-1]).String("state") != "idle" {
		t.Fatal("replacement did not emit lost idle", sdk)
	}
	if err := current(json.RawMessage(`{"type":"system","subtype":"session_state_changed","state":"running"}`)); err != nil {
		t.Fatal(err)
	}
	n := len(frames)
	m := len(sdk)
	old(json.RawMessage(`{"type":"system","subtype":"session_state_changed","state":"idle"}`))
	old(json.RawMessage(`{"type":"system","subtype":"task_progress","task_id":"retired"}`))
	if !d.cliRunning || len(frames) != n || len(sdk) != m {
		t.Fatal("retired Host output overwrote replacement state")
	}
}
