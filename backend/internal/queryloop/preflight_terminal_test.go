package queryloop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// This tests the single-loop typed seam against actual exported Source query
// terminal bytes. The host decision is injected; this does not certify a real
// independent preflight policy or replace its required original-input replay.
func TestActualSourcePreModelTerminalSeam(t *testing.T) {
	raw, err := os.ReadFile("testdata/preflight_terminal_source.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name, Mode  string
		History     []string
		Yielded     []string
		Allocations []struct{ Kind, Value string }
		Terminal    *struct{ Reason string }
	}
	if err := json.Unmarshal(raw, &cases); err != nil || len(cases) != 4 {
		t.Fatalf("actual Source terminal cases %d: %v", len(cases), err)
	}
	for _, item := range cases {
		t.Run(item.Name, func(t *testing.T) {
			h := testHarness(t, wireHandler(completeText()))
			h.History = nil
			for _, source := range item.History {
				h.History = append(h.History, Message{Record: fixtureObject(source)})
			}
			allocations := []struct{ Kind, Value string }{}
			calls := 0
			h.Options.UUID = func() string {
				calls++
				value := fmt.Sprintf("probe-id-%d", calls)
				allocations = append(allocations, struct{ Kind, Value string }{"uuid", value})
				return value
			}
			h.Options.Now = func() time.Time {
				allocations = append(allocations, struct{ Kind, Value string }{"date", "2023-11-14T22:13:20.000Z"})
				return time.UnixMilli(1700000000000)
			}
			h.Host.Preflight = nil
			h.Host.ResolvePreflight = func(_ context.Context, round Round) (PreflightResult, error) {
				if round.Tracking.ChainID != "probe-id-1" || round.Tracking.Depth != 0 {
					t.Fatalf("preflight ran before Source tracking: %+v", round)
				}
				factory, err := messages.NewFactory(h.Options.UUID, h.Options.Now)
				if err != nil {
					return PreflightResult{}, err
				}
				record, err := factory.CreateOrderedAssistantAPIError(storage.Object{"content": json.RawMessage(`"Prompt is too long"`), "error": json.RawMessage(`"invalid_request"`), "businessErrorCode": json.RawMessage(`"prompt_too_long"`)})
				return PreflightResult{History: round.History, ToolContext: "pre-signature-context", Terminal: &PreflightTerminal{Message: Message{Record: record}, Reason: "blocking_limit"}}, err
			}
			yielded := []string{}
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			if item.Mode == "abort-before" {
				cancel(errors.New("user"))
			}
			outcome, err := RunControlled(ctx, h.History, h.Options, h.Host, func(value Yield) (requestretry.NoticeAction, error) {
				if value.RequestStart {
					yielded = append(yielded, `{"type":"stream_request_start"}`)
				} else if value.Message != nil {
					raw, err := value.Message.MarshalJSON()
					if err != nil {
						return 0, err
					}
					yielded = append(yielded, string(raw))
					if item.Mode == "stop-terminal" {
						return requestretry.Stop, nil
					}
				} else {
					t.Fatal("pre-model terminal emitted a provider event")
				}
				return requestretry.Resume, nil
			})
			if err != nil || !reflect.DeepEqual(yielded, item.Yielded) || !reflect.DeepEqual(allocations, item.Allocations) {
				t.Fatalf("Source terminal seam changed: %v\nyield=%v\nwant=%v\nalloc=%v want=%v", err, yielded, item.Yielded, allocations, item.Allocations)
			}
			wantReason := "consumer_stopped"
			if item.Terminal != nil {
				wantReason = item.Terminal.Reason
			}
			if outcome.Reason != wantReason || h.Requests.Load() != 0 || len(h.Order) != 0 || outcome.ToolContext != "pre-signature-context" {
				t.Fatalf("pre-model terminal entered downstream host: %+v requests=%d order=%v", outcome, h.Requests.Load(), h.Order)
			}
			if got, err := Snapshot(outcome.History); err != nil || len(got) != len(item.History) || string(got[0]) != item.History[0] {
				t.Fatalf("terminal accepted its yield into input history: %v %v", got, err)
			}
		})
	}
}

func TestTypedPreflightIsExclusiveAndKeepsErrorOwnership(t *testing.T) {
	h := testHarness(t, wireHandler(completeText()))
	h.Host.ResolvePreflight = func(context.Context, Round) (PreflightResult, error) {
		t.Fatal("ambiguous adapters executed")
		return PreflightResult{}, nil
	}
	if _, err := h.run(context.Background()); err == nil || h.UUIDs != 0 || len(h.Yielded) != 0 {
		t.Fatal("both preflight adapters accepted")
	}
	h.Host.Preflight = nil
	sentinel := errors.New("original preflight failed")
	h.Host.ResolvePreflight = func(context.Context, Round) (PreflightResult, error) { return PreflightResult{}, sentinel }
	if _, err := h.run(context.Background()); err != sentinel || h.Requests.Load() != 0 {
		t.Fatalf("preflight error ownership changed: %v", err)
	}
	h.Host.ResolvePreflight = func(_ context.Context, round Round) (PreflightResult, error) {
		return PreflightResult{History: round.History, Terminal: &PreflightTerminal{Reason: "invented"}}, nil
	}
	_, err := h.run(context.Background())
	scope(t, err, "pre-model terminal decision")
}

func TestTypedPreflightSuppliesEarlierToolContextToOneModelConsumer(t *testing.T) {
	h := testHarness(t, wireHandler(completeText()))
	h.Host.Preflight = nil
	h.Host.ResolvePreflight = func(_ context.Context, round Round) (PreflightResult, error) {
		return PreflightResult{History: round.History, ToolContext: "before-signature"}, nil
	}
	original := h.Host.ResolveModelInput
	h.Host.ResolveModelInput = func(ctx context.Context, round Round) (requestretry.Input, error) {
		if round.ToolContext != "before-signature" {
			t.Fatalf("model lost earlier ToolContext: %+v", round)
		}
		return original(ctx, round)
	}
	outcome, err := h.run(context.Background())
	if err != nil || outcome.ToolContext != "before-signature" || h.Requests.Load() != 1 {
		t.Fatalf("typed normal path %+v %v requests=%d", outcome, err, h.Requests.Load())
	}
}
