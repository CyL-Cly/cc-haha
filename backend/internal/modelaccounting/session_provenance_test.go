package modelaccounting

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
)

func originalAccountingInput(model, source string) requestretry.Input {
	return requestretry.Input{Context: requestcontext.Context{Options: requestcontext.Options{Model: model, QuerySource: source}, Host: requestcontext.Host{Env: map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:1", "ANTHROPIC_API_KEY": "original"}}}}
}

func observeOriginalDelta(scope *cooperative.Scope, a *Attempt) error {
	for _, original := range []string{`{"type":"message_start","message":{"model":"different-response-model","usage":{"input_tokens":4,"cache_read_input_tokens":2,"cache_creation_input_tokens":1}}}`, `{"type":"message_delta","usage":{"output_tokens":3}}`} {
		if err := a.Observe(scope, []byte(original)); err != nil {
			return err
		}
	}
	return nil
}

func TestSessionProvenanceMultipleOriginalModelsAndSnapshotIsolation(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	seed := "original-preexisting-main-request"
	s, err := NewOwnedSession(owner, &seed)
	if err != nil {
		t.Fatal(err)
	}
	seed = "later-mutation"
	binding, _ := NewMainContext(owner)
	err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		firstInput := originalAccountingInput("fixture-model", "sdk")
		first, err := s.BeginRequest(scope, firstInput, binding)
		if err != nil {
			return err
		}
		// Begin binds original fields. Later caller changes do not retarget an attempt.
		firstInput.Context.Options.Model = "claude-unported"
		firstInput.Context.Options.QuerySource = "auxiliary-changed"
		firstInput.Context.Host.Env["ANTHROPIC_API_KEY"] = ""
		second, err := s.BeginRequest(scope, originalAccountingInput("deepseek-flash", "sdk"), binding)
		if err != nil {
			return err
		}
		for _, attempt := range []*Attempt{first, second} {
			if err := observeOriginalDelta(scope, attempt); err != nil {
				return err
			}
		}
		for i, attempt := range []*Attempt{second, first} {
			id := fmt.Sprintf("actual-original-request-%d", i)
			if err := attempt.Finish(scope, true, &id); err != nil {
				return err
			}
		}
		snapshot, err := s.Snapshot(scope)
		if err != nil {
			return err
		}
		models := object(snapshot.Get("modelUsage"))
		if len(models.OwnProperties()) != 2 || models.Get("fixture-model") == nil || models.Get("deepseek-flash") == nil || models.Get("different-response-model") != nil || snapshot.String("lastMainRequestID") != "actual-original-request-1" || num(snapshot.Get("totalInputTokens")) != 8 || num(snapshot.Get("totalOutputTokens")) != 6 {
			return errors.New("per-request original model/source or concurrent-attempt ledger lost")
		}
		if num(snapshot.Get("totalCostUSD")) != 2*6.135e-05 {
			return errors.New("per-attempt cost did not accumulate once at finally")
		}
		snapshot.Set("totalCostUSD", raw(99))
		snapshot.Set("modelUsage", []byte(`{}`))
		again, _ := s.Snapshot(scope)
		if num(again.Get("totalCostUSD")) != 2*6.135e-05 || len(object(again.Get("modelUsage")).OwnProperties()) != 2 {
			return errors.New("read-only snapshot mutated session authority")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSessionProvenanceOriginalContextAndFallthroughPredicate(t *testing.T) {
	for _, profile := range []struct {
		name, source   string
		active, normal bool
		requestID      *string
		changes        bool
	}{
		{"sdk-main", "sdk", false, true, ptr("actual-id"), true},
		{"repl-prefix", "repl_main_thread_provenance", false, true, ptr("actual-id"), true},
		{"other-query", "auxiliary", false, true, ptr("actual-id"), false},
		{"almost-prefix", "repl_main_threa", false, true, ptr("actual-id"), false},
		{"original-empty-store-object", "sdk", true, true, ptr("actual-id"), false},
		{"return-after-delta", "sdk", false, false, ptr("actual-id"), false},
		{"caller-abort-finally", "sdk", false, false, nil, false},
		{"empty-header", "sdk", false, true, ptr(""), false},
		{"no-header", "sdk", false, true, nil, false},
	} {
		t.Run(profile.name, func(t *testing.T) {
			owner := cooperative.New()
			defer owner.Close()
			seed := "original-preexisting-main-request"
			s, _ := NewOwnedSession(owner, &seed)
			binding, _ := NewMainContext(owner)
			if profile.active {
				original := protocol.NewJSONObject()
				binding, _ = NewAgentContext(owner, original)
				original.Set("laterMutation", raw(true))
				if binding.original.Get("laterMutation") != nil {
					t.Fatal("context token borrowed mutable original object")
				}
			}
			err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				a, err := s.BeginRequest(scope, originalAccountingInput("fixture-model", profile.source), binding)
				if err != nil {
					return err
				}
				if err := observeOriginalDelta(scope, a); err != nil {
					return err
				}
				if err := a.Finish(scope, profile.normal, profile.requestID); err != nil {
					return err
				}
				snapshot, _ := s.Snapshot(scope)
				expected := seed
				if profile.changes {
					expected = *profile.requestID
				}
				if snapshot.String("lastMainRequestID") != expected || num(snapshot.Get("totalCostUSD")) != 6.135e-05 {
					return fmt.Errorf("actual finally charge / later lastID predicate changed: %s", snapshot.String("lastMainRequestID"))
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func ptr(v string) *string { return &v }

func TestSessionProvenanceAdmissionAndOriginalErrors(t *testing.T) {
	owner, foreign := cooperative.New(), cooperative.New()
	defer owner.Close()
	defer foreign.Close()
	s, _ := NewOwnedSession(owner, nil)
	main, _ := NewMainContext(owner)
	other, _ := NewMainContext(foreign)
	for _, makeBinding := range []func() error{
		func() error { _, e := NewMainContext(nil); return e },
		func() error { _, e := NewAgentContext(nil, protocol.NewJSONObject()); return e },
		func() error { _, e := NewOwnedSession(nil, nil); return e },
	} {
		if !errors.Is(makeBinding(), cooperative.ErrRestricted) {
			t.Fatal("nil-owner original context/session admitted")
		}
	}
	if _, e := NewAgentContext(owner, protocol.JSONObject{}); e == nil {
		t.Fatal("nonobject original ALS context admitted")
	}
	if main.Owns(nil) || other.Owns(owner) || (*AgentContextBinding)(nil).Owns(owner) {
		t.Fatal("context token owner identity confused")
	}
	var stale *cooperative.Scope
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		stale = scope
		for _, binding := range []*AgentContextBinding{nil, other} {
			if _, e := s.BeginRequest(scope, originalAccountingInput("fixture-model", "sdk"), binding); e == nil {
				return errors.New("missing/foreign original context admitted")
			}
		}
		if _, e := s.Begin(scope); e == nil {
			return errors.New("new owned session silently used legacy request provenance")
		}
		for _, input := range []requestretry.Input{
			originalAccountingInput("claude-sonnet-4", "sdk"),
			{Context: requestcontext.Context{Options: requestcontext.Options{Model: "fixture-model", QuerySource: "sdk"}}},
			{Context: originalAccountingInput("fixture-model", "sdk").Context, ResolvedFastMode: newTrue()},
		} {
			if _, e := s.BeginRequest(scope, input, main); e == nil {
				return errors.New("unported model/env/active fast pricing admitted")
			}
		}
		a, _ := s.BeginRequest(scope, originalAccountingInput("fixture-model", "sdk"), main)
		if e := a.Observe(scope, []byte(`{"type":`)); e == nil {
			return errors.New("malformed native event did not preserve parse error")
		}
		if e := a.Observe(scope, []byte(`{"type":"message_delta","usage":{"output_tokens":"unported"}}`)); e == nil || a.seen {
			return errors.New("unported usage marked chargeable before validation")
		}
		if e := a.Observe(scope, []byte(`{"type":"content_block_stop","index":0}`)); e != nil {
			return e
		}
		if e := a.Finish(scope, false, nil); e != nil {
			return e
		}
		snapshot, _ := s.Snapshot(scope)
		if snapshot.Get("lastMainRequestID") != nil || num(snapshot.Get("totalCostUSD")) != 0 {
			return errors.New("invalid/empty attempt mutated accounting")
		}
		// A real negative token delta reaches the existing Charge failure unchanged.
		a, _ = s.BeginRequest(scope, originalAccountingInput("fixture-model", "sdk"), main)
		if e := a.Observe(scope, []byte(`{"type":"message_delta","usage":{"output_tokens":-1}}`)); e != nil {
			return e
		}
		if e := a.Finish(scope, false, nil); e == nil || e.Error() != "invalid actual token usage" {
			return errors.New("original pricing failure identity changed")
		}
		if e := a.Finish(scope, true, ptr("not-written-after-failed-finally")); e != nil {
			return e
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, e := s.BeginRequest(stale, originalAccountingInput("fixture-model", "sdk"), main); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal("stale scope admitted")
	}
	err = foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		if _, e := s.BeginRequest(scope, originalAccountingInput("fixture-model", "sdk"), main); !errors.Is(e, cooperative.ErrRestricted) {
			return errors.New("foreign scope admitted request")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, e := (*Session)(nil).BeginRequest(nil, requestretry.Input{}, main); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal("nil session admitted")
	}
	if e := (*Attempt)(nil).Finish(nil, false, nil); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal("nil attempt finalized")
	}
}

func newTrue() *bool { value := true; return &value }
