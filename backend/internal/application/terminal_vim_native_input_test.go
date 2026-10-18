package application

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestInteractiveNativeVimLocalInputUsesSharedHistoryAndLiveModeWithoutModel(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("local vim called a model") }))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive = true
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	var mode string
	if err = s.Submit(context.Background(), tui.Input{Text: "/vim"}, func(event tui.Event) error {
		if event.Kind == tui.EventKind("editor_mode_changed") {
			mode = event.Text
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	global, err := s.process.Global.Get()
	if err != nil || global.String("editorMode") != "vim" || mode != "vim" {
		t.Fatal("actual owned local command not published", err, global, mode)
	}
	var history strings.Builder
	snapshot, err := s.process.TerminalConversation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range snapshot.Messages {
		if message.String("type") == "user" {
			history.WriteString(obj(message.Get("message")).String("content"))
		} else {
			history.WriteString(message.String("content"))
		}
		history.WriteByte('\n')
	}
	if !strings.Contains(history.String(), "<command-name>/vim</command-name>") || !strings.Contains(history.String(), "Editor mode set to vim.") || !strings.Contains(history.String(), "local-command-caveat") {
		t.Fatal("shared native local history absent", history.String())
	}
}
