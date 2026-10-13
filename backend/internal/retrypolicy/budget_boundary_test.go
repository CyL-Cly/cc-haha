package retrypolicy

import (
	"context"
	"errors"
	"testing"
)

func TestSharedBudgetRejectsBeforeClientAndKeepsLastFailure(t *testing.T) {
	for _, remaining := range []int{0, 1, 2} {
		deps := attemptDependencies()
		spent, clients, operations := 0, 0, 0
		exhausted := errors.New("No API attempts left for this request")
		original := statusFailure(401)
		deps.ConsumeAttempt = func() error {
			if spent == remaining {
				return exhausted
			}
			spent++
			return nil
		}
		deps.GetClient = func(context.Context) (int, error) {
			clients++
			// An auth/client rejection consumes an attempt too. No operation runs.
			return 0, original
		}
		_, err := RunAttempts(context.Background(), attemptOptions(), attemptHost(), deps, func(int, float64, *RetryContext) (string, error) {
			operations++
			return "", nil
		})
		var stopped *CannotRetryError
		if !errors.As(err, &stopped) || clients != remaining || spent != remaining || operations != 0 {
			t.Fatalf("remaining%d clients%d spent%d operations%d error%v", remaining, clients, spent, operations, err)
		}
		want := error(original)
		if remaining == 0 {
			want = exhausted
		}
		if stopped.Original != want {
			t.Fatal("budget discarded the original last client failure", stopped.Original)
		}
	}
}

func TestCanceledAttemptDoesNotSpendSharedBudget(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	deps := attemptDependencies()
	spent, clients := 0, 0
	deps.ConsumeAttempt = func() error { spent++; return nil }
	deps.GetClient = func(context.Context) (int, error) { clients++; return 1, nil }
	_, err := RunAttempts(ctx, attemptOptions(), attemptHost(), deps, func(int, float64, *RetryContext) (string, error) { return "", nil })
	if err == nil || spent != 0 || clients != 0 {
		t.Fatalf("canceled query crossed budget/client boundary: %v %d %d", err, spent, clients)
	}
}
