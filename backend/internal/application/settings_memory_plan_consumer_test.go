package application

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestNativePersistedMemoryExcludesAndPlansReachActualSessionAndColdResume(t *testing.T) {
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		calls.Add(1)
		if bytes.Contains(data, []byte("EXCLUDED_PRIVATE_PROJECT_MEMORY")) || !bytes.Contains(data, []byte("MANAGED_MEMORY_MUST_BE_INCLUDED")) {
			t.Error("actual settings exclusion must skip project memory and preserve managed policy")
		}
		response(w, "memory-settings", "", nil, "PRIVATE_MEMORY_SETTINGS_DONE")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive = true
	o.Arguments.SystemPrompt = ""
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	settings := raw(map[string]any{"autoMemoryEnabled": false, "claudeMdExcludes": []string{filepath.Join(o.WorkDir, "CLAUDE.md"), filepath.Join(o.Native.ManagedRoot, "CLAUDE.md")}, "plansDirectory": "custom/plans", "spinnerVerbs": map[string]any{"mode": "replace", "verbs": []string{"私人验证"}}})
	for path, data := range map[string][]byte{
		filepath.Join(config, "settings.json"):           settings,
		filepath.Join(o.WorkDir, "CLAUDE.md"):            []byte("EXCLUDED_PRIVATE_PROJECT_MEMORY\n"),
		filepath.Join(o.Native.ManagedRoot, "CLAUDE.md"): []byte("MANAGED_MEMORY_MUST_BE_INCLUDED\n"),
	} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, cold := range []bool{false, true} {
		if cold {
			o.Arguments.Resume = o.Arguments.SessionID
		}
		s, err := NewSession(context.Background(), o)
		if err != nil {
			t.Fatal(err)
		}
		var path string
		if err = s.process.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			var err error
			path, err = s.process.Plans().Path(scope, "")
			return err
		}); err != nil {
			_ = s.Close(context.Background())
			t.Fatal(err)
		}
		if !strings.HasPrefix(path, filepath.Join(o.WorkDir, "custom", "plans")+string(filepath.Separator)) {
			_ = s.Close(context.Background())
			t.Fatal("settings plan directory did not reach actual plan producer", path)
		}
		if info, err := os.Stat(filepath.Dir(path)); err != nil || !info.IsDir() {
			_ = s.Close(context.Background())
			t.Fatal("actual plan producer did not create configured directory", err)
		}
		spinnerObserved := false
		if err = s.Submit(context.Background(), tui.Input{Text: "Complete private settings task", TurnID: "private-settings-turn"}, func(event tui.Event) error {
			if event.Kind == tui.Notice && event.Text == "私人验证…" {
				spinnerObserved = true
				state := tui.NewState(nil, 100, 24)
				state.ActiveTurn = event.TurnID
				state.Apply(event)
				if !strings.Contains(state.View("", false).Content, "私人验证…") {
					t.Error("actual TUI consumer lost configured spinner")
				}
			}
			return nil
		}); err != nil {
			_ = s.Close(context.Background())
			t.Fatal(err)
		}
		if !spinnerObserved {
			_ = s.Close(context.Background())
			t.Fatal("actual interactive session did not publish configured spinner")
		}
		if err = s.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("settings inspection added model calls", calls.Load())
	}
	retained, err := os.ReadFile(filepath.Join(config, "settings.json"))
	if err != nil || !bytes.Equal(retained, settings) {
		t.Fatal("startup/cold consumers rewrote user settings", err)
	}
}
