package application

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/server"
)

func TestActualSourceDesktopCompletionDoesNotPublishReconnectSnapshot(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		response(w, "ordinary-completion", "", nil, "ordinary completed reply")
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	var mu sync.Mutex
	var frames []json.RawMessage
	boundarySnapshot := make(chan []json.RawMessage, 1)
	var live server.LiveSession
	var err error
	live, err = DesktopFactory(o)(context.Background(), server.SessionOptions{ID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", WorkDir: o.WorkDir}, func(data json.RawMessage) {
		if obj(data).String("type") == "message_complete" {
			snapshot, snapshotErr := live.Snapshot(context.Background())
			if snapshotErr != nil {
				t.Error(snapshotErr)
			}
			boundarySnapshot <- snapshot
		}
		mu.Lock()
		frames = append(frames, append(json.RawMessage(nil), data...))
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- live.Handle(ctx, raw(map[string]any{"type": "user_message", "content": "ordinary completed turn"}))
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("actual foreground request did not start")
	}
	running, err := live.Snapshot(ctx)
	if err != nil || len(running) == 0 || obj(running[0]).String("turnState") != "running" {
		t.Fatal("registered foreground turn missing", running, err)
	}
	close(release)
	select {
	case snapshot := <-boundarySnapshot:
		if len(snapshot) == 0 || obj(snapshot[0]).String("turnState") != "idle" {
			t.Fatal("result frame raced foreground settlement", snapshot)
		}
	case <-ctx.Done():
		t.Fatal("actual completion boundary not reached")
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	completed := false
	for _, frame := range frames {
		v := obj(frame)
		completed = completed || v.String("type") == "message_complete"
		if v.String("type") == "session_state" {
			t.Error("normal completion published a reconnect-only snapshot after permission replay", string(frame))
		}
	}
	if !completed {
		t.Fatal("actual normal CLI result was not projected")
	}
	snapshot, err := live.Snapshot(context.Background())
	if err != nil || len(snapshot) == 0 || obj(snapshot[0]).String("type") != "session_state" || obj(snapshot[0]).String("turnState") != "idle" {
		t.Fatal("explicit reconnect snapshot was lost", snapshot, err)
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual Source Bun required")
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-desktop-completion-frames.fixture.ts")
	home := t.TempDir()
	command := exec.Command(bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_COMPLETION_FRAMES_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "USER_TYPE=external", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	command.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "sessionId": "private-source-completion", "pending": []any{map[string]any{"requestId": "private-pending-write", "toolName": "Write", "toolUseId": "private-write", "input": map[string]string{"file_path": filepath.Join(home, "alpha.json")}, "displayName": "alpha"}}, "result": map[string]any{"type": "result", "subtype": "success", "is_error": false, "usage": map[string]int{"input_tokens": 1, "output_tokens": 1}}}))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	var source struct {
		Direct, Broadcast, RequestedSnapshot []json.RawMessage
		RegisteredSnapshot, SettledSnapshot  []json.RawMessage
		Capabilities                         struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Capabilities.Blocked) != 0 {
		t.Fatal("invalid actual Source completion evidence", err, string(output))
	}
	for _, frames := range [][]json.RawMessage{source.Direct, source.Broadcast} {
		if len(frames) != 2 || obj(frames[0]).String("type") != "message_complete" || obj(frames[1]).String("requestId") != "private-pending-write" {
			t.Fatal("Source original result and pending permission forwarding changed", string(raw(frames)))
		}
	}
	if len(source.RequestedSnapshot) != 1 || obj(source.RequestedSnapshot[0]).String("type") != "session_state" {
		t.Fatal("Source sync_state snapshot producer changed", string(raw(source.RequestedSnapshot)))
	}
	if len(source.RegisteredSnapshot) != 1 || obj(source.RegisteredSnapshot[0]).String("turnState") != "running" || len(source.SettledSnapshot) != 1 || obj(source.SettledSnapshot[0]).String("turnState") != "idle" {
		t.Fatal("actual Source registered foreground boundary changed", string(raw(source)))
	}
}
