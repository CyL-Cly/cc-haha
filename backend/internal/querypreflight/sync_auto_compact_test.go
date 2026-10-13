package querypreflight

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"strings"
	"testing"
)

func TestNativeAutoCompactCallbackReplacesActualPreflightAndFailureCircuit(t *testing.T) {
	s, _, _, _, _ := testServices(t)
	in := base(t)
	in.Profile = NativeProcessProfile
	in.History = []queryloop.Message{{Record: record(t, `{"type":"user","message":{"content":"`+strings.Repeat("fixture", 100)+`","role":"user"}}`)}}
	in.OriginalEnvironment["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] = "8192"
	in.OriginalEnvironment["CLAUDE_AUTOCOMPACT_PCT_OVERRIDE"] = "1"
	failures := float64(0)
	in.ConsecutiveFailures = &failures
	calls := 0
	in.AutoCompact = func(ctx context.Context, history []queryloop.Message) ([]queryloop.Message, error) {
		calls++
		if len(history) == 0 {
			t.Fatal("actual history missing")
		}
		return history, nil
	}
	_, err := s.Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || failures != 0 {
		t.Fatal(calls, failures)
	}
	in.AutoCompact = func(context.Context, []queryloop.Message) ([]queryloop.Message, error) {
		calls++
		return nil, errors.New("real compact request rejected")
	}
	for range 4 {
		_, err = s.Run(context.Background(), in)
		if err != nil {
			t.Fatal(err)
		}
	}
	if failures != 3 || calls != 4 {
		t.Fatal("auto compact failure circuit missing", failures, calls)
	}
}
