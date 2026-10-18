package application

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTerminalFastPanelPersistsPreferenceChangesRealNextRequestAndKeepsOffModel(t *testing.T) {
	var count atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		n := count.Add(1)
		body := obj(data)
		if body.String("model") != "claude-opus-4-6" {
			t.Error("picker did not switch actual next model", body.String("model"))
		}
		if (body.String("speed") == "fast") != (n == 1) {
			t.Error("picker did not switch actual next fast producer", body.String("speed"), n)
		}
		response(w, "fast-panel-next", "", nil, "FAST_PANEL_NEXT")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive = true
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		delete(env, "CLAUDE_CODE_DISABLE_FAST_MODE")
		env["CLAUDE_CODE_SIMPLE"] = "1"
		env["ANTHROPIC_DEFAULT_OPUS_MODEL"] = "claude-opus-4-6"
	}
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	_ = os.MkdirAll(config, 0700)
	_ = os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"future":{"keep":true}}`), 0600)
	_ = os.WriteFile(filepath.Join(config, ".claude.json"), []byte(`{"penguinModeOrgEnabled":true}`), 0600)
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	var panel *tui.LocalPanel
	emit := func(event tui.Event) error {
		if event.LocalPanel != nil {
			copy := *event.LocalPanel
			panel = &copy
		}
		return nil
	}
	if err = s.InvokeAction(context.Background(), "chat:fastMode", emit); err != nil || panel == nil {
		t.Fatal(err, panel)
	}
	for _, text := range []string{"enabled", "disabled"} {
		if err = s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Action: "toggle", Row: "fast"}, emit); err != nil || panel.Notice != "" {
			t.Fatal(err, panel.Notice)
		}
		if err = s.Submit(context.Background(), tui.Input{Text: "Use actual " + text + " fast request"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(config, "settings.json"))
	if err != nil || strings.Contains(string(data), "fastMode") || !strings.Contains(string(data), "future") {
		t.Fatal(err, string(data))
	}
	if count.Load() != 2 {
		t.Fatal(count.Load())
	}
}
