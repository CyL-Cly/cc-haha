package application

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestTerminalAutoConsentRealSessionDefersClassifierPersistsAndRunsPhysicalShell(t *testing.T) {
	for _, entry := range []string{"startup", "carousel"} {
		t.Run(entry, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			var mainCalls, classCalls atomic.Int32
			var proof string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				request := obj(body)
				if string(request.Get("stream")) != "true" {
					classCalls.Add(1)
					if _, err := os.Stat(proof); !os.IsNotExist(err) {
						t.Error("action preceded classifier", err)
					}
					if !bytes.Contains(body, []byte(`"name":"classify_result"`)) {
						t.Error("original classifier schema missing")
					}
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"id":"owned-auto-consent-classifier","content":[{"type":"tool_use","name":"classify_result","input":{"thinking":"owned test marker only","shouldBlock":false,"reason":"private fixture write"}}],"usage":{"input_tokens":2,"output_tokens":1}}`)
					return
				}
				if mainCalls.Add(1) == 1 {
					response(w, "consent-bash", "Bash", map[string]string{"command": "printf AUTO_CONSENT_REAL_TOOL > '" + proof + "'"}, "")
				} else {
					response(w, "consent-final", "", nil, "AUTO_CONSENT_REAL_TOOL completed")
				}
			}))
			defer server.Close()
			o := nativeSessionOptions(t, server.URL)
			o.Interactive = true
			o.Arguments.PermissionMode = "auto"
			o.Arguments.Tools = []string{"Bash"}
			o.Arguments.EnableAutoMode = true
			if entry == "carousel" {
				o.Arguments.PermissionMode = "default"
			}
			disabled := false
			o.Arguments.ComputerUse = &disabled
			settingsPath := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "settings.json")
			_ = os.MkdirAll(filepath.Dir(settingsPath), 0700)
			_ = os.WriteFile(settingsPath, []byte(`{"future":{"preserved":17},"autoMode":{"classifyAllShell":true},"permissions":{"allow":["Bash(*)"]}}`), 0600)
			proof = filepath.Join(o.WorkDir, "auto-consent-proof.txt")
			s, err := NewSession(ctx, o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			events := make(chan tui.Event, 20)
			emit := func(event tui.Event) error { events <- event; return nil }
			request, remove, err := s.BindTerminalAutoConsent(ctx, emit)
			if err != nil {
				t.Fatal(err)
			}
			defer remove()
			if entry == "carousel" {
				if request != nil {
					t.Fatal("CLI availability flag bypassed first-use dialog")
				}
				for _, want := range []string{"acceptEdits", "plan", "auto"} {
					if err = s.InvokeAction(ctx, "chat:cycleMode", emit); err != nil {
						t.Fatal(err)
					}
					mode, _ := s.SubscribePermissionMode(nil)
					if mode != want {
						t.Fatal("cycle label", mode, want)
					}
				}
				// Before the original debounce expires the real Host remains plan,
				// with inactive classifier and all original rules.
				if err = s.process.Owner.Run(ctx, func(scope *cooperative.Scope) error {
					context, err := s.process.Host.NativePermissionContext(scope)
					if err != nil {
						return err
					}
					active, err := s.process.Controller.NativeAutoModeActive(scope)
					if err != nil {
						return err
					}
					if context.String("mode") != "plan" || active || context.Get("strippedDangerousRules") != nil {
						return fmt.Errorf("classifier activated before consent: %s active%v", raw(context), active)
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				for request == nil {
					select {
					case event := <-events:
						if event.Kind == tui.PermissionAsked {
							request = event.Permission
						}
					case <-ctx.Done():
						t.Fatal(context.Cause(ctx))
					}
				}
			}
			if request == nil || request.Title != "Enable auto mode?" || len(request.Choices) != 3 {
				t.Fatal("original first-use choices", request)
			}
			if err = s.Submit(ctx, tui.Input{Text: "must wait for consent"}, nil); err == nil {
				t.Fatal("pending consent admitted model input")
			}
			if mainCalls.Load() != 0 || classCalls.Load() != 0 {
				t.Fatal("consent spent model calls before acceptance")
			}
			if err = s.RespondPermission(ctx, request.ID, request.Choices[0].Decision); err != nil {
				t.Fatal(err)
			}
			if err = s.RespondPermission(ctx, request.ID, request.Choices[0].Decision); err == nil {
				t.Fatal("retired consent accepted twice")
			}
			data, err := os.ReadFile(settingsPath)
			if err != nil {
				t.Fatal(err)
			}
			saved := obj(data)
			permissions, _ := saved.Object("permissions")
			if string(saved.Get("skipAutoPermissionPrompt")) != "true" || permissions.String("defaultMode") != "auto" || !bytes.Contains(saved.Get("future"), []byte("17")) {
				t.Fatal("trusted opt-in lost unknown settings", string(data))
			}
			if err = s.Submit(ctx, tui.Input{TurnID: "auto-consent-turn", Text: "Create the declared owned marker only"}, nil); err != nil {
				t.Fatal(err)
			}
			data, err = os.ReadFile(proof)
			if err != nil || string(data) != "AUTO_CONSENT_REAL_TOOL" || mainCalls.Load() != 2 || classCalls.Load() != 1 {
				t.Fatal("actual tool/classifier output", string(data), err, mainCalls.Load(), classCalls.Load())
			}
		})
	}
}

func TestTerminalAutoConsentDeclineAndCycleAwayRetireOwnedDialog(t *testing.T) {
	for _, action := range []string{"decline", "cycle-away", "startup-decline"} {
		t.Run(action, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			o := nativeSessionOptions(t, "http://127.0.0.1:1")
			o.Interactive = true
			o.Arguments.PermissionMode = "default"
			o.Arguments.EnableAutoMode = true
			if action == "startup-decline" {
				o.Arguments.PermissionMode = "auto"
			}
			disabled := false
			o.Arguments.ComputerUse = &disabled
			s, err := NewSession(ctx, o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			events := make(chan tui.Event, 20)
			emit := func(event tui.Event) error { events <- event; return nil }
			request, remove, err := s.BindTerminalAutoConsent(ctx, emit)
			if err != nil {
				t.Fatal(err)
			}
			defer remove()
			if request == nil {
				for i := 0; i < 3; i++ {
					if err = s.InvokeAction(ctx, "chat:cycleMode", emit); err != nil {
						t.Fatal(err)
					}
				}
				pending, err := s.process.TerminalAutoConsent(ctx)
				if err != nil {
					t.Fatal(err)
				}
				request = terminalAutoConsentRequest(pending)
			}
			if action == "cycle-away" {
				if err = s.InvokeAction(ctx, "chat:cycleMode", emit); err != nil {
					t.Fatal(err)
				}
				timer := time.NewTimer(500 * time.Millisecond)
				defer timer.Stop()
				for {
					select {
					case event := <-events:
						if event.Kind == tui.PermissionAsked {
							t.Fatal("retired debounce delivered stale dialog")
						}
					case <-timer.C:
						goto checked
					case <-ctx.Done():
						t.Fatal(context.Cause(ctx))
					}
				}
			checked:
			} else {
				err = s.RespondPermission(ctx, request.ID, tui.PermissionDecision{Allowed: false})
				if action == "startup-decline" {
					if !errors.Is(err, tui.ErrStartupConsentDeclined) {
						t.Fatal(err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
			}
			if pending, err := s.process.TerminalAutoConsent(ctx); err != nil || pending != nil {
				t.Fatal("dialog not retired", pending, err)
			}
			if err = s.RespondPermission(ctx, request.ID, tui.PermissionDecision{Allowed: true}); err == nil {
				t.Fatal("stale approval granted permissions")
			}
			mode, _ := s.SubscribePermissionMode(nil)
			want := "plan"
			if action != "decline" {
				want = "default"
			}
			if mode != want {
				t.Fatal("mode after decline/advance", mode, want)
			}
			if _, err = os.Stat(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "settings.json")); !os.IsNotExist(err) {
				t.Fatal("decline persisted consent", err)
			}
		})
	}
}
