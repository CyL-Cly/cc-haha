package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/querypolicy"
)

func number(raw json.RawMessage) (float64, error) {
	if len(raw) > 0 && raw[0] == '{' {
		var descriptor struct {
			Source string `json:"source"`
		}
		if err := json.Unmarshal(raw, &descriptor); err != nil {
			return 0, err
		}
		raw = json.RawMessage(descriptor.Source)
	}
	value, err := strconv.ParseFloat(string(raw), 64)
	if err != nil && !math.IsInf(value, 0) {
		return 0, err
	}
	return value, nil
}

func project(number float64) any {
	if math.IsNaN(number) {
		return map[string]any{"number": "NaN"}
	}
	if math.IsInf(number, 1) {
		return map[string]any{"number": "Infinity"}
	}
	if math.IsInf(number, -1) {
		return map[string]any{"number": "-Infinity"}
	}
	negativeZero := number == 0 && math.Signbit(number)
	if number == 0 {
		number = 0
	}
	return map[string]any{"number": number, "negativeZero": negativeZero}
}

func trackerProjection(tracker querypolicy.BudgetTracker) any {
	return map[string]any{"continuationCount": project(tracker.ContinuationCount), "lastDeltaTokens": project(tracker.LastDeltaTokens), "lastGlobalTurnTokens": project(tracker.LastGlobalTurnTokens), "startedAt": project(tracker.StartedAt)}
}

func decisionProjection(decision querypolicy.TokenBudgetDecision) any {
	if continuation := decision.Continue; continuation != nil {
		return map[string]any{"action": decision.Action, "nudgeMessage": continuation.NudgeMessage, "continuationCount": project(continuation.ContinuationCount), "pct": project(continuation.Pct), "turnTokens": project(continuation.TurnTokens), "budget": project(continuation.Budget)}
	}
	var event any
	if completion := decision.CompletionEvent; completion != nil {
		event = map[string]any{"continuationCount": project(completion.ContinuationCount), "pct": project(completion.Pct), "turnTokens": project(completion.TurnTokens), "budget": project(completion.Budget), "diminishingReturns": completion.DiminishingReturns, "durationMs": project(completion.DurationMS)}
	}
	return map[string]any{"action": decision.Action, "completionEvent": event}
}

func run() error {
	var input struct {
		HomeDir   string          `json:"homeDir"`
		StartedAt json.RawMessage `json:"startedAt"`
		Steps     []struct {
			Operation string                     `json:"operation"`
			Now       json.RawMessage            `json:"now"`
			AgentID   string                     `json:"agentId"`
			Budget    json.RawMessage            `json:"budget"`
			Tokens    json.RawMessage            `json:"tokens"`
			Pct       json.RawMessage            `json:"pct"`
			Tracker   map[string]json.RawMessage `json:"tracker"`
		} `json:"steps"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		return err
	}
	if input.HomeDir == "" || input.HomeDir != os.Getenv("HOME") || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(input.HomeDir, ".claude") {
		return errors.New("query policy fixture requires explicit disposable HOME/config")
	}
	startedAt, err := number(input.StartedAt)
	if err != nil {
		return err
	}
	tracker := querypolicy.NewBudgetTracker(startedAt)
	results := []any{map[string]any{"tracker": trackerProjection(tracker)}}
	for _, step := range input.Steps {
		switch step.Operation {
		case "reset":
			now, err := number(step.Now)
			if err != nil {
				return err
			}
			tracker = querypolicy.NewBudgetTracker(now)
			results = append(results, map[string]any{"tracker": trackerProjection(tracker)})
		case "restore":
			count, err := number(step.Tracker["continuationCount"])
			if err != nil {
				return err
			}
			delta, err := number(step.Tracker["lastDeltaTokens"])
			if err != nil {
				return err
			}
			tokens, err := number(step.Tracker["lastGlobalTurnTokens"])
			if err != nil {
				return err
			}
			start, err := number(step.Tracker["startedAt"])
			if err != nil {
				return err
			}
			tracker = querypolicy.BudgetTracker{ContinuationCount: count, LastDeltaTokens: delta, LastGlobalTurnTokens: tokens, StartedAt: start}
			results = append(results, map[string]any{"tracker": trackerProjection(tracker)})
		case "check":
			var budget *float64
			if !bytes.Equal(step.Budget, []byte("null")) {
				value, err := number(step.Budget)
				if err != nil {
					return err
				}
				budget = &value
			}
			tokens, err := number(step.Tokens)
			if err != nil {
				return err
			}
			now, err := number(step.Now)
			if err != nil {
				return err
			}
			decision := querypolicy.CheckTokenBudget(&tracker, step.AgentID, budget, tokens, now)
			results = append(results, map[string]any{"decision": decisionProjection(decision), "tracker": trackerProjection(tracker)})
		case "message":
			pct, err := number(step.Pct)
			if err != nil {
				return err
			}
			tokens, err := number(step.Tokens)
			if err != nil {
				return err
			}
			budget, err := number(step.Budget)
			if err != nil {
				return err
			}
			results = append(results, map[string]any{"message": querypolicy.BudgetContinuationMessage(pct, tokens, budget)})
		default:
			return fmt.Errorf("unsupported query policy operation: %s", step.Operation)
		}
	}
	raw, err := json.Marshal(map[string]any{"results": results})
	if err != nil {
		return err
	}
	serialized, err := protocol.ParseAndStringifyJavaScriptJSON(raw)
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(os.Stdout, string(serialized))
	return err
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
