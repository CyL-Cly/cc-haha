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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualSourceExportRendererLiveHeaderLabelsAndHistoryConsumer(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual pinned Source required")
	}
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		response(w, "actual-export-history", "", nil, "ACTUAL_EXPORT_ASSISTANT")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive = true
	home := o.Environment["HOME"]
	work := filepath.Join(home, "work")
	if err := os.MkdirAll(work, 0700); err != nil {
		t.Fatal(err)
	}
	// The original renderer requires a bounded Git project. Create that
	// launch fixture before either renderer samples directory emptiness.
	if err := os.MkdirAll(filepath.Join(work, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	var err error
	o.Native, err = clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: o.Environment, CWD: work, Platform: "darwin", ManagedRoot: filepath.Join(home, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	o.Native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrNotExist }
	o.WorkDir = o.Native.CWD
	if err := os.MkdirAll(o.Environment["CLAUDE_CONFIG_DIR"], 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], ".claude.json"), []byte(`{"oauthAccount":{"displayName":"fixture-reader"},"future":{"preserve":17}}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	if err := s.Submit(context.Background(), tui.Input{Text: "ACTUAL_EXPORT_HUMAN"}, nil); err != nil {
		t.Fatal(err)
	}
	value, err := s.process.TerminalConversation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	presentation, err := s.process.TerminalExportPresentation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-export-renderer.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + o.Environment["CLAUDE_CONFIG_DIR"], "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_EXPORT_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "USER_TYPE=external", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin", "TZ=UTC"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "cwd": o.WorkDir, "model": "fixture-model", "version": o.Version, "rows": []any{map[string]any{"messages": value.Messages, "columns": 80}}}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal("actual original React export renderer", err, string(output))
	}
	var source struct {
		Results      []string
		Presentation struct{ Version, CWD, BillingType, Model string }
		Caps         struct{ Blocked, SecureReads []string }
	}
	if err := json.Unmarshal(output, &source); err != nil || len(source.Results) != 1 || len(source.Caps.Blocked) != 0 {
		t.Fatal("actual Source renderer boundary", err, string(output))
	}
	if presentation.Version != source.Presentation.Version || presentation.WorkingDirectory != source.Presentation.CWD || presentation.BillingType != source.Presentation.BillingType || presentation.Model != source.Presentation.Model {
		t.Fatal("live export header labels diverged", presentation, source.Presentation)
	}
	text := tui.RenderConversationText(value.Messages, presentation)
	for _, label := range []string{source.Presentation.Version, source.Presentation.Model, source.Presentation.BillingType, "Welcome back fixture-reader!", "❯ ACTUAL_EXPORT_HUMAN", "⏺ ACTUAL_EXPORT_ASSISTANT"} {
		if !strings.Contains(text, label) || !strings.Contains(source.Results[0], label) {
			t.Fatal("actual Source/Go export omitted live identity or history", label, text, source.Results[0])
		}
	}
	var notice string
	if handled, err := s.InvokeTerminalCommand(context.Background(), "export", "source-header.txt", func(event tui.Event) error {
		if event.Kind == tui.Notice {
			notice = event.Text
		}
		return nil
	}); err != nil || !handled {
		t.Fatal("actual file export entry", handled, err)
	}
	file, err := os.ReadFile(filepath.Join(o.WorkDir, "source-header.txt"))
	if err != nil || string(file) != text || !strings.HasPrefix(notice, "Conversation exported to:") || calls.Load() != 1 {
		t.Fatal("live header did not reach actual export without inference", err, string(file), notice, calls.Load())
	}
}
