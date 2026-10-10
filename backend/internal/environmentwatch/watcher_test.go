package environmentwatch

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPhysicalWatcherInitialSuppressionDynamicPathsStableWritesAndCloseJoin(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "one.env")
	second := filepath.Join(root, "nested", "two.env")
	if err := os.WriteFile(first, []byte("initial"), 0600); err != nil {
		t.Fatal(err)
	}
	events := make(chan [2]string, 20)
	config := protocol.NewJSONObject(protocol.JSONField{Name: "FileChanged", Value: []byte(`[{"matcher":"one.env"}]`)})
	w, err := Start(Options{CWD: root, Snapshot: func() (protocol.JSONObject, error) { return config, nil }, Event: func(_ context.Context, path, event string) error { events <- [2]string{path, event}; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	select {
	case event := <-events:
		t.Fatal("Source ignoreInitial", event)
	case <-time.After(250 * time.Millisecond):
	}
	if err = w.Update([]string{second}); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(second), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(second, []byte("start"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(first, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	// Two writes inside the original stability threshold produce one settled event.
	time.Sleep(100 * time.Millisecond)
	if err = os.WriteFile(first, []byte("settled"), 0600); err != nil {
		t.Fatal(err)
	}
	seen := map[[2]string]bool{}
	deadline := time.After(5 * time.Second)
	for len(seen) < 2 {
		select {
		case event := <-events:
			seen[event] = true
		case <-deadline:
			t.Fatal("physical fsnotify watch did not deliver", seen)
		}
	}
	if !seen[[2]string{first, "change"}] || !seen[[2]string{second, "add"}] {
		t.Fatal(seen)
	}
	if err = os.Remove(second); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		if event != [2]string{second, "unlink"} {
			t.Fatal(event)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("unlink absent")
	}
	w.Close()
	w.Close()
}

func TestWatcherCloseCancelsAndJoinsInFlightHook(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "dynamic")
	entered := make(chan struct{})
	finished := make(chan struct{})
	w, err := Start(Options{CWD: root, Snapshot: func() (protocol.JSONObject, error) { return protocol.NewJSONObject(), nil }, Event: func(ctx context.Context, _, _ string) error {
		close(entered)
		<-ctx.Done()
		close(finished)
		return ctx.Err()
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Update([]string{path}); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		w.Close()
		t.Fatal("actual callback absent")
	}
	w.Close()
	select {
	case <-finished:
	default:
		t.Fatal("close did not join actual callback")
	}
}
