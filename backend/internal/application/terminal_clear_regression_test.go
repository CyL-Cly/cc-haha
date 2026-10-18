package application

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func TestActualPublicSessionClearKeepsLocalCommandOutOfModel(t *testing.T) {
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		call := calls.Add(1)
		if call > 1 && (strings.Contains(string(body), "OLD_BEFORE_CLEAR_HUMAN") || !strings.Contains(string(body), "<command-name>/clear</command-name>") || !strings.Contains(string(body), "<local-command-stdout></local-command-stdout>")) {
			t.Error("Local clear retained old model input", string(body))
		}
		response(w, "clear-basic", "", nil, "PUBLIC_CLEAR_MODEL_RESPONSE")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.WorkDir = o.Native.CWD
	o.Native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrNotExist }
	o.Interactive = true
	o.ForegroundShellProgress = true
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	emit := func(tui.Event) error { return nil }
	if err = s.Submit(context.Background(), tui.Input{Text: "OLD_BEFORE_CLEAR_HUMAN", Mode: "prompt"}, emit); err != nil {
		t.Fatal(err)
	}
	oldSID := s.process.Controller.SessionID()
	if err = s.Submit(context.Background(), tui.Input{Text: "/clear", Mode: "prompt"}, emit); err != nil {
		t.Fatal("public /clear failed", err)
	}
	if calls.Load() != 1 || s.process.Controller.SessionID() == oldSID || len(s.process.Controller.History()) != 3 {
		t.Fatal("Local clear failed to reset actual current conversation", calls.Load(), oldSID, s.process.Controller.SessionID(), s.process.Controller.History())
	}
	if err = s.Submit(context.Background(), tui.Input{Text: "NEW_AFTER_CLEAR_HUMAN", Mode: "prompt"}, emit); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal(calls.Load())
	}
}
