package querypolicy

import (
	"math"
	"strings"
	"testing"
)

func budget(value float64) *float64 { return &value }

func TestBudgetDisabledAndSubagentsDoNotMutateTracker(t *testing.T) {
	tracker := BudgetTracker{ContinuationCount: 4, LastDeltaTokens: 10, LastGlobalTurnTokens: 120, StartedAt: 200}
	for _, test := range []struct {
		agent  string
		amount *float64
	}{
		{"", nil}, {"", budget(0)}, {"", budget(-1)}, {"child", budget(10000)}, {" ", budget(10000)},
	} {
		before := tracker
		decision := CheckTokenBudget(&tracker, test.agent, test.amount, 130, 300)
		if decision.Action != "stop" || decision.CompletionEvent != nil || decision.Continue != nil || tracker != before {
			t.Fatalf("disabled policy changed tracker or returned telemetry: %#v; %#v", decision, tracker)
		}
	}
	// The JS guard returns before dereferencing tracker for these inputs.
	if decision := CheckTokenBudget(nil, "child", budget(10000), 0, 0); decision.Action != "stop" {
		t.Fatal("subagent guard should precede tracker access")
	}
}

func TestBudgetThresholdAndCompletionEventKeepStopTracker(t *testing.T) {
	tracker := NewBudgetTracker(1000)
	if stopped := CheckTokenBudget(&tracker, "", budget(10000), 9000, 1100); stopped.Action != "stop" || stopped.CompletionEvent != nil {
		t.Fatalf("first stop has no completion telemetry: %#v", stopped)
	}
	continued := CheckTokenBudget(&tracker, "", budget(10000), 8999.999, 1200)
	if continued.Action != "continue" || continued.Continue.ContinuationCount != 1 || continued.Continue.Pct != 90 || tracker.LastDeltaTokens != 8999.999 {
		t.Fatalf("rounded percent does not control the strict token threshold: %#v; %#v", continued, tracker)
	}
	before := tracker
	stopped := CheckTokenBudget(&tracker, "", budget(10000), 9000, 900)
	if stopped.Continue != nil || stopped.CompletionEvent == nil || stopped.CompletionEvent.DurationMS != -100 || stopped.CompletionEvent.DiminishingReturns || tracker != before {
		t.Fatalf("stop should retain tracker and raw elapsed time: %#v; %#v", stopped, tracker)
	}
	// A later higher target resumes using the same cross-iteration tracker.
	continued = CheckTokenBudget(&tracker, "", budget(20000), 9500, 1300)
	if continued.Continue == nil || continued.Continue.ContinuationCount != 2 || tracker.LastDeltaTokens != 500.0010000000002 {
		t.Fatalf("changing target unexpectedly reset tracker: %#v; %#v", continued, tracker)
	}
}

func TestBudgetDiminishingReturnsNeedsThreeContinuationsAndTwoSmallDeltas(t *testing.T) {
	tracker := NewBudgetTracker(1000)
	for index, tokens := range []float64{1000, 1500, 2000, 2499, 2999, 3498} {
		decision := CheckTokenBudget(&tracker, "", budget(10000), tokens, 1100+float64(index))
		if decision.Continue == nil || decision.Continue.ContinuationCount != float64(index+1) {
			t.Fatalf("500-token boundary should remain open at iteration %d: %#v", index, decision)
		}
	}
	before := tracker
	for _, tokens := range []float64{3997, 3996} {
		decision := CheckTokenBudget(&tracker, "", budget(10000), tokens, 1500)
		if decision.CompletionEvent == nil || !decision.CompletionEvent.DiminishingReturns || decision.CompletionEvent.ContinuationCount != 6 || tracker != before {
			t.Fatalf("small deltas should stop without consuming state: %#v", decision)
		}
	}
	tracker = NewBudgetTracker(0)
	for _, tokens := range []float64{3000, 2000, 1000} {
		if decision := CheckTokenBudget(&tracker, "", budget(10000), tokens, 1); decision.Action != "continue" {
			t.Fatal("three continuations are required even with decreasing usage")
		}
	}
	if decision := CheckTokenBudget(&tracker, "", budget(10000), 0, 1); decision.CompletionEvent == nil || !decision.CompletionEvent.DiminishingReturns {
		t.Fatal("the original arithmetic treats negative deltas as small deltas")
	}
}

func TestBudgetJavaScriptNumbersRemainObservable(t *testing.T) {
	tracker := NewBudgetTracker(math.Copysign(0, -1))
	if !math.Signbit(tracker.StartedAt) {
		t.Fatal("explicit clock should keep negative zero")
	}
	negative := CheckTokenBudget(&tracker, "", budget(10000), -49.999, 10)
	if negative.Continue == nil || negative.Continue.Pct != 0 || !math.Signbit(negative.Continue.Pct) || !strings.HasPrefix(negative.Continue.NudgeMessage, "Stopped at 0%") {
		t.Fatalf("Math.round and string interpolation have different -0 rules: %#v", negative)
	}
	for _, test := range []struct{ number, rounded float64 }{
		{-0.5, math.Copysign(0, -1)}, {-1.5, -1}, {0.49999999999999994, 0}, {0.5, 1}, {1.5, 2}, {math.Copysign(0, -1), math.Copysign(0, -1)},
	} {
		actual := javascriptRound(test.number)
		if actual != test.rounded || (actual == 0 && math.Signbit(actual) != math.Signbit(test.rounded)) {
			t.Fatalf("Math.round(%v): %v, want %v", test.number, actual, test.rounded)
		}
	}
	tracker = NewBudgetTracker(0)
	if decision := CheckTokenBudget(&tracker, "", budget(math.NaN()), 1, 1); decision.Action != "stop" || decision.CompletionEvent != nil || tracker.ContinuationCount != 0 {
		t.Fatalf("NaN budget should keep the source comparison behavior: %#v", decision)
	}
	continued := CheckTokenBudget(&tracker, "", budget(math.Inf(1)), 1, 1)
	if continued.Continue == nil || continued.Continue.Pct != 0 || !strings.Contains(continued.Continue.NudgeMessage, " / ∞)") {
		t.Fatalf("infinite budget: %#v", continued)
	}
	stopped := CheckTokenBudget(&tracker, "", budget(math.Inf(1)), math.Inf(1), 1)
	if stopped.CompletionEvent == nil || !math.IsNaN(stopped.CompletionEvent.Pct) {
		t.Fatalf("Infinity/Infinity should be NaN: %#v", stopped)
	}
	tracker = BudgetTracker{ContinuationCount: math.Inf(1), LastDeltaTokens: 1, LastGlobalTurnTokens: 1, StartedAt: math.Inf(-1)}
	stopped = CheckTokenBudget(&tracker, "", budget(100), 2, math.Inf(1))
	if stopped.CompletionEvent == nil || !math.IsInf(stopped.CompletionEvent.DurationMS, 1) {
		t.Fatal("do not clamp completion metadata")
	}
	tracker = NewBudgetTracker(0)
	continued = CheckTokenBudget(&tracker, "", budget(math.SmallestNonzeroFloat64), 0, 1)
	if continued.Continue == nil {
		t.Fatal("positive subnormal budget is enabled")
	}
	if decision := CheckTokenBudget(&tracker, "", budget(1), math.NaN(), 2); decision.CompletionEvent == nil || !math.IsNaN(decision.CompletionEvent.Pct) {
		t.Fatal("NaN usage should stop with source telemetry")
	}
	tracker = NewBudgetTracker(0)
	continued = CheckTokenBudget(&tracker, "", budget(100), math.Inf(-1), 1)
	if continued.Continue == nil || !math.IsInf(continued.Continue.Pct, -1) {
		t.Fatal("negative infinite usage keeps source arithmetic")
	}
}

func TestBudgetModelVisibleNudgeUsesPinnedIntlFormatting(t *testing.T) {
	for _, test := range []struct {
		pct, tokens, target float64
		expected            string
	}{
		{1, 1234.5675, 1000000, "Stopped at 1% of token target (1,234.568 / 1,000,000). Keep working — do not summarize."},
		{0, 1.0005, -1.0005, "Stopped at 0% of token target (1.001 / -1.001). Keep working — do not summarize."},
		{math.Copysign(0, -1), math.Copysign(0, -1), 0, "Stopped at 0% of token target (-0 / 0). Keep working — do not summarize."},
		{0, 0.00049999999999999995, -0.0005, "Stopped at 0% of token target (0 / -0.001). Keep working — do not summarize."},
		{0, 999.9995, 9999999.9995, "Stopped at 0% of token target (1,000 / 10,000,000). Keep working — do not summarize."},
		{1e20, 1000000000000000100, 1e21, "Stopped at 100000000000000000000% of token target (1,000,000,000,000,000,100 / 1,000,000,000,000,000,000,000). Keep working — do not summarize."},
		{1e-7, 1e-7, 1e-6, "Stopped at 1e-7% of token target (0 / 0). Keep working — do not summarize."},
		{1e-6, 1.25, 12.125, "Stopped at 0.000001% of token target (1.25 / 12.125). Keep working — do not summarize."},
		{-1.25, 12345.5, -12345.5, "Stopped at -1.25% of token target (12,345.5 / -12,345.5). Keep working — do not summarize."},
		{1.25, 0.001, 0.01, "Stopped at 1.25% of token target (0.001 / 0.01). Keep working — do not summarize."},
		{math.NaN(), math.NaN(), math.Inf(1), "Stopped at NaN% of token target (NaN / ∞). Keep working — do not summarize."},
		{math.Inf(1), math.Inf(-1), 0, "Stopped at Infinity% of token target (-∞ / 0). Keep working — do not summarize."},
		{math.Inf(-1), 1, math.NaN(), "Stopped at -Infinity% of token target (1 / NaN). Keep working — do not summarize."},
		{-1e21, 1e21, 1e-7, "Stopped at -1e+21% of token target (1,000,000,000,000,000,000,000 / 0). Keep working — do not summarize."},
		{1.25e21, 1.25e21, 10000, "Stopped at 1.25e+21% of token target (1,250,000,000,000,000,000,000 / 10,000). Keep working — do not summarize."},
	} {
		if actual := BudgetContinuationMessage(test.pct, test.tokens, test.target); actual != test.expected {
			t.Fatalf("model-visible nudge changed:\n%s\nwant:\n%s", actual, test.expected)
		}
	}
}
