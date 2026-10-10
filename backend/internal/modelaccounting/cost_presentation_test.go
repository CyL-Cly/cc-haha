package modelaccounting

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"strings"
	"testing"
)

// Static Source algorithm expectations; no captured table or seeded ledger.
func TestCostDisplayConsumesRealFinallyChargeAndKeepsLedgerIdentity(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	s, _ := NewOwnedSession(owner, nil)
	binding, _ := NewMainContext(owner)
	e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		presentation := CostPresentation{NowMillis: func() float64 { return 2500 }, StartMillis: 0, LinesAdded: 1, LinesRemoved: 0}
		empty, e := s.FormatTotalCost(scope, presentation)
		if e != nil {
			return e
		}
		if !strings.Contains(empty, "Usage:                 0 input, 0 output, 0 cache read, 0 cache write") || strings.Contains(empty, "inaccurate") {
			t.Fatalf("initial ledger formatted wrong: %s", empty)
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
		before, _ := s.Snapshot(scope)
		actual, e := s.FormatTotalCost(scope, presentation)
		if e != nil {
			return e
		}
		wanted := "Total cost:            $0.0001 (costs may be inaccurate due to usage of unknown models)\nTotal duration (API):  0s\nTotal duration (wall): 2s\nTotal code changes:    1 line added, 0 lines removed\nUsage by model:\n       fixture-model:  4 input, 3 output, 2 cache read, 1 cache write ($0.0001)"
		if actual != wanted {
			t.Fatalf("Source format consumer: got %q want %q", actual, wanted)
		}
		after, _ := s.Snapshot(scope)
		b, _ := before.MarshalJSON()
		c, _ := after.MarshalJSON()
		if string(b) != string(c) {
			t.Fatal("display changed sole ledger")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}
func TestCostDurationAndNumberSourceBoundaries(t *testing.T) {
	for _, p := range []struct {
		ms   float64
		want string
	}{{0, "0s"}, {0.5, "0.0s"}, {999, "0s"}, {1000, "1s"}, {59999, "59s"}, {60000, "1m 0s"}, {119500, "2m 0s"}, {3599500, "1h 0m 0s"}, {86400000, "1d 0h 0m"}} {
		if got := durationText(p.ms); got != p.want {
			t.Errorf("ms=%v got=%q want=%q", p.ms, got, p.want)
		}
	}
}

func TestSourceCostToFixedChoosesPositiveDecimalTieAwayFromZero(t *testing.T) {
	// Source (0.03125).toFixed(4) uses positive ties upward. This exact binary
	// value can be charged by 3750 input tokens and two web-search requests.
	if got := costText(0.03125); got != "$0.0313" {
		t.Fatalf("Source decimal tie got %s want $0.0313", got)
	}
}

func TestExactPositiveToFixedUsesBinary64ValueWithoutIntermediateRound(t *testing.T) {
	for _, p := range []struct {
		value  float64
		digits int
		want   string
	}{{2.55, 1, "2.5"}, {1.005, 2, "1.00"}, {1.25, 1, "1.3"}, {0.03125, 4, "0.0313"}, {0, 4, "0.0000"}} {
		if got := positiveFixed(p.value, p.digits); got != p.want {
			t.Errorf("value%g digits%d got%q want%q", p.value, p.digits, got, p.want)
		}
	}
}
func TestCostConsumerRealChargeReachesExactDecimalTie(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	s, _ := NewOwnedSession(owner, nil)
	binding, _ := NewMainContext(owner)
	e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		a, e := s.BeginRequest(scope, originalAccountingInput("fixture-model", "sdk"), binding)
		if e != nil {
			return e
		}
		for _, raw := range []string{`{"type":"message_start","message":{"usage":{"input_tokens":3750,"output_tokens":0,"server_tool_use":{"web_search_requests":2}}}}`, `{"type":"message_delta","usage":{"output_tokens":0}}`} {
			if e = a.Observe(scope, []byte(raw)); e != nil {
				return e
			}
		}
		if e = a.Finish(scope, true, nil); e != nil {
			return e
		}
		state, _ := s.Snapshot(scope)
		if num(state.Get("totalCostUSD")) != 0.03125 {
			t.Fatal("original integer tokens/search did not reach exact tie", state)
		}
		text, e := s.FormatTotalCost(scope, CostPresentation{NowMillis: func() float64 { return 1000 }, StartMillis: 0})
		if e != nil {
			return e
		}
		if !strings.HasPrefix(text, "Total cost:            $0.0313 ") || !strings.Contains(text, "3.8k input") || !strings.Contains(text, "2 web search ($0.0313)") {
			t.Fatal(text)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}
