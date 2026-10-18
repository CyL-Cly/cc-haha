//go:build !windows

package application

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func newTerminalStatusLinePhysicalSession(t *testing.T, modelURL, scriptBody string, subscriber ...bool) (*Session, string) {
	t.Helper()
	o := nativeSessionOptions(t, modelURL)
	o.Interactive = true
	for _, environment := range []map[string]string{o.Environment, o.Native.Environment} {
		environment["NO_PROXY"] = "127.0.0.1,localhost"
	}
	home, config := o.Environment["HOME"], o.Environment["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(home, "actual-statusline")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nIFS= read -r input\nprintf '%s %s\\n' \"$$\" \"$input\" >> \"$HOME/statusline-records\"\n"+scriptBody), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "settings.json"), raw(map[string]any{"statusLine": map[string]any{"type": "command", "command": "'" + strings.ReplaceAll(script, "'", "'\\''") + "'", "padding": 2}}), 0600); err != nil {
		t.Fatal(err)
	}
	if len(subscriber) > 0 && subscriber[0] {
		for _, environment := range []map[string]string{o.Environment, o.Native.Environment} {
			delete(environment, "ANTHROPIC_API_KEY")
		}
		if err := os.WriteFile(filepath.Join(config, ".credentials.json"), raw(map[string]any{"claudeAiOauth": map[string]any{"accessToken": "private-statusline-subscriber", "scopes": []string{"user:inference", "user:profile"}, "expiresAt": 9999999999999, "subscriptionType": "max"}}), 0600); err != nil {
			t.Fatal(err)
		}
	}
	seedTerminalReleaseWarmFixture(t, o)
	global := map[string]any{}
	data, err := os.ReadFile(filepath.Join(config, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &global); err != nil {
		t.Fatal(err)
	}
	if len(subscriber) > 0 && subscriber[0] {
		global["oauthAccount"] = map[string]any{"billingType": "stripe_subscription"}
	}
	global["editorMode"] = "vim"
	global["projects"] = map[string]any{o.Native.CWD: map[string]any{"hasTrustDialogAccepted": true}}
	if err = os.WriteFile(filepath.Join(config, ".claude.json"), raw(global), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return s, home
}

func waitTerminalStatusLinePhysicalRecord(t *testing.T, home string, count int) []string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(filepath.Join(home, "statusline-records"))
		rows := strings.Split(strings.TrimSpace(string(data)), "\n")
		if len(data) > 0 && len(rows) >= count {
			return rows
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("actual statusline command did not consume its callback JSON")
	return nil
}

func TestMountedStatusLineHeldShellGlobalCloseDuringActualModelJoinsBoth(t *testing.T) {
	started, joined := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil || !strings.Contains(string(body), "STATUSLINE_HELD_MODEL") {
			t.Error("actual held model input", err, string(body))
		}
		close(started)
		<-r.Context().Done()
		close(joined)
	}))
	defer server.Close()
	s, home := newTerminalStatusLinePhysicalSession(t, server.URL, "/bin/sleep 60\nprintf 'FORBIDDEN_LATE_RESULT'\nprintf late > \"$HOME/forbidden-late\"\n")
	var mu sync.Mutex
	var callbacks []json.RawMessage
	initial, remove, err := s.SubscribeTerminalStatusLine(context.Background(), func(value json.RawMessage) {
		mu.Lock()
		callbacks = append(callbacks, append(json.RawMessage(nil), value...))
		mu.Unlock()
	})
	if err != nil || !strings.Contains(string(initial), `"configured":true`) {
		t.Fatal(string(initial), err)
	}
	defer remove()
	rows := waitTerminalStatusLinePhysicalRecord(t, home, 1)
	pid, err := strconv.Atoi(strings.SplitN(rows[0], " ", 2)[0])
	if err != nil {
		t.Fatal(err)
	}
	submitted := make(chan error, 1)
	var modelEvents []tui.Event
	go func() {
		submitted <- s.Submit(context.Background(), tui.Input{Text: "STATUSLINE_HELD_MODEL"}, func(event tui.Event) error {
			if event.Err != nil {
				t.Log("actual model event", event.Kind, event.Err)
			}
			modelEvents = append(modelEvents, event)
			return nil
		})
	}()
	select {
	case <-started:
	case err := <-submitted:
		t.Fatal("model failed before physical HTTP", err, string(raw(modelEvents)))
	case <-time.After(2 * time.Second):
		t.Fatal("held model did not start beside statusline child")
	}
	closed := make(chan error, 1)
	go func() { closed <- s.Close(context.Background()) }()
	select {
	case err := <-closed:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("component/native close blocked on held shell or actual model")
	}
	select {
	case <-submitted:
	case <-time.After(time.Second):
		t.Fatal("model turn not joined")
	}
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("physical model HTTP did not observe cancellation")
	}
	deadline := time.Now().Add(time.Second)
	for syscall.Kill(pid, 0) == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err = syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatal("actual statusline child retained after joined Close", pid, err)
	}
	if _, err = os.Stat(filepath.Join(home, "forbidden-late")); !os.IsNotExist(err) {
		t.Fatal("canceled old statusline executed a physical late effect", err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, value := range callbacks {
		if strings.Contains(string(value), "FORBIDDEN_LATE_RESULT") {
			t.Fatal("late statusline result re-published", string(value))
		}
	}
}

func TestMountedStatusLineUnmountJoinsAndVimDebouncesActualShell(t *testing.T) {
	s, home := newTerminalStatusLinePhysicalSession(t, "http://127.0.0.1:9", "printf 'ACTUAL_VISIBLE_STATUSLINE'\n")
	values := make(chan json.RawMessage, 8)
	_, remove, err := s.SubscribeTerminalStatusLine(context.Background(), func(value json.RawMessage) { values <- value })
	if err != nil {
		t.Fatal(err)
	}
	defer remove()
	waitTerminalStatusLinePhysicalRecord(t, home, 1)
	for _, mode := range []string{"NORMAL", "INSERT", "NORMAL"} {
		if err = s.UpdateTerminalStatusLine(context.Background(), mode); err != nil {
			t.Fatal(err)
		}
	}
	rows := waitTerminalStatusLinePhysicalRecord(t, home, 2)
	var input map[string]any
	if err = json.Unmarshal([]byte(strings.SplitN(rows[1], " ", 2)[1]), &input); err != nil {
		t.Fatal(err)
	}
	if mode := input["vim"].(map[string]any)["mode"]; mode != "NORMAL" {
		t.Fatal("debounced callback lost latest editor mode", input)
	}
	remove()
	if err = s.UpdateTerminalStatusLine(context.Background(), "INSERT"); err != nil {
		t.Fatal(err)
	}
	if rows = waitTerminalStatusLinePhysicalRecord(t, home, 2); len(rows) != 2 {
		t.Fatal("component unmount admitted an additional shell", rows)
	}
	s.process.BeginNativeTerminalStatusLineClose()
}
