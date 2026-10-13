package server

import (
	"context"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/teamworker"
)

func TestSelfRetirementRequiresCurrentRuntimeAndFactoryLiveIdentityAndRevokesOnlyThatGeneration(t *testing.T) {
	s, _ := testHost(t, testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil }))
	first, replacement := &transportSession{}, &transportSession{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	old := &runtimeSession{ctx: ctx, cancel: cancel, live: first, options: SessionOptions{TeamWorker: &teamworker.Start{ParentSessionID: "parent"}}}
	newCtx, newCancel := context.WithCancel(context.Background())
	defer newCancel()
	next := &runtimeSession{ctx: newCtx, cancel: newCancel, live: replacement, options: SessionOptions{TeamWorker: &teamworker.Start{ParentSessionID: "parent"}}}
	s.mu.Lock()
	s.runtimes["same-id"] = next
	s.mu.Unlock()
	if err := s.retireSelfClosedRuntime(context.Background(), "same-id", old, first); err != nil {
		t.Fatal(err)
	}
	if replacement.closed.Load() != 0 || !s.TeamWorkerConnected("same-id") {
		t.Fatal("old runtime callback retired a replacement")
	}
	if err := s.retireSelfClosedRuntime(context.Background(), "same-id", next, first); err != nil {
		t.Fatal(err)
	}
	if replacement.closed.Load() != 0 || !s.TeamWorkerConnected("same-id") {
		t.Fatal("old live callback retired the current runtime's different factory generation")
	}
	revoked := 0
	next.mu.Lock()
	next.revokeCollaboration = func() { revoked++ }
	next.mu.Unlock()
	if err := s.retireSelfClosedRuntime(context.Background(), "same-id", next, replacement); err != nil {
		t.Fatal(err)
	}
	if replacement.closed.Load() != 1 || revoked != 1 || s.TeamWorkerConnected("same-id") || newCtx.Err() == nil {
		t.Fatal("actual retirement did not close/revoke/remove/cancel the same generation", replacement.closed.Load(), revoked)
	}
	s.mu.Lock()
	remaining := s.runtimes["same-id"]
	s.mu.Unlock()
	if remaining != nil {
		t.Fatal("successfully joined self retirement retained the registry slot")
	}
	if err := s.retireSelfClosedRuntime(context.Background(), "same-id", next, replacement); err != nil || replacement.closed.Load() != 1 || revoked != 1 {
		t.Fatal("repeated self retirement repeated actual close or revocation", err)
	}
}
