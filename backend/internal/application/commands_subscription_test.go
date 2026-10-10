package application

import (
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestSessionCommandSubscriptionsOwnSnapshotsAndUnsubscribeWithoutLockingCallbacks(t *testing.T) {
	s := &Session{}
	if err := s.commandsChanged([]sdkhost.ControlCommand{{Name: "before", Description: "old", Kind: "prompt", ArgumentHint: "<target>", RequiresArguments: true, ArgumentNames: []string{"target"}}}); err != nil {
		t.Fatal(err)
	}
	var calls int
	var observed []tui.Completion
	initial, stop := s.SubscribeCommands(func(commands []tui.Completion) {
		calls++
		observed = append([]tui.Completion{}, commands...)
		commands[0].Name = "subscriber mutation"
		_, unsubscribe := s.SubscribeCommands(nil)
		unsubscribe()
	})
	if len(initial) != 1 || initial[0].Name != "before" {
		t.Fatal(initial)
	}
	initial[0].Name = "initial mutation"
	if initial[0].Kind != "prompt" || initial[0].ArgumentHint != "<target>" || !initial[0].RequiresArguments || len(initial[0].ArgumentNames) != 1 {
		t.Fatal("actual command metadata lost", initial)
	}
	initial[0].ArgumentNames[0] = "subscriber mutation"
	fresh, _ := s.SubscribeCommands(nil)
	if fresh[0].ArgumentNames[0] != "target" {
		t.Fatal("snapshot mutated real argument names", fresh)
	}
	if err := s.commandsChanged([]sdkhost.ControlCommand{{Name: "after", Description: "new"}}); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := s.SubscribeCommands(nil)
	if calls != 1 || observed[0].Name != "after" || snapshot[0].Name != "after" {
		t.Fatal(calls, observed, snapshot)
	}
	stop()
	stop()
	if err := s.commandsChanged([]sdkhost.ControlCommand{{Name: "final"}}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("unsubscribed listener was called", calls)
	}
	var work sync.WaitGroup
	for i := 0; i < 32; i++ {
		work.Add(1)
		go func() { defer work.Done(); _, stop := s.SubscribeCommands(func([]tui.Completion) {}); stop() }()
	}
	work.Wait()
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	if err := s.commandsChanged([]sdkhost.ControlCommand{{Name: "after closed"}}); err != nil {
		t.Fatal(err)
	}
	snapshot, _ = s.SubscribeCommands(nil)
	if snapshot[0].Name != "final" {
		t.Fatal("closed runtime accepted notification", snapshot)
	}
}
