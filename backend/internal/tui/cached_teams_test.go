package tui

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func cachedTeam(name string, count int) protocol.JSONObject {
	value := protocol.NewJSONObject()
	n, _ := json.Marshal(name)
	value.Set("name", n)
	n, _ = json.Marshal(count)
	value.Set("memberCount", n)
	return value
}

func TestTerminalCachedTeamsLatestDetachedUpdatesAndCancelledBackgroundWaiter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	queue := make(chan []protocol.JSONObject, 1)
	first := cachedTeam("lead-only", 0)
	enqueueTerminalCachedTeams(ctx, queue, []protocol.JSONObject{first})
	first.Set("name", json.RawMessage(`"mutated producer"`))
	second := cachedTeam("real roster", 3)
	enqueueTerminalCachedTeams(ctx, queue, []protocol.JSONObject{second})
	second.Set("name", json.RawMessage(`"mutated producer"`))
	m := &terminalModel{ctx: ctx, state: NewState(nil, 100, 35), cachedTeamsChanges: queue}
	message := m.terminalCachedTeamsCommand()()
	changed, ok := message.(terminalCachedTeamsChangedMsg)
	if !ok || len(changed.teams) != 1 || changed.teams[0].String("name") != "real roster" {
		t.Fatal("Pre-run producer update was blocked, stale, or shared", message)
	}
	m.Update(changed)
	changed.teams[0].Set("name", json.RawMessage(`"mutated callback"`))
	if m.state.CachedTeams[0].String("name") != "real roster" || m.activeTurns != 0 {
		t.Fatal("Background roster update altered foreground lifetime or shared mutable rows")
	}
	waiter := m.terminalCachedTeamsCommand()
	done := make(chan struct{})
	go func() { defer close(done); waiter() }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Global cancellation did not join background roster waiter")
	}
	m.turns.Wait()
	enqueueTerminalCachedTeams(ctx, queue, []protocol.JSONObject{cachedTeam("late", 5)})
	if len(queue) != 0 {
		t.Fatal("Closed terminal retained late roster update")
	}
}
