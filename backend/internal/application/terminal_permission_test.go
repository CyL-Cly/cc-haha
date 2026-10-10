package application

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestTerminalPermissionModesSubscribeActualHostAndNeverRunModel(t *testing.T) {
	var count atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count.Add(1); http.Error(w, "mode must not infer", 500) }))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Arguments.PermissionMode = "default"
	s, e := NewSession(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	observed := []string{}
	initial, stop := s.SubscribePermissionMode(func(mode string) { observed = append(observed, mode); _, off := s.SubscribePermissionMode(nil); off() })
	if initial != "default" {
		t.Fatal("initial mode not actual Host", initial)
	}
	for _, want := range []string{"acceptEdits", "plan", "default"} {
		var emitted string
		if e = s.InvokeAction(context.Background(), "chat:cycleMode", func(event tui.Event) error {
			if event.Kind == tui.PermissionModeChanged {
				emitted = event.PermissionMode
			}
			return nil
		}); e != nil {
			t.Fatal(e)
		}
		actual, e := s.process.SessionPermissionMode(context.Background())
		if e != nil || actual != want || emitted != want || observed[len(observed)-1] != want {
			t.Fatal("observer diverged from only real permission state", want, actual, emitted, observed, e)
		}
	}
	if count.Load() != 0 {
		t.Fatal("cycle ran provider", count.Load())
	}
	stop()
	stop()
	if e = s.InvokeAction(context.Background(), "chat:cycleMode", nil); e != nil {
		t.Fatal(e)
	}
	if len(observed) != 3 {
		t.Fatal("unsubscribed listener ran", observed)
	}
}
