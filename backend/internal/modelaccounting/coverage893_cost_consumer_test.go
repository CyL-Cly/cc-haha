package modelaccounting

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"math"
	"strings"
	"testing"
)

func TestCoverage893RestoredCostConsumerAggregatesAliasesWithoutReplacingRows(t *testing.T) {
	o := cooperative.New()
	defer o.Close()
	s, e := NewOwnedSession(o, nil)
	if e != nil {
		t.Fatal(e)
	}
	original := `{"lastSessionId":"sid","lastCost":2.75,"lastDuration":86399500,"lastLinesAdded":1,"lastModelUsage":{"alias-a":{"inputTokens":999950,"outputTokens":999950000,"cacheReadInputTokens":999950000000,"cacheCreationInputTokens":0,"webSearchRequests":2,"costUSD":1.25,"future":{"preserve":true}},"alias-b":{"inputTokens":0,"outputTokens":0,"cacheReadInputTokens":0,"cacheCreationInputTokens":0,"webSearchRequests":1,"costUSD":1.5,"future":{"preserve":false}}}}`
	e = o.Run(context.Background(), func(scope *cooperative.Scope) error {
		restored, e := s.RestoreCostStateForSession(scope, "sid", storedProject(t, original), RestoredModelResolver{}, func() float64 { return 86400000 })
		if e != nil {
			return e
		}
		if !restored {
			t.Fatal("restore skipped matching actual SID")
		}
		before, e := s.Snapshot(scope)
		if e != nil {
			return e
		}
		b, _ := before.MarshalJSON()
		calls := 0
		text, e := s.FormatTotalCost(scope, CostPresentation{NowMillis: func() float64 { return 86400000 }, Dim: true, CanonicalName: func(string) (string, error) { calls++; return "private-long-canonical-model😀", nil }})
		if e != nil {
			return e
		}
		for _, want := range []string{"\x1b[2mTotal cost:            $2.75", "Total duration (wall): 1d 0h 0m", "Total code changes:    1 line added, 0 lines removed", "private-long-canonical-model😀:  1.0m input, 1.0b output, 1.0t cache read, 0 cache write, 3 web search ($2.75)", "\x1b[22m"} {
			if !strings.Contains(text, want) {
				t.Fatalf("restored display missing %q: %s", want, text)
			}
		}
		if calls != 2 || strings.Count(text, "private-long-canonical-model😀:") != 1 {
			t.Fatal("aliases were not grouped once", calls, text)
		}
		after, _ := s.Snapshot(scope)
		a, _ := after.MarshalJSON()
		if string(a) != string(b) {
			t.Fatal("display rewrote original usage rows", string(a), string(b))
		}
		reason := errors.New("actual model name resolver rejected")
		_, e = s.FormatTotalCost(scope, CostPresentation{NowMillis: func() float64 { return 86400000 }, CanonicalName: func(string) (string, error) { return "", reason }})
		if e != reason {
			t.Fatal("canonical dependency error identity changed", e)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}
func TestCoverage893CostDisplayRejectsInvalidExternalClockAndTokenDomain(t *testing.T) {
	for _, p := range []CostPresentation{{}, {NowMillis: func() float64 { return 0 }, LinesAdded: -1}, {NowMillis: func() float64 { return math.NaN() }}, {NowMillis: func() float64 { return math.Inf(1) }}, {NowMillis: func() float64 { return -1 }}} {
		o := cooperative.New()
		s, _ := NewOwnedSession(o, nil)
		e := o.Run(context.Background(), func(scope *cooperative.Scope) error {
			before, _ := s.Snapshot(scope)
			b, _ := before.MarshalJSON()
			text, e := s.FormatTotalCost(scope, p)
			if e == nil || text != "" {
				t.Fatal("invalid external presentation admitted", text, e)
			}
			after, _ := s.Snapshot(scope)
			a, _ := after.MarshalJSON()
			if string(a) != string(b) {
				t.Fatal("failed display changed ledger")
			}
			return nil
		})
		o.Close()
		if e != nil {
			t.Fatal(e)
		}
	}
	for _, v := range []float64{-1, math.NaN(), math.Inf(1), 1e15, 0.5} {
		if got, e := numberText(v); e == nil || got != "" {
			t.Fatal("unsupported numeric format rounded silently", v, got, e)
		}
	}
}
