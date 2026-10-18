package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"
)

// This fixture preserves the real owner's Warm and permission snapshots. Only
// after Warm does it inject a Snapshot transport error; it never writes server
// runtime, conversation, admission or disconnect state.
type liveStatusSnapshotErrorOwner struct {
	*liveStatusPublicOwner
	snapshotMu  sync.Mutex
	armed       bool
	blocked     bool
	entered     chan struct{}
	release     chan struct{}
	enteredOnce sync.Once
	releaseOnce sync.Once
	err         error
}

func newLiveStatusSnapshotErrorOwner() *liveStatusSnapshotErrorOwner {
	return &liveStatusSnapshotErrorOwner{
		liveStatusPublicOwner: newLiveStatusPublicOwner(),
		entered:               make(chan struct{}), release: make(chan struct{}),
		err: errors.New("owned Snapshot transport failure must stay private"),
	}
}

func (owner *liveStatusSnapshotErrorOwner) arm(blocked bool) {
	owner.snapshotMu.Lock()
	owner.armed, owner.blocked = true, blocked
	owner.snapshotMu.Unlock()
}

func (owner *liveStatusSnapshotErrorOwner) unblock() {
	owner.releaseOnce.Do(func() { close(owner.release) })
}

func (owner *liveStatusSnapshotErrorOwner) Snapshot(ctx context.Context) ([]json.RawMessage, error) {
	frames, err := owner.liveStatusPublicOwner.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	owner.snapshotMu.Lock()
	armed, blocked := owner.armed, owner.blocked
	owner.snapshotMu.Unlock()
	if !armed {
		return frames, nil
	}
	owner.enteredOnce.Do(func() { close(owner.entered) })
	if blocked {
		select {
		case <-owner.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	// Returning the captured real permission frames alongside the error also
	// proves a retired snapshot cannot revive a stale approval.
	return frames, owner.err
}

func liveStatusSnapshotErrorPath(kind, id string) string {
	if kind == "collection" {
		return "/api/sessions/live-status"
	}
	return "/api/sessions/" + id + "/chat/status"
}

func liveStatusSnapshotErrorWantIdle(t *testing.T, kind string, raw []byte) {
	t.Helper()
	if kind == "collection" {
		var wire struct {
			Sessions []json.RawMessage `json:"sessions"`
		}
		if err := json.Unmarshal(raw, &wire); err != nil || wire.Sessions == nil || len(wire.Sessions) != 0 {
			t.Fatal("retired owner's late error/approval must produce an empty collection", err, string(raw))
		}
		return
	}
	var wire struct {
		State    string `json:"state"`
		Activity string `json:"activityState"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil || wire.State != "idle" || wire.Activity != "idle" {
		t.Fatal("retired owner's late error/approval must leave item status idle", err, string(raw))
	}
}

func TestLiveStatusPublicCurrentSnapshotErrorReturnsInternalError(t *testing.T) {
	for _, kind := range []string{"collection", "item"} {
		t.Run(kind, func(t *testing.T) {
			owner := newLiveStatusSnapshotErrorOwner()
			options := testOptions(t, func(_ context.Context, options SessionOptions, emit Emit) (LiveSession, error) {
				owner.attach(options, emit)
				return owner, nil
			})
			options.AfterRuntimeTimer = (&lifecycleClock{}).after
			_, host := testHost(t, options)
			id := createTestSession(t, host, options.WorkDir)
			_ = liveStatusPublicWarm(t, host, id, owner.liveStatusPublicOwner)
			owner.permission("current-owner-write", "permission_request")
			liveStatusPublicWant(t, host, map[string]string{id: "waiting"})
			liveStatusPublicChatState(t, host, id, "waiting")

			owner.arm(false)
			raw := requestJSON(t, host, http.MethodGet, liveStatusSnapshotErrorPath(kind, id), "", http.StatusInternalServerError)
			var wire struct {
				Error   string `json:"error"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(raw, &wire); err != nil || wire.Error != "INTERNAL_ERROR" || wire.Message != "Internal server error" {
				t.Fatal("current owner's Snapshot failure must be a generic HTTP 500", err, string(raw))
			}
		})
	}
}

type liveStatusSnapshotHTTPResult struct {
	code int
	data []byte
	err  error
}

func TestLiveStatusPublicRetiredSnapshotErrorDoesNotReviveApproval(t *testing.T) {
	for _, kind := range []string{"collection", "item"} {
		t.Run(kind, func(t *testing.T) {
			owner := newLiveStatusSnapshotErrorOwner()
			options := testOptions(t, func(_ context.Context, options SessionOptions, emit Emit) (LiveSession, error) {
				owner.attach(options, emit)
				return owner, nil // OnRuntimeClosed below must receive this exact wrapper.
			})
			options.AfterRuntimeTimer = (&lifecycleClock{}).after
			_, host := testHost(t, options)
			id := createTestSession(t, host, options.WorkDir)
			_ = liveStatusPublicWarm(t, host, id, owner.liveStatusPublicOwner)
			owner.permission("retired-owner-write", "permission_request")
			liveStatusPublicWant(t, host, map[string]string{id: "waiting"})
			liveStatusPublicChatState(t, host, id, "waiting")

			owner.arm(true)
			path := liveStatusSnapshotErrorPath(kind, id)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			result := make(chan liveStatusSnapshotHTTPResult, 1)
			done := make(chan struct{})
			// Cleanup runs before the socket/host cleanups. Even a failed assertion
			// releases the Snapshot and joins the actual HTTP goroutine first.
			t.Cleanup(func() {
				owner.unblock()
				cancel()
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					t.Error("owned HTTP request did not join after fixture cleanup")
				}
			})
			go func() {
				defer close(done)
				request, err := http.NewRequestWithContext(ctx, http.MethodGet, host.URL+path, nil)
				if err != nil {
					result <- liveStatusSnapshotHTTPResult{err: err}
					return
				}
				response, err := host.Client().Do(request)
				if err != nil {
					result <- liveStatusSnapshotHTTPResult{err: err}
					return
				}
				defer response.Body.Close()
				data, err := io.ReadAll(response.Body)
				result <- liveStatusSnapshotHTTPResult{code: response.StatusCode, data: data, err: err}
			}()

			liveStatusPublicAwait(t, owner.entered, "public HTTP did not enter the actual owner's Snapshot")
			owner.mu.Lock()
			onClosed := owner.onClosed
			owner.mu.Unlock()
			if onClosed == nil {
				t.Fatal("actual owner retirement callback is unavailable")
			}
			if err := onClosed(ctx, owner); err != nil {
				t.Fatal("exact wrapper retirement failed", err)
			}
			liveStatusPublicAwait(t, owner.closed, "exact owner Close did not join before Snapshot release")
			owner.unblock()
			select {
			case reply := <-result:
				if reply.err != nil || reply.code != http.StatusOK {
					t.Fatal("retired owner's late Snapshot error must be ignored", reply.err, reply.code, string(reply.data))
				}
				liveStatusSnapshotErrorWantIdle(t, kind, reply.data)
			case <-ctx.Done():
				t.Fatal("public HTTP did not settle after exact owner retirement", ctx.Err())
			}
			liveStatusPublicAwait(t, done, "public HTTP body/response did not close")
			// The old owner still holds its real approval snapshot. A fresh query
			// must also ignore it rather than restoring waiting after the reply.
			liveStatusSnapshotErrorWantIdle(t, kind, requestJSON(t, host, http.MethodGet, path, "", http.StatusOK))
		})
	}
}
