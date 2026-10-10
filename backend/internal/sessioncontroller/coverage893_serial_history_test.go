package sessioncontroller

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
	"testing"
)

func TestCoverage893SerialHistoryPreservesLiveEnvelopeUntilFinally(t *testing.T) {
	c, _, o := publicController(t)
	a, e := NewSerialASK(c, o)
	if e != nil {
		t.Fatal(e)
	}
	e = o.Run(context.Background(), func(s *cooperative.Scope) error {
		activationFuture, e := c.BeginActivate(s, activation())
		if e != nil {
			return e
		}
		if _, e = activationFuture.Await(s); e != nil {
			return e
		}
		if e := a.Accept(s, queryloop.Message{Record: protocol.NewJSONObject()}); e == nil {
			t.Fatal("accept outside ASK changed history")
		}
		in := parsedInput("first", "question")
		accepted, e := a.Begin(s, in)
		if e != nil {
			return e
		}
		if accepted.String("type") != "user" || a.HistoryLength() != 1 {
			t.Fatal(accepted, a.HistoryLength())
		}
		if _, e := a.Begin(s, parsedInput("second", "blocked")); e == nil {
			t.Fatal("overlapping ASK admitted")
		}
		before := a.HistoryLength()
		if e := a.Accept(s, queryloop.Message{}); e == nil || a.HistoryLength() != before {
			t.Fatal("invalid yielded value polluted history", e)
		}
		var record protocol.JSONObject
		if e := record.UnmarshalJSON([]byte(`{"type":"assistant","uuid":"assistant","future":{"keep":true},"message":{"id":"message","role":"assistant","model":"fixture-model","content":[{"type":"text","text":"answer"}],"usage":{"input_tokens":1}}}`)); e != nil {
			return e
		}
		envelope := &streamhistory.Envelope{Record: record}
		if e := a.Accept(s, queryloop.Message{Assistant: envelope}); e != nil {
			return e
		}
		if a.History()[1].Assistant != envelope {
			t.Fatal("assistant live identity severed")
		}
		envelope.Record.Set("future", []byte(`{"keep":false,"late":7}`))
		raw, e := a.Raw(requestcontext.Context{})
		if e != nil {
			return e
		}
		found := false
		for _, r := range raw.Context.History {
			if r.String("uuid") == "assistant" {
				found = string(r.Get("future")) == `{"keep":false,"late":7}`
			}
		}
		if !found {
			t.Fatal("next original context lost late envelope reference", raw.Context.History)
		}
		// This existing component host has no canonical Git binding. Admission
		// must reject rather than fall back to a synchronous legacy writer.
		var boundary *CapabilityError
		if e := a.Record(s, a.History()); !errors.As(e, &boundary) {
			t.Fatal("missing canonical Record branch was admitted", e)
		}
		if a.HistoryLength() != 2 || a.History()[1].Assistant != envelope {
			t.Fatal("independent Record replaced live history")
		}
		if e := a.Finally(s); e != nil {
			return e
		}
		if e := a.Finally(s); e == nil {
			t.Fatal("duplicate finally admitted")
		}
		if a.running || c.readCache != a.cache {
			t.Fatal("finally did not return captured cache")
		}
		if _, e := a.Begin(s, in); e == nil {
			t.Fatal("duplicate runtime UUID started another ASK")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}
func TestCoverage893PublicActivationRestoreErrorSettlesBeforeRetry(t *testing.T) {
	c, _, o := publicController(t)
	c.options.PublicPrint = true
	calls := 0
	c.options.RestoreCosts = func(*cooperative.Scope, string) (bool, error) {
		calls++
		return false, capability("test actual restore dependency")
	}
	e := o.Run(context.Background(), func(s *cooperative.Scope) error {
		f, e := c.BeginActivate(s, activation())
		if e != nil {
			return e
		}
		_, e = f.Await(s)
		if e == nil || c.active || c.activationPending || calls != 1 {
			t.Fatal("failed restore activated or stayed pending", e, c.active, c.activationPending, calls)
		}
		c.options.RestoreCosts = func(*cooperative.Scope, string) (bool, error) { calls++; return true, nil }
		f, e = c.BeginActivate(s, activation())
		if e != nil {
			return e
		}
		_, e = f.Await(s)
		if e != nil {
			return e
		}
		if !c.active || calls != 2 {
			t.Fatal("settled restore failure prevented retry", c.active, calls)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}
