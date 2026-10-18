package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestRemoteManagedPolicyRealSourceDangerousComparatorAndChecksum(t *testing.T) {
	rows := []map[string]any{
		{"before": nil, "after": map[string]any{}},
		{"before": nil, "after": map[string]any{"apiKeyHelper": "owned-secret-command", "env": map[string]any{"ANTHROPIC_MODEL": "safe", "PRIVATE_DANGER": "owned-value"}, "hooks": map[string]any{"SessionStart": []any{}}}},
		{"before": map[string]any{"apiKeyHelper": "same"}, "after": map[string]any{"apiKeyHelper": "same", "awsAuthRefresh": ""}},
		{"before": map[string]any{"hooks": map[string]any{"a": 1}}, "after": map[string]any{"hooks": map[string]any{"a": 2}}},
		{"before": nil, "after": map[string]any{"env": map[string]any{"anthropic_model": "safe", "CLAUDE_CODE_DISABLE_NONEßENTIAL_TRAFFIC": "safe-sharp-s", "CLAUDE_CODE_DISABLE_NONEſSENTIAL_TRAFFIC": "safe-long-s", "NOT_SAFE": "", "ignored": nil}, "hooks": []any{}}},
		{"before": nil, "after": map[string]any{"hooks": []any{1}, "statusLine": map[string]any{"type": "command", "command": "not-a-dangerous-string"}, "otelHeadersHelper": "owned-headers"}},
	}
	// JSONObject preserves the actual JS insertion order; swapping only the
	// dangerous env order must remain a changed comparison, unlike checksum.
	var before, after protocol.JSONObject
	_ = before.UnmarshalJSON([]byte(`{"env":{"Z":"1","A":"2"}}`))
	_ = after.UnmarshalJSON([]byte(`{"env":{"A":"2","Z":"1"}}`))
	rows = append(rows, map[string]any{"before": before, "after": after})
	var source []struct {
		Before, After string
		Names         []string
		Changed       bool
		Checksum      string
	}
	proxy, ca := claudeAIMCPRefreshPrivateTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("pure comparator invoked HTTP", r.URL)
		w.WriteHeader(403)
	}))
	raw := managedSourceJSON(t, map[string]string{"HTTPS_PROXY": proxy, "NODE_EXTRA_CA_CERTS": ca}, map[string]any{"action": "primitives", "rows": rows})
	if err := json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err, string(raw))
	}
	if len(source) != len(rows) {
		t.Fatal("lost Source rows")
	}
	for index, row := range rows {
		object := func(value any) *protocol.JSONObject {
			if value == nil {
				return nil
			}
			var object protocol.JSONObject
			if err := object.UnmarshalJSON(rawMain(value)); err != nil {
				t.Fatal(err)
			}
			return &object
		}
		previous, next := object(row["before"]), object(row["after"])
		beforeJSON, _ := remoteManagedDangerous(previous)
		afterJSON, names := remoteManagedDangerous(next)
		changed := len(names) != 0 && !bytes.Equal(beforeJSON, afterJSON)
		checksum, err := remoteManagedChecksum(next)
		if err != nil {
			t.Fatal(err)
		}
		actual := source[index]
		if string(beforeJSON) != actual.Before || string(afterJSON) != actual.After || !reflect.DeepEqual(names, actual.Names) || changed != actual.Changed || checksum != actual.Checksum {
			t.Fatalf("actual Source dangerous/checksum row%d\nSource=%s\nGo before=%s after=%s names=%v changed=%v checksum=%s", index, rawMain(actual), beforeJSON, afterJSON, names, changed, checksum)
		}
	}
}

func TestRemoteManagedPolicyConsentPublicationRejectionAndHeldClose(t *testing.T) {
	for _, decision := range []string{"accept", "reject", "close"} {
		t.Run(decision, func(t *testing.T) {
			proxy, ca := claudeAIMCPRefreshPrivateTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"uuid":"owned","checksum":"x","settings":{"apiKeyHelper":"owned-secret-command","model":"opus"}}`)
			}))
			m, f := managedRemoteMain(t, map[string]string{"HTTPS_PROXY": proxy, "NODE_EXTRA_CA_CERTS": ca}, nil)
			s := m.remoteManaged
			s.interactive = true
			events := make(chan NativeManagedPolicyConsentEvent, 4)
			initial, remove := m.SubscribeManagedPolicyConsent(func(event NativeManagedPolicyConsentEvent) { events <- event })
			defer remove()
			if len(initial) != 0 {
				t.Fatal("invented consent")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			bound, release := s.bindProcessContext(ctx)
			defer release()
			if err := m.BeginNativeAPIPreconnect(ctx); err != nil {
				t.Fatal(err)
			}
			var event NativeManagedPolicyConsentEvent
			select {
			case event = <-events:
			case <-ctx.Done():
				t.Fatal("real consent not shown", ctx.Err())
			}
			if event.Closed || !reflect.DeepEqual(event.Consent.Settings, []string{"apiKeyHelper"}) {
				t.Fatal("dialog reveals values or wrong names", event)
			}
			if s.snapshot() != nil {
				t.Fatal("unapproved policy published")
			}
			if _, err := os.Stat(filepath.Join(f.config, "remote-settings.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("unapproved policy persisted", err)
			}
			if decision == "close" {
				m.Close()
				if count, _ := s.refs.Snapshot(); count != 0 {
					t.Fatal("held dialog leaked native worker", count)
				}
				s.mu.Lock()
				pending := s.consents[event.Consent.ID]
				s.mu.Unlock()
				if pending == nil {
					t.Fatal("lost close obligation evidence")
				}
				if _, err := pending.result.Wait(ctx); !errors.Is(err, cooperative.ErrClosed) {
					t.Fatal("close did not settle held decision", err)
				}
				return
			}
			handled, err := m.RespondManagedPolicyConsent(ctx, event.Consent.ID, decision == "accept")
			if !handled {
				t.Fatal("actual input was not consumed")
			}
			if decision == "reject" {
				if !errors.Is(err, ErrManagedPolicyRejected) {
					t.Fatal("reject identity", err)
				}
				select {
				case <-bound.Done():
					if !errors.Is(context.Cause(bound), ErrManagedPolicyRejected) {
						t.Fatal(context.Cause(bound))
					}
				case <-ctx.Done():
					t.Fatal("reject did not exit process lifetime")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if err = m.Owner.Run(ctx, s.wait); err != nil {
				t.Fatal(err)
			}
			if decision == "accept" {
				if s.snapshot() == nil || s.snapshot().String("model") != "opus" {
					t.Fatal("accepted policy never published")
				}
			} else if s.snapshot() != nil {
				t.Fatal("rejected policy published")
			}
			if handled, err = m.RespondManagedPolicyConsent(ctx, event.Consent.ID, true); handled || err != nil {
				t.Fatal("one decision consumed twice", handled, err)
			}
		})
	}
}

type managedDialogOutput struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	ready  chan struct{}
	once   sync.Once
}

func (w *managedDialogOutput) Write(raw []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.buffer.Write(raw)
	if strings.Contains(w.buffer.String(), "Managed settings require approval") {
		w.once.Do(func() { close(w.ready) })
	}
	return n, err
}
func (w *managedDialogOutput) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.String()
}

func TestRemoteManagedPolicyActualCommandTerminalInputAcceptAndReject(t *testing.T) {
	for _, allowed := range []bool{true, false} {
		t.Run(fmt.Sprint(allowed), func(t *testing.T) {
			proxy, ca := claudeAIMCPRefreshPrivateTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"uuid":"owned","checksum":"x","settings":{"apiKeyHelper":"SECRET_NOT_RENDERED"}}`)
			}))
			f := newNativePluginFixture(t)
			delete(f.n.Environment, "ANTHROPIC_BASE_URL")
			f.n.Environment["HTTPS_PROXY"], f.n.Environment["NODE_EXTRA_CA_CERTS"] = proxy, ca
			f.n.Environment["CLAUDE_CODE_OAUTH_TOKEN"] = "fake-owned-env"
			f.n.Environment["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
			f.write(t, filepath.Join(f.config, "settings.json"), `{}`)
			input, writer := io.Pipe()
			defer input.Close()
			defer writer.Close()
			output := &managedDialogOutput{ready: make(chan struct{})}
			m, err := NewMainCommand(MainCommandOptions{Arguments: []string{"auth", "status"}, Native: f.n, Platform: "darwin", Version: "2.1.288", PID: os.Getpid(), Input: input, Output: output, Error: io.Discard})
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			if err = m.BeginNativeAPIPreconnect(ctx); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- m.Run(ctx, m.remoteManaged.wait) }()
			select {
			case <-output.ready:
			case <-ctx.Done():
				t.Fatal("real terminal dialog not rendered", output.String())
			}
			if strings.Contains(output.String(), "SECRET_NOT_RENDERED") {
				t.Fatal("dialog disclosed setting values")
			}
			key := "2"
			if allowed {
				key = "1"
			}
			if _, err = io.WriteString(writer, key); err != nil {
				t.Fatal(err)
			}
			select {
			case err = <-done:
			case <-ctx.Done():
				t.Fatal("real terminal key did not complete", ctx.Err())
			}
			if allowed && err != nil || !allowed && !errors.Is(err, ErrManagedPolicyRejected) {
				t.Fatal("real terminal decision lost", allowed, err)
			}
		})
	}
}

func TestRemoteManagedPolicyPollingActualNativeTimerAndSettingsConsumer(t *testing.T) {
	var mu sync.Mutex
	revision := 0
	proxy, ca := claudeAIMCPRefreshPrivateTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		revision++
		value := revision
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"uuid":"owned","checksum":"server","settings":{"model":"revision-%d","env":{"REMOTE_POLICY_REVISION":"%d"},"permissions":{"deny":["Write"]}}}`, value, value)
	}))
	m, _ := managedRemoteMain(t, map[string]string{"HTTPS_PROXY": proxy, "NODE_EXTRA_CA_CERTS": ca}, nil)
	s := m.remoteManaged
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if err := m.StartSession(ctx); err != nil {
		t.Fatal("actual long-lived host initialization", err)
	}
	// NativeBoundary.Timer is the existing actual timer primitive. Only its
	// explicit one-hour callback is manually fired; there are no sleeps/ticks.
	var callback func()
	var stopped bool
	m.options.Native.Timer = func(delay time.Duration, fire func()) func() {
		if delay != time.Hour {
			t.Error("unexpected newly-armed native delay", delay)
		}
		mu.Lock()
		callback = fire
		stopped = false
		mu.Unlock()
		return func() { mu.Lock(); stopped = true; mu.Unlock() }
	}
	changed := make(chan string, 3)
	notify := s.notify
	s.notify = func(scope *cooperative.Scope) error {
		if err := notify(scope); err != nil {
			return err
		}
		value, err := m.Startup.GetSettingsForSource("policySettings")
		if err != nil {
			return err
		}
		if value == nil {
			return errors.New("poll policy consumer returned no settings")
		}
		actual, permission, err := m.Controller.SDKSettingsOnOwner(scope)
		if err != nil {
			return err
		}
		if actual.String("model") != value.String("model") || !strings.Contains(string(permission.Get("alwaysDenyRules")), `"Write"`) {
			return errors.New("real Controller permission/settings consumer retained stale policy")
		}
		want := strings.TrimPrefix(value.String("model"), "revision-")
		if m.nativeOpenAIEnvironment()["REMOTE_POLICY_REVISION"] != want {
			return errors.New("real Host request environment retained stale policy")
		}
		changed <- value.String("model")
		return nil
	}
	if err := m.BeginNativeAPIPreconnect(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case value := <-changed:
		if value != "revision-1" {
			t.Fatal(value)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	mu.Lock()
	fire := callback
	mu.Unlock()
	if fire == nil {
		t.Fatal("one-hour producer was not registered")
	}
	fire()
	select {
	case value := <-changed:
		if value != "revision-2" {
			t.Fatal("hot reload did not consume poll", value)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := m.Owner.Run(ctx, s.clear); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	didStop := stopped
	mu.Unlock()
	if !didStop {
		t.Fatal("clear left polling registered")
	}
}
