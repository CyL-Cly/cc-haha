package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
	"golang.org/x/net/websocket"
)

type pendingRepositoryPrewarmAttempt struct {
	options  SessionOptions
	prepared RepositorySessionLaunch
	err      error
}

// MAIN skips prewarm for pending repository launches. The eventual transport is
// a fixture, but HTTP/WS, persisted placeholder and checkout callbacks are real.
func TestPendingRepositoryPrewarmPreservesPlaceholderUntilRealUserTurn(t *testing.T) {
	initial, options := repositoryLaunchFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	initialError := initial.Close(ctx)
	cancel()
	if initialError != nil {
		t.Fatal(initialError)
	}
	var factoryCalls atomic.Int32
	started := make(chan struct{}, 2)
	attempts := make(chan pendingRepositoryPrewarmAttempt, 2)
	live := &transportSession{received: make(chan json.RawMessage, 4)}
	options.Factory = func(ctx context.Context, selected SessionOptions, _ Emit) (LiveSession, error) {
		factoryCalls.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		attempt := pendingRepositoryPrewarmAttempt{options: selected}
		attempt.options.Metadata = append(json.RawMessage(nil), selected.Metadata...)
		if selected.PrepareRepositoryLaunch == nil || selected.FinalizeRepositoryLaunch == nil {
			attempt.err = errors.New("actual repository launch callbacks are missing")
		} else {
			attempt.prepared, attempt.err = selected.PrepareRepositoryLaunch(ctx)
			if attempt.err == nil {
				attempt.err = selected.FinalizeRepositoryLaunch(ctx, attempt.prepared.WorkDir, attempt.prepared.Repository)
			}
		}
		select {
		case attempts <- attempt:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if attempt.err != nil {
			return nil, attempt.err
		}
		return live, nil
	}
	git := func(args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		result, err := worktree.NativeRun(ctx, worktree.ProcessRequest{Command: "/usr/bin/git", Arguments: args, CWD: options.WorkDir, Environment: options.Environment()})
		if err != nil || result.Code != 0 {
			t.Fatalf("private Git %v: result=%+v error=%v", args, result, err)
		}
		return result.Stdout
	}
	server, host := testHost(t, options)
	var socket *websocket.Conn
	closed := false
	t.Cleanup(func() {
		if closed {
			return
		}
		if socket != nil {
			_ = socket.Close()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Close(ctx); err != nil {
			t.Error("private prewarm server cleanup did not join", err)
		}
		host.Close()
	})
	body, _ := json.Marshal(map[string]any{"workDir": options.WorkDir, "permissionMode": "plan", "repository": map[string]any{"branch": "feature", "worktree": false}})
	var created struct{ SessionID string }
	if err := json.Unmarshal(requestJSON(t, host, "POST", "/api/sessions", string(body), 201), &created); err != nil || created.SessionID == "" {
		t.Fatalf("owned repository placeholder was not created: %+v error=%v", created, err)
	}
	placeholder := filepath.Join(options.ConfigDir, "projects", projectpath.Sanitize(options.WorkDir), created.SessionID+".jsonl")
	beforePlaceholder, err := os.ReadFile(placeholder)
	if err != nil {
		t.Fatal(err)
	}
	beforeHead := git("rev-parse", "HEAD")
	beforeIndex := git("ls-files", "--stage", "-z")
	tracked := filepath.Join(options.WorkDir, "tracked.txt")
	beforeTracked, err := os.ReadFile(tracked)
	if err != nil || strings.TrimSpace(git("branch", "--show-current")) != "main" {
		t.Fatal("private repository fixture is not on main", err)
	}
	socket = dial(t, host, created.SessionID)
	receiveType(t, socket, "permission_requests_snapshot")
	if err = websocket.Message.Send(socket, `{"type":"prewarm_session"}`); err != nil {
		t.Fatal(err)
	}
	// pong fences socket ingress only. The following 150ms is an explicitly
	// finite absence observation, not a permanent no-start or queue-drain claim.
	if err = websocket.Message.Send(socket, `{"type":"ping"}`); err != nil {
		t.Fatal(err)
	}
	receiveType(t, socket, "pong")
	observation := time.NewTimer(150 * time.Millisecond)
	select {
	case <-started:
		t.Error("pending repository prewarm entered the session factory before a user turn")
		<-observation.C
	case <-observation.C:
	}
	observation.Stop()
	if count := factoryCalls.Load(); count != 0 {
		t.Errorf("repository prewarm factory calls=%d want0 within the observed window", count)
	}
	afterPlaceholder, err := os.ReadFile(placeholder)
	if err != nil || !bytes.Equal(beforePlaceholder, afterPlaceholder) {
		t.Error("repository prewarm replaced the original pending placeholder", err)
	}
	if strings.TrimSpace(git("branch", "--show-current")) != "main" || git("rev-parse", "HEAD") != beforeHead || git("ls-files", "--stage", "-z") != beforeIndex {
		t.Error("repository prewarm materialized checkout or changed HEAD/index before user input")
	}
	afterTracked, err := os.ReadFile(tracked)
	if err != nil || !bytes.Equal(beforeTracked, afterTracked) {
		t.Error("repository prewarm changed owned tracked bytes", err)
	}
	// A real user frame proves this same socket and queue can enter the actual
	// callbacks. Ordered delivery also exposes a late prewarm sent to transport.
	userFrame := json.RawMessage(`{"type":"user_message","content":"owned repository first user turn"}`)
	if err = websocket.Message.Send(socket, string(userFrame)); err != nil {
		t.Fatal(err)
	}
	var attempt pendingRepositoryPrewarmAttempt
	select {
	case attempt = <-attempts:
	case <-time.After(5 * time.Second):
		t.Fatal("real user turn did not reach the actual repository callback consumer")
	}
	if attempt.err != nil {
		t.Fatal("actual repository checkout/finalize failed", attempt.err)
	}
	var selection struct {
		Repository struct{ Branch string }
	}
	if err = json.Unmarshal(attempt.options.Metadata, &selection); err != nil || selection.Repository.Branch != "feature" || attempt.options.PermissionMode != "plan" || attempt.options.WorkDir != options.WorkDir || attempt.options.Resume || attempt.prepared.WorkDir != options.WorkDir {
		t.Fatalf("first real user turn lost literal pending repository selection: options=%+v prepared=%+v error=%v", attempt.options, attempt.prepared, err)
	}
	delivery := time.NewTimer(5 * time.Second)
	defer delivery.Stop()
	userDelivered := false
	for !userDelivered {
		select {
		case raw := <-live.received:
			if bytes.Equal(raw, userFrame) {
				userDelivered = true
			} else {
				t.Errorf("fixture transport received input before the real user turn: %s", raw)
			}
		case <-delivery.C:
			t.Fatal("real user frame was not delivered to the fixture transport")
		}
	}
	if count := factoryCalls.Load(); count != 1 {
		t.Errorf("one real user turn factory calls=%d want1", count)
	}
	if strings.TrimSpace(git("branch", "--show-current")) != "feature" || git("rev-parse", "HEAD") != beforeHead || git("ls-files", "--stage", "-z") != beforeIndex {
		t.Error("real user turn did not perform the feature checkout while preserving HEAD/index")
	}
	afterTracked, err = os.ReadFile(tracked)
	if err != nil || !bytes.Equal(beforeTracked, afterTracked) {
		t.Error("real user turn changed owned tracked bytes", err)
	}
	_ = socket.Close()
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	closeError := server.Close(ctx)
	cancel()
	host.Close()
	closed = closeError == nil
	if closeError != nil || live.closed.Load() != 1 {
		t.Errorf("private repository runtime did not join exactly once: error=%v closeCount=%d", closeError, live.closed.Load())
	}
}
