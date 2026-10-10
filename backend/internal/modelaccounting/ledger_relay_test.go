package modelaccounting

import (
	"context"
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualChildLedgerDeltasReconstructExistingSharedLedgerWithoutForeignScopes(t *testing.T) {
	parentOwner, childOwner, sharedOwner := cooperative.New(), cooperative.New(), cooperative.New()
	defer parentOwner.Close()
	defer childOwner.Close()
	defer sharedOwner.Close()
	env := map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:1", "ANTHROPIC_API_KEY": "fake-ledger-key"}
	parent, _ := NewOwnedSession(parentOwner, nil)
	child, _ := NewSession(childOwner, "fixture-model", env)
	shared, _ := NewSession(sharedOwner, "fixture-model", env)
	deltas := []LedgerDelta{}
	if e := childOwner.Run(context.Background(), func(scope *cooperative.Scope) error {
		return child.SetLedgerRelay(scope, func(_ *cooperative.Scope, d LedgerDelta) error { deltas = append(deltas, d); return nil })
	}); e != nil {
		t.Fatal(e)
	}
	produce := func(owner *cooperative.Owner, s *Session) error {
		return owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			for _, rows := range [][]string{{`{"type":"message_start","message":{"usage":{"input_tokens":2,"cache_creation_input_tokens":3,"cache_read_input_tokens":4}}}`, `{"type":"message_delta","usage":{"output_tokens":5,"server_tool_use":{"web_search_requests":1}}}`}, {`{"type":"message_start","message":{"usage":{"input_tokens":7,"cache_creation_input_tokens":11,"cache_read_input_tokens":13}}}`, `{"type":"message_delta","usage":{"output_tokens":17}}`}} {
				a, e := s.Begin(scope)
				if e != nil {
					return e
				}
				for _, row := range rows {
					if e = a.Observe(scope, []byte(row)); e != nil {
						return e
					}
				}
				if e = a.Finish(scope, true, nil); e != nil {
					return e
				}
				if e = a.Finish(scope, true, nil); e != nil {
					return e
				}
			}
			if e := s.addDuration(scope, 29, 23); e != nil {
				return e
			}
			return s.AddGenerationDuration(scope, 31, 37)
		})
	}
	if e := produce(childOwner, child); e != nil {
		t.Fatal(e)
	}
	if e := produce(sharedOwner, shared); e != nil {
		t.Fatal(e)
	}
	if len(deltas) != 4 {
		t.Fatal("actual finally relayed twice", len(deltas))
	}
	if e := childOwner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if e := parent.ApplyLedgerDelta(scope, deltas[0]); !errors.Is(e, cooperative.ErrRestricted) {
			t.Fatal("foreign scope admitted", e)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if e := parentOwner.Run(context.Background(), func(scope *cooperative.Scope) error {
		for _, d := range deltas {
			if e := parent.ApplyLedgerDelta(scope, d); e != nil {
				return e
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	var actual, expected, actualDuration, expectedDuration protocol.JSONObject
	_ = parentOwner.Run(context.Background(), func(scope *cooperative.Scope) error {
		actual, _ = parent.Snapshot(scope)
		actualDuration, _ = parent.DurationSnapshot(scope)
		return nil
	})
	_ = sharedOwner.Run(context.Background(), func(scope *cooperative.Scope) error {
		expected, _ = shared.Snapshot(scope)
		expectedDuration, _ = shared.DurationSnapshot(scope)
		return nil
	})
	if string(raw(actual)) != string(raw(expected)) || string(raw(actualDuration)) != string(raw(expectedDuration)) {
		t.Fatal("relay changed existing ledger", actual, expected, actualDuration, expectedDuration)
	}
	for _, d := range deltas {
		copy := d.Clone()
		if copy.usage.IsObject() {
			copy.usage.Set("input_tokens", raw(999))
		}
		if d.usage.IsObject() && num(d.usage.Get("input_tokens")) == 999 {
			t.Fatal("mutable delta escaped clone")
		}
	}
}
