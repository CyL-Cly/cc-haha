package modelaccounting

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strings"
	"testing"
)

func storedProject(t *testing.T, raw string) *protocol.JSONObject {
	t.Helper()
	var p protocol.JSONObject
	if e := p.UnmarshalJSON([]byte(raw)); e != nil {
		t.Fatal(e)
	}
	return &p
}
func TestRestoreMismatchPreservesActualChargedLedgerAndUnknownFlag(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	s, _ := NewOwnedSession(owner, nil)
	binding, _ := NewMainContext(owner)
	e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		a, e := s.BeginRequest(scope, originalAccountingInput("fixture-model", "sdk"), binding)
		if e != nil {
			return e
		}
		if e = observeOriginalDelta(scope, a); e != nil {
			return e
		}
		if e = a.Finish(scope, true, nil); e != nil {
			return e
		}
		before, _ := s.Snapshot(scope)
		b, _ := before.MarshalJSON()
		p := storedProject(t, `{"lastSessionId":"other","lastCost":900,"lastModelUsage":"unported"}`)
		restored, e := s.RestoreCostStateForSession(scope, "target", p, RestoredModelResolver{}, nil)
		if e != nil || restored {
			t.Fatal(restored, e)
		}
		after, _ := s.Snapshot(scope)
		c, _ := after.MarshalJSON()
		if string(b) != string(c) || !s.unknownModelCost {
			t.Fatal("mismatch reset current charged ledger")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}
func TestRestoreNullishFieldsAndFalseyDurationKeepExistingUsageAndClock(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	s, _ := NewOwnedSession(owner, nil)
	binding, _ := NewMainContext(owner)
	e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		a, _ := s.BeginRequest(scope, originalAccountingInput("fixture-model", "sdk"), binding)
		if e := observeOriginalDelta(scope, a); e != nil {
			return e
		}
		if e := a.Finish(scope, true, nil); e != nil {
			return e
		}
		p := storedProject(t, `{"lastSessionId":"target","lastCost":null,"lastAPIDuration":null,"lastDuration":0,"lastModelUsage":null,"lastTotalInputTokens":123456,"future":{"untouched":true}}`)
		before, _ := p.MarshalJSON()
		called := false
		restored, e := s.RestoreCostStateForSession(scope, "target", p, RestoredModelResolver{}, func() float64 { called = true; return 99999 })
		if e != nil || !restored {
			t.Fatal(restored, e)
		}
		if called || s.restoredStartMillis != nil || s.cost != 0 || s.apiDuration != 0 || len(s.models.OwnProperties()) != 1 || !s.unknownModelCost {
			t.Fatal("nullish/falsey setter changed wrong state")
		}
		shot, _ := s.Snapshot(scope)
		if num(shot.Get("totalInputTokens")) != 4 {
			t.Fatal("stored lastTotal restored instead of retained real usage", shot)
		}
		after, _ := p.MarshalJSON()
		if string(before) != string(after) {
			t.Fatal("read mutated old persisted input")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}
func TestRestoreReplacesRowsInEnumerationOrderThenRealChargeAccumulates(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	s, _ := NewOwnedSession(owner, nil)
	binding, _ := NewMainContext(owner)
	e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		p := storedProject(t, `{"lastSessionId":"target","lastCost":1.25,"lastAPIDuration":20,"lastAPIDurationWithoutRetries":15,"lastDecodeDuration":12,"lastTtftDuration":3,"lastToolDuration":7,"lastLinesAdded":4,"lastLinesRemoved":1,"lastDuration":5000,"lastModelUsage":{"fixture-model":{"inputTokens":2,"outputTokens":3,"cacheReadInputTokens":4,"cacheCreationInputTokens":5,"webSearchRequests":6,"costUSD":1.25,"future":{"a":[null,true]},"contextWindow":17},"deepseek-flash":{"inputTokens":7,"outputTokens":8,"cacheReadInputTokens":9,"cacheCreationInputTokens":10,"webSearchRequests":0,"costUSD":0.5}}}`)
		restored, e := s.RestoreCostStateForSession(scope, "target", p, RestoredModelResolver{}, func() float64 { return 8000 })
		if e != nil || !restored {
			t.Fatal(restored, e)
		}
		if s.cost != 1.25 || s.apiDuration != 20 || s.apiDurationWithoutRetries != 15 || s.generationDecodeDuration != 12 || s.generationTtftDuration != 3 || s.restoredToolDuration != 7 || *s.restoredStartMillis != 3000 {
			t.Fatal("ordered scalar setter lost Source fields")
		}
		fields := object(s.models.Get("fixture-model")).OwnProperties()
		var names []string
		for _, p := range fields {
			var n string
			_ = json.Unmarshal(p.NameJSON, &n)
			names = append(names, n)
		}
		if strings.Join(names, ",") != "inputTokens,outputTokens,cacheReadInputTokens,cacheCreationInputTokens,webSearchRequests,costUSD,future,contextWindow,maxOutputTokens" {
			t.Fatal(names)
		}
		shot, _ := s.Snapshot(scope)
		if num(shot.Get("totalInputTokens")) != 9 || num(shot.Get("totalOutputTokens")) != 11 {
			t.Fatal(shot)
		}
		a, e := s.BeginRequest(scope, originalAccountingInput("fixture-model", "sdk"), binding)
		if e != nil {
			return e
		}
		if e = observeOriginalDelta(scope, a); e != nil {
			return e
		}
		if e = a.Finish(scope, true, nil); e != nil {
			return e
		}
		row := object(s.models.Get("fixture-model"))
		if num(row.Get("inputTokens")) != 6 || !object(row.Get("future")).IsObject() || s.cost <= 1.25 || !s.unknownModelCost {
			t.Fatal("next finally replaced restored history or unknown fields", row)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}
func TestRestoreUnportedNumericAndModelDomainsRejectWithoutPartialMutation(t *testing.T) {
	for _, raw := range []string{`{"lastSessionId":"target","lastCost":"2"}`, `{"lastSessionId":"target","lastLinesAdded":1.2}`, `{"lastSessionId":"target","lastModelUsage":{"fixture-model":true}}`} {
		owner := cooperative.New()
		s, _ := NewOwnedSession(owner, nil)
		e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			s.cost = 7
			_, e := s.RestoreCostStateForSession(scope, "target", storedProject(t, raw), RestoredModelResolver{}, nil)
			var cap *CapabilityError
			if !errors.As(e, &cap) || s.cost != 7 {
				t.Fatal("unsupported stored domain normalized or partially mutated", e)
			}
			return nil
		})
		owner.Close()
		if e != nil {
			t.Fatal(e)
		}
	}
}

func TestRestoredAliasesGroupOnlyForCostPresentationAndRetainRawRows(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	s, _ := NewOwnedSession(owner, nil)
	e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		p := storedProject(t, `{"lastSessionId":"target","lastCost":0.03125,"lastLinesAdded":7,"lastLinesRemoved":2,"lastDuration":5000,"lastModelUsage":{"private-relay":{"inputTokens":2500,"outputTokens":2,"cacheReadInputTokens":0,"cacheCreationInputTokens":0,"webSearchRequests":0,"costUSD":0.015625,"future":"keep"},"claude-sonnet-5":{"inputTokens":1250,"outputTokens":3,"cacheReadInputTokens":0,"cacheCreationInputTokens":0,"webSearchRequests":1,"costUSD":0.015625}}}`)
		resolver := RestoredModelResolver{Settings: func() (*protocol.JSONObject, error) {
			settings := protocol.NewJSONObject(protocol.JSONField{Name: "modelOverrides", Value: []byte(`{"claude-sonnet-5":"private-relay"}`)})
			return &settings, nil
		}}
		if restored, e := s.RestoreCostStateForSession(scope, "target", p, resolver, func() float64 { return 8000 }); e != nil || !restored {
			t.Fatal(restored, e)
		}
		text, e := s.FormatTotalCost(scope, CostPresentation{NowMillis: func() float64 { return 9000 }, StartMillis: 8000, LinesAdded: 1, CanonicalName: resolver.Canonical})
		if e != nil || strings.Count(text, "claude-sonnet-5:") != 1 || strings.Contains(text, "private-relay:") || !strings.Contains(text, "3.8k input, 5 output") || !strings.Contains(text, "8 lines added, 2 lines removed") || !strings.Contains(text, "Total duration (wall): 6s") {
			t.Fatal("restored ledger/canonical display lost accumulation", text, e)
		}
		shot, e := s.Snapshot(scope)
		rows := object(shot.Get("modelUsage"))
		if e != nil || len(rows.OwnProperties()) != 2 || string(object(rows.Get("private-relay")).Get("future")) != `"keep"` || num(object(rows.Get("private-relay")).Get("maxOutputTokens")) != 64000 {
			t.Fatal("display grouping rewrote shared raw usage rows", shot, e)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}
