package modelaccounting

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"testing"
)

func TestSessionFinallyUsesActualDeltaAndOwner(t *testing.T) {
	owner, foreign := cooperative.New(), cooperative.New()
	defer owner.Close()
	defer foreign.Close()
	session, e := NewSession(owner, "fixture-model", map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:1", "ANTHROPIC_API_KEY": "original"})
	if e != nil {
		t.Fatal(e)
	}
	e = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		a, e := session.Begin(scope)
		if e != nil {
			return e
		}
		if session.Owns(foreign) || (*Session)(nil).Owns(owner) {
			return errors.New("foreign model session identity admitted")
		}
		if e = a.Observe(scope, []byte(`{"type":"message_start","message":{"usage":{"input_tokens":3,"output_tokens":0}}}`)); e != nil {
			return e
		}
		if e = a.Finish(scope, false, nil); e != nil {
			return e
		}
		s, _ := session.Snapshot(scope)
		if string(s.Get("totalCostUSD")) != "0" {
			return errors.New("message_start alone charged")
		}
		if e = a.Observe(scope, []byte(`{"type":"message_delta","usage":{"output_tokens":2}}`)); !errors.Is(e, cooperative.ErrRestricted) {
			return errors.New("closed attempt accepted usage")
		}
		a, e = session.Begin(scope)
		if e != nil {
			return e
		}
		for _, v := range []string{`{"type":"message_start","message":{"usage":{"input_tokens":1,"cache_read_input_tokens":2,"cache_creation_input_tokens":3}}}`, `{"type":"message_delta","usage":{"output_tokens":2,"server_tool_use":{"web_search_requests":1},"cache_creation":{"ephemeral_1h_input_tokens":1}}}`} {
			if e = a.Observe(scope, []byte(v)); e != nil {
				return e
			}
		}
		id := "original-response"
		if e = a.Finish(scope, true, &id); e != nil {
			return e
		}
		before, _ := session.Snapshot(scope)
		if e = a.Finish(scope, true, &id); e != nil {
			return e
		}
		after, _ := session.Snapshot(scope)
		b, _ := before.MarshalJSON()
		c, _ := after.MarshalJSON()
		if string(b) != string(c) {
			return errors.New("repeated finally charged twice")
		}
		if after.String("lastMainRequestID") != id || string(after.Get("totalInputTokens")) != "1" || string(after.Get("totalOutputTokens")) != "2" {
			return errors.New("actual usage/session fallthrough lost")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	e = foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		if _, e := session.Begin(scope); !errors.Is(e, cooperative.ErrRestricted) {
			return errors.New("foreign session admitted attempt")
		}
		if _, e := session.Snapshot(scope); !errors.Is(e, cooperative.ErrRestricted) {
			return errors.New("foreign state read")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}
func TestAccountingProfileAndMalformedUsageAreExplicit(t *testing.T) {
	for _, v := range []struct {
		model string
		env   map[string]string
	}{{"", nil}, {"claude-sonnet-4", nil}, {"fixture-model[]", nil}, {"fixture-model", map[string]string{}}, {"fixture-model", map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:1", "ANTHROPIC_API_KEY": "original", "CLAUDE_CODE_DISABLE_FAST_MODE": "yes"}}} {
		if _, e := NewCalculator(v.model, v.env); e == nil {
			t.Fatal("unported pricing/profile accepted")
		}
	}
	if _, e := NewSession(nil, "fixture-model", nil); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	for _, v := range []string{`{"unknown":1}`, `{"input_tokens":"2"}`, `{"output_tokens":1e400}`, `{"server_tool_use":{"web_search_requests":"bad"}}`} {
		var part protocol.JSONObject
		_ = part.UnmarshalJSON([]byte(v))
		if _, e := UpdateUsage(ZeroUsage(), part); e == nil {
			t.Fatal("unported usage accepted", v)
		}
	}
	usage := ZeroUsage()
	usage.Set("input_tokens", []byte(`-1`))
	a := Calculator{Model: "fixture-model", Models: protocol.NewJSONObject()}
	if _, e := a.Charge(usage); e == nil {
		t.Fatal("negative usage charged")
	}
	// Omitted/null totals preserve Source's zero and >0 guards; nested recognized
	// metrics update in their original schema order while unknown fields refuse.
	var part protocol.JSONObject
	_ = part.UnmarshalJSON([]byte(`{"input_tokens":null,"output_tokens":null,"cache_read_input_tokens":0,"server_tool_use":null,"iterations":[],"speed":"fast"}`))
	if _, e := UpdateUsage(ZeroUsage(), part); e != nil {
		t.Fatal(e)
	}
}
