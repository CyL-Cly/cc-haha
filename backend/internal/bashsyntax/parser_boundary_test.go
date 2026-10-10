package bashsyntax

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNativeBashParserOriginalNodeBudgetDeadlineAndCancellation(t *testing.T) {
	raw, _ := json.Marshal(strings.Repeat("echo hi; ", 18000))
	if node, err := Parse(context.Background(), raw, ParseOptions{DisableDeadline: true}); node != nil || !errors.Is(err, ErrParseAborted) {
		t.Fatal("original node budget not applied", err)
	}
	source, _ := json.Marshal(strings.Repeat("echo hi; ", 1000))
	if node, err := Parse(context.Background(), source, ParseOptions{Timeout: -time.Millisecond}); node != nil || !errors.Is(err, ErrParseAborted) {
		t.Fatal("original periodic deadline not applied", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if node, err := Parse(ctx, source, ParseOptions{DisableDeadline: true}); node != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("real cancellation lost", err)
	}
}
