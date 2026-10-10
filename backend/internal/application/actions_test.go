package application

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestCostActionUsesOriginalLocalCommandAndUnknownActionNeverCallsModel(t *testing.T) {
	for _, auth := range []string{"api-key-without-user-type", "bearer-without-user-type"} {
		t.Run(auth, func(t *testing.T) {
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if auth == "bearer-without-user-type" && (r.Header.Get("Authorization") != "Bearer fake-loopback-bearer" || r.Header.Get("X-Api-Key") != "") {
					t.Error("direct Bearer auth was not used")
				}
				response(w, "cost-initial", "", nil, "FIRST ACTUAL TURN")
			}))
			defer provider.Close()
			o := nativeSessionOptions(t, provider.URL)
			for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
				delete(env, "USER_TYPE")
				if auth == "bearer-without-user-type" {
					delete(env, "ANTHROPIC_API_KEY")
					env["ANTHROPIC_AUTH_TOKEN"] = "fake-loopback-bearer"
				}
			}
			s, err := NewSession(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			if err = s.Submit(context.Background(), tui.Input{Text: "First actual turn."}, nil); err != nil {
				t.Fatal(err)
			}
			var text strings.Builder
			if err = s.InvokeAction(context.Background(), "command:cost", func(event tui.Event) error {
				if event.Kind == tui.TextDelta {
					text.WriteString(event.Text)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(text.String(), "Total cost:") || calls.Load() != 1 {
				t.Fatalf("original local output lost or sent to model: %q calls=%d", text.String(), calls.Load())
			}
			for _, action := range []string{"command:unknown", "chat:unportedAction"} {
				var unsupported *tui.UnsupportedAction
				err = s.InvokeAction(context.Background(), action, nil)
				if !errors.As(err, &unsupported) || unsupported.Action != action || calls.Load() != 1 {
					t.Fatalf("unported host action silently became prompt: %s %v calls=%d", action, err, calls.Load())
				}
			}
		})
	}
}
