package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

var ErrManagedPolicyRejected = errors.New("remote managed settings rejected")

type NativeManagedPolicyConsent struct {
	ID       string
	Settings []string
}
type NativeManagedPolicyConsentEvent struct {
	Consent NativeManagedPolicyConsent
	Closed  bool
}
type nativeManagedConsent struct {
	view   NativeManagedPolicyConsent
	result *ownerasync.OwnerFuture[bool]
}

func remoteManagedDangerous(settings *protocol.JSONObject) (json.RawMessage, []string) {
	shell, env := protocol.NewJSONObject(), protocol.NewJSONObject()
	names := []string{}
	if settings != nil {
		for _, key := range []string{"apiKeyHelper", "awsAuthRefresh", "awsCredentialExport", "gcpAuthRefresh", "otelHeadersHelper", "statusLine"} {
			var value string
			if json.Unmarshal(settings.Get(key), &value) == nil && value != "" {
				shell.Set(key, settings.Get(key))
				names = append(names, key)
			}
		}
		if source, ok := settings.Object("env"); ok {
			for _, property := range source.OwnProperties() {
				var key, value string
				_ = json.Unmarshal(property.NameJSON, &key)
				if json.Unmarshal(property.Value, &value) == nil && value != "" && !clistartupenv.IsSafeManagedEnvironmentKey(key) {
					_ = env.SetPropertyJSON(property.NameJSON, property.Value)
					names = append(names, key)
				}
			}
		}
	}
	snapshot := protocol.NewJSONObject(protocol.JSONField{Name: "shellSettings", Value: rawMain(shell)}, protocol.JSONField{Name: "envVars", Value: rawMain(env)})
	if settings != nil {
		hooks := bytes.TrimSpace(settings.Get("hooks"))
		nonempty := false
		if len(hooks) > 0 && hooks[0] == '{' {
			var object protocol.JSONObject
			nonempty = object.UnmarshalJSON(hooks) == nil && len(object.OwnProperties()) > 0
		}
		if len(hooks) > 0 && hooks[0] == '[' {
			var array []json.RawMessage
			nonempty = json.Unmarshal(hooks, &array) == nil && len(array) > 0
		}
		if nonempty {
			snapshot.Set("hooks", hooks)
			names = append(names, "hooks")
		}
	}
	raw, _ := snapshot.MarshalJSON()
	return protocol.StringifyValidatedJavaScriptJSON(raw), names
}
func (s *nativeRemoteManagedPolicy) security(cached, next *protocol.JSONObject) (bool, error) {
	before, _ := remoteManagedDangerous(cached)
	after, names := remoteManagedDangerous(next)
	if !s.interactive || len(names) == 0 || bytes.Equal(before, after) {
		return true, nil
	}
	var pending *nativeManagedConsent
	err := s.onOwner(s.life, func(scope *cooperative.Scope) error {
		result, err := ownerasync.NewOwnerFuture[bool](s.owner)
		if err != nil {
			return err
		}
		s.mu.Lock()
		s.consentSequence++
		pending = &nativeManagedConsent{NativeManagedPolicyConsent{ID: fmt.Sprintf("managed-policy-%d", s.consentSequence), Settings: append([]string{}, names...)}, result}
		s.consents[pending.view.ID] = pending
		listeners := s.consentListenersLocked()
		s.mu.Unlock()
		for _, listener := range listeners {
			listener(NativeManagedPolicyConsentEvent{Consent: pending.view})
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	if s.commandDialog != nil {
		approved, dialogError := s.commandDialog(s.life, pending.view.ID, pending.view.Settings)
		if dialogError != nil {
			// A failed renderer has no surviving user decision. Settle and remove the
			// exact pending obligation; fetch's outer fail-open catch keeps stale data.
			cleanupError := s.onOwner(s.life, func(scope *cooperative.Scope) error {
				s.mu.Lock()
				delete(s.consents, pending.view.ID)
				listeners := s.consentListenersLocked()
				s.mu.Unlock()
				for _, listener := range listeners {
					listener(NativeManagedPolicyConsentEvent{Consent: pending.view, Closed: true})
				}
				return pending.result.Reject(scope, dialogError)
			})
			if cleanupError != nil {
				return false, cleanupError
			}
			return false, dialogError
		}
		if err = s.onOwner(s.life, func(scope *cooperative.Scope) error {
			s.mu.Lock()
			delete(s.consents, pending.view.ID)
			s.mu.Unlock()
			if !approved {
				s.failSecurity(ErrManagedPolicyRejected)
			}
			return pending.result.Resolve(scope, approved)
		}); err != nil {
			return false, err
		}
	}
	approved, err := pending.result.Wait(s.life)
	if err != nil {
		return false, err
	}
	return approved, nil
}
func (s *nativeRemoteManagedPolicy) consentListenersLocked() []func(NativeManagedPolicyConsentEvent) {
	rows := make([]func(NativeManagedPolicyConsentEvent), 0, len(s.listeners))
	for _, listener := range s.listeners {
		rows = append(rows, listener)
	}
	return rows
}
func (m *MainPrint) SubscribeManagedPolicyConsent(listener func(NativeManagedPolicyConsentEvent)) ([]NativeManagedPolicyConsent, func()) {
	s := m.remoteManaged
	if s == nil || s.owner != m.Owner {
		return nil, func() {}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	initial := []NativeManagedPolicyConsent{}
	// Numeric IDs are creation order, independent of Go map iteration.
	for sequence := uint64(1); sequence <= s.consentSequence; sequence++ {
		if row := s.consents[fmt.Sprintf("managed-policy-%d", sequence)]; row != nil {
			view := row.view
			view.Settings = append([]string{}, view.Settings...)
			initial = append(initial, view)
		}
	}
	if listener == nil || s.closed {
		return initial, func() {}
	}
	s.listenerSequence++
	id := s.listenerSequence
	s.listeners[id] = listener
	return initial, func() { s.mu.Lock(); delete(s.listeners, id); s.mu.Unlock() }
}
func (m *MainPrint) RespondManagedPolicyConsent(ctx context.Context, id string, allowed bool) (bool, error) {
	s := m.remoteManaged
	if s == nil || s.owner != m.Owner {
		return false, nil
	}
	s.mu.Lock()
	pending := s.consents[id]
	s.mu.Unlock()
	if pending == nil {
		return false, nil
	}
	receipt, err := m.Owner.Submit(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if err := s.check(scope); err != nil {
			return err
		}
		s.mu.Lock()
		if s.consents[id] != pending {
			s.mu.Unlock()
			return errors.New("managed settings consent is no longer pending")
		}
		delete(s.consents, id)
		listeners := s.consentListenersLocked()
		s.mu.Unlock()
		if !allowed {
			s.failSecurity(ErrManagedPolicyRejected)
		}
		if err := pending.result.Resolve(scope, allowed); err != nil {
			return err
		}
		for _, listener := range listeners {
			listener(NativeManagedPolicyConsentEvent{Consent: pending.view, Closed: true})
		}
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	if err == nil && !allowed {
		err = ErrManagedPolicyRejected
	}
	return true, err
}
