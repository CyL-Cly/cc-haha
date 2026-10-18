package tui

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type cachedTeamsFrameTestValue struct{ teams []protocol.JSONObject }

func (v *cachedTeamsFrameTestValue) InitialTeams() []protocol.JSONObject {
	return cloneCachedTeams(v.teams)
}

type capturedTeamsRequestTestSession struct {
	*testSession
	request TerminalTeamsRequest
}

func (s *capturedTeamsRequestTestSession) OpenTerminalTeams(_ context.Context, request TerminalTeamsRequest, _ Emit) error {
	s.request = request
	return nil
}
func TestTerminalCachedTeamsFrameAtomicLatestCaptureAndCancelledWaiter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	queue := make(chan terminalCachedTeamsFrameChangedMsg, 1)
	first := &cachedTeamsFrameTestValue{[]protocol.JSONObject{cachedTeam("old", 1)}}
	last := &cachedTeamsFrameTestValue{[]protocol.JSONObject{cachedTeam("owned", 3)}}
	enqueueTerminalCachedTeamsFrame(ctx, queue, first)
	enqueueTerminalCachedTeamsFrame(ctx, queue, last)
	last.teams[0].Set("name", []byte(`"later-file-state"`))
	session := &capturedTeamsRequestTestSession{testSession: &testSession{}}
	m := &terminalModel{state: NewState(nil, 100, 35), session: session, ctx: ctx, cachedTeamsFrames: queue}
	m.Update(m.terminalCachedTeamsFrameCommand()())
	if m.state.CachedTeamsFrame != last || m.state.CachedTeams[0].String("name") != "owned" {
		t.Fatal("Frame and teams did not commit from the same detached latest publication")
	}
	if msg, ok := m.terminalTeamsCommand()().(actionReturnedMsg); !ok || msg.err != nil {
		t.Fatal(msg)
	}
	if session.request.InitialFrame != last || session.request.InitialTeams[0].String("name") != "owned" {
		t.Fatal("Actual mounted request did not retain the visible footer frame", session.request)
	}
	encoded, err := json.Marshal(session.request)
	if err != nil || string(encoded) == "" {
		t.Fatal(err)
	}
	var decoded map[string]json.RawMessage
	_ = json.Unmarshal(encoded, &decoded)
	if decoded["InitialFrame"] != nil {
		t.Fatal("Private carrier leaked into public JSON", string(encoded))
	}
	cancel()
	if msg := m.terminalCachedTeamsFrameCommand()(); msg != nil {
		t.Fatal("Cancelled background waiter produced an unowned message", msg)
	}
	m.turns.Wait()
}
