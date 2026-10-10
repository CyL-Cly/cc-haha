// Package querypolicy contains isolated policies from the agent query loop.
// Porting these functions does not activate the TOKEN_BUDGET feature or migrate
// the loop's stop hooks, usage accounting, messages, or continuation scheduling.
package querypolicy

import (
	"math"
	"math/big"
	"strconv"
	"strings"
)

// BudgetTracker spans all iterations of one agentic turn. Float64 deliberately
// keeps the source's JavaScript number semantics, including non-finite values.
type BudgetTracker struct {
	ContinuationCount    float64
	LastDeltaTokens      float64
	LastGlobalTurnTokens float64
	StartedAt            float64
}

func NewBudgetTracker(startedAt float64) BudgetTracker {
	return BudgetTracker{StartedAt: startedAt}
}

type BudgetContinuation struct {
	NudgeMessage      string
	ContinuationCount float64
	Pct               float64
	TurnTokens        float64
	Budget            float64
}

type BudgetCompletion struct {
	ContinuationCount  float64
	Pct                float64
	TurnTokens         float64
	Budget             float64
	DiminishingReturns bool
	DurationMS         float64
}

// TokenBudgetDecision corresponds to the TS continue/stop union. Continue is
// present only for action=continue; a stop can have a nil completion event.
type TokenBudgetDecision struct {
	Action          string
	Continue        *BudgetContinuation
	CompletionEvent *BudgetCompletion
}

// CheckTokenBudget receives the existing loop's nullable budget and cumulative
// output usage; it does not infer either from messages or provider responses.
// nowMillis is the Date.now value used only by the completion event.
func CheckTokenBudget(tracker *BudgetTracker, agentID string, budget *float64, globalTurnTokens, nowMillis float64) TokenBudgetDecision {
	if agentID != "" || budget == nil || *budget <= 0 {
		return TokenBudgetDecision{Action: "stop"}
	}
	turnTokens := globalTurnTokens
	pct := javascriptRound((turnTokens / *budget) * 100)
	delta := globalTurnTokens - tracker.LastGlobalTurnTokens
	diminishing := tracker.ContinuationCount >= 3 && delta < 500 && tracker.LastDeltaTokens < 500
	if !diminishing && turnTokens < *budget*0.9 {
		tracker.ContinuationCount++
		tracker.LastDeltaTokens = delta
		tracker.LastGlobalTurnTokens = globalTurnTokens
		return TokenBudgetDecision{Action: "continue", Continue: &BudgetContinuation{
			NudgeMessage:      BudgetContinuationMessage(pct, turnTokens, *budget),
			ContinuationCount: tracker.ContinuationCount, Pct: pct, TurnTokens: turnTokens, Budget: *budget,
		}}
	}
	if diminishing || tracker.ContinuationCount > 0 {
		return TokenBudgetDecision{Action: "stop", CompletionEvent: &BudgetCompletion{
			ContinuationCount: tracker.ContinuationCount, Pct: pct, TurnTokens: turnTokens, Budget: *budget,
			DiminishingReturns: diminishing, DurationMS: nowMillis - tracker.StartedAt,
		}}
	}
	return TokenBudgetDecision{Action: "stop"}
}

// BudgetContinuationMessage preserves the exact model-visible nudge, including
// Intl.NumberFormat('en-US')'s grouping and default three fractional digits.
func BudgetContinuationMessage(pct, turnTokens, budget float64) string {
	return "Stopped at " + javascriptNumber(pct) + "% of token target (" + formatEnglishNumber(turnTokens) + " / " + formatEnglishNumber(budget) + "). Keep working — do not summarize."
}

func javascriptRound(number float64) float64 {
	if math.IsNaN(number) || math.IsInf(number, 0) || number == 0 {
		return number
	}
	result := math.Floor(number)
	if number-result >= 0.5 {
		result++
	}
	if result == 0 && math.Signbit(number) {
		return math.Copysign(0, -1)
	}
	return result
}

func javascriptNumber(number float64) string {
	if math.IsNaN(number) {
		return "NaN"
	}
	if math.IsInf(number, 0) {
		if number < 0 {
			return "-Infinity"
		}
		return "Infinity"
	}
	if number == 0 {
		return "0"
	}
	negative := number < 0
	digits, exponent := decimalDigits(math.Abs(number))
	var value string
	if absolute := math.Abs(number); absolute >= 1e-6 && absolute < 1e21 {
		position := exponent + 1
		switch {
		case position <= 0:
			value = "0." + strings.Repeat("0", -position) + digits
		case position >= len(digits):
			value = digits + strings.Repeat("0", position-len(digits))
		default:
			value = digits[:position] + "." + digits[position:]
		}
	} else {
		value = digits[:1]
		if len(digits) > 1 {
			value += "." + digits[1:]
		}
		value += "e"
		if exponent >= 0 {
			value += "+"
		}
		value += strconv.Itoa(exponent)
	}
	if negative {
		value = "-" + value
	}
	return value
}

// Decimal rounding uses the shortest decimal value supplied to Intl, avoiding
// a second binary multiplication (1.0005 * 1000 can drift below the half).
func formatEnglishNumber(number float64) string {
	if math.IsNaN(number) {
		return "NaN"
	}
	if math.IsInf(number, 0) {
		if number < 0 {
			return "-∞"
		}
		return "∞"
	}
	digits, exponent := decimalDigits(math.Abs(number))
	scaled, _ := new(big.Int).SetString(digits, 10)
	power := exponent - (len(digits) - 1) + 3
	if power >= 0 {
		scaled.Mul(scaled, decimalPower(power))
	} else {
		divisor := decimalPower(-power)
		remainder := new(big.Int)
		scaled.QuoRem(scaled, divisor, remainder)
		if remainder.Lsh(remainder, 1).Cmp(divisor) >= 0 {
			scaled.Add(scaled, big.NewInt(1))
		}
	}
	fixed := scaled.String()
	if len(fixed) < 4 {
		fixed = strings.Repeat("0", 4-len(fixed)) + fixed
	}
	integer, fraction := fixed[:len(fixed)-3], strings.TrimRight(fixed[len(fixed)-3:], "0")
	for position := len(integer) - 3; position > 0; position -= 3 {
		integer = integer[:position] + "," + integer[position:]
	}
	if fraction != "" {
		integer += "." + fraction
	}
	if math.Signbit(number) {
		integer = "-" + integer
	}
	return integer
}

func decimalDigits(number float64) (string, int) {
	mantissa, exponentText, _ := strings.Cut(strconv.FormatFloat(number, 'e', -1, 64), "e")
	exponent, _ := strconv.Atoi(exponentText)
	return strings.ReplaceAll(mantissa, ".", ""), exponent
}

func decimalPower(power int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(power)), nil)
}
