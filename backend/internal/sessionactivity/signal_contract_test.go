package sessionactivity

import (
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestSignalReadsLiveEnvironmentAndOriginalCallbackError(t *testing.T) {
	environment := "false"
	h := newActivityHarness(t, &environment)
	original := errors.New("original synchronous transport callback failure")
	calls := 0
	h.run(t, func(s *cooperative.Scope) error {
		if err := h.tracker.RegisterCallback(s, func(next *cooperative.Scope) error {
			if next != s {
				return errors.New("callback changed original owner scope")
			}
			calls++
			return original
		}); err != nil {
			return err
		}
		if active, err := h.tracker.TrackingActive(s); err != nil || !active {
			return errors.New("registered callback not active")
		}
		if err := h.tracker.SendSignal(s); err != nil || calls != 0 {
			return errors.New("falsy environment invoked transport")
		}
		environment = "\ufeff ON\u00a0"
		if err := h.tracker.SendSignal(s); err != original || calls != 1 {
			return errors.New("live truthy getter/original synchronous error identity lost")
		}
		if err := h.tracker.UnregisterCallback(s); err != nil {
			return err
		}
		if active, err := h.tracker.TrackingActive(s); err != nil || active {
			return errors.New("unregistered callback remained active")
		}
		if err := h.tracker.SendSignal(s); err != nil || calls != 1 {
			return errors.New("signal invoked removed callback")
		}
		if h.nowCalls != 0 || h.dateCalls != 0 || len(h.entries) != 0 {
			return errors.New("registration/signal fabricated work or diagnostics")
		}
		return nil
	})
}

func TestSignalJavaScriptEnvironmentWhitespace(t *testing.T) {
	for _, v := range []string{"true", " yes ", "1", "ON", "\ufeffon\u2028"} {
		if !keepaliveTruthy(v) {
			t.Fatal("truthy original environment rejected", v)
		}
	}
	for _, v := range []string{"", "false", "0", "off", "\u0085on\u0085"} {
		if keepaliveTruthy(v) {
			t.Fatal("falsy original environment accepted", v)
		}
	}
}

func TestHeartbeatRemainsTypedCapabilityAfterRealFirstStartClock(t *testing.T) {
	h := newActivityHarness(t)
	h.run(t, func(s *cooperative.Scope) error {
		if err := h.tracker.RegisterCallback(s, func(*cooperative.Scope) error { return nil }); err != nil {
			return err
		}
		err := h.tracker.Start(s, APICall)
		var cap *CapabilityError
		if !errors.As(err, &cap) || h.tracker.refs != 1 || h.nowCalls != 1 || h.tracker.cleanupRegistered || h.tracker.oldest == nil {
			return errors.New("unsupported interval was hidden or first Source prefix lost")
		}
		return h.tracker.UnregisterCallback(s)
	})
}
