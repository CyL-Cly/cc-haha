package application

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestDoctorActualApplicationSlashGateAndFollowingSharedASK(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "enabled", true: "disabled"}[disabled], func(t *testing.T) {
			releases := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`[]`)) }))
			defer releases.Close()
			var requests atomic.Int32
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				response(w, "after-doctor", "", nil, "AFTER_DOCTOR_SHARED_SID")
			}))
			defer model.Close()
			options := nativeSessionOptions(t, model.URL)
			options.Interactive = true
			options.Native.Environment["CC_HAHA_CLI_RELEASE_API"] = releases.URL
			if disabled {
				options.Native.Environment["DISABLE_DOCTOR_COMMAND"] = "true"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			session, e := NewSession(ctx, options)
			if e != nil {
				t.Fatal(e)
			}
			defer session.Close(context.Background())
			before := session.process.Controller.SessionID()
			names, _ := session.SubscribeCommands(nil)
			found := false
			for _, command := range names {
				if command.Name == "doctor" {
					found = true
				}
			}
			if found == disabled {
				t.Fatal("Doctor Source gate missing from terminal completion", names, disabled)
			}
			panel := ""
			e = session.Submit(ctx, tui.Input{Text: "/doctor", Mode: "prompt", TurnID: "local-doctor"}, func(event tui.Event) error {
				if event.Kind == tui.DoctorOpened {
					panel = event.Text
				}
				return nil
			})
			if disabled {
				if e == nil || !strings.Contains(e.Error(), "disabled native /doctor") {
					t.Fatal("disabled local command admitted", e, panel)
				}
			} else {
				if e != nil || !strings.Contains(panel, "Diagnostics") || !strings.Contains(panel, "Context diagnostics") {
					t.Fatal("actual slash did not render native Doctor", e, panel)
				}
			}
			if requests.Load() != 0 || session.process.Controller.SessionID() != before {
				t.Fatal("Doctor started ASK or changed SID", requests.Load(), before, session.process.Controller.SessionID())
			}
			if e = session.Submit(ctx, tui.Input{Text: "ordinary task after local Doctor", Mode: "prompt", TurnID: "after-doctor"}, func(tui.Event) error { return nil }); e != nil {
				t.Fatal(e)
			}
			if requests.Load() != 1 || session.process.Controller.SessionID() != before {
				t.Fatal("following ASK did not share original harness", requests.Load(), before, session.process.Controller.SessionID())
			}
		})
	}
}
