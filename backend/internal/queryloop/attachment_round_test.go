package queryloop

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPostToolAttachmentsUseUpdatedContextAfterUnpausedBatch(t *testing.T) {
	h := testHarness(t, wireHandler(completeTool()))
	attached := Message{Record: fixtureObject(`{"type":"attachment","attachment":{"type":"plan_mode_exit","planFilePath":"/private/plan.md","planExists":true}}`)}
	h.Host.AttachmentsAfterTools = func(_ context.Context, round Round) ([]Message, error) {
		h.Order = append(h.Order, "attachments")
		if round.ToolContext != "updated" || len(round.History) != 3 || round.History[2].Type() != "user" {
			t.Fatal("producer lost actual tool feedback/context", round)
		}
		return []Message{attached}, nil
	}
	h.Host.PauseAfterTools = func(context.Context, Round) (bool, error) { h.Order = append(h.Order, "pause"); return false, nil }
	h.Options.MaxTurns = 1
	outcome, err := h.run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(outcome.History) < 4 || outcome.History[3].Type() != "attachment" || h.Requests.Load() != 1 {
		t.Fatal("attachment not retained", outcome)
	}
	order := strings.Join(h.Order, ",")
	if !strings.Contains(order, "normalize,pause,attachments,message:attachment") {
		t.Fatal(order)
	}
}
func TestTeamSubmitPauseYieldsAllToolResultsBeforeAnyAttachmentSideEffect(t *testing.T) {
	h := testHarness(t, wireHandler(completeTool()))
	h.Host.PauseAfterTools = func(context.Context, Round) (bool, error) { h.Order = append(h.Order, "pause"); return true, nil }
	called := false
	h.Host.AttachmentsAfterTools = func(context.Context, Round) ([]Message, error) {
		called = true
		return nil, errors.New("paused batch consumed attachment state")
	}
	outcome, err := h.run(context.Background())
	if err != nil || outcome.Reason != "completed" || called || len(outcome.History) != 3 || outcome.History[2].Type() != "user" || h.Requests.Load() != 1 {
		t.Fatal(outcome, err, called)
	}
	if !strings.Contains(strings.Join(h.Order, ","), "normalize,pause") {
		t.Fatal(h.Order)
	}
}
func TestAttachmentProducerFailuresCannotStartAnotherModelRequest(t *testing.T) {
	sentinel := errors.New("attachment producer failed")
	for _, mode := range []string{"producer", "consumer", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			h := testHarness(t, wireHandler(completeTool()))
			h.Host.AttachmentsAfterTools = func(context.Context, Round) ([]Message, error) {
				if mode == "producer" {
					return nil, sentinel
				}
				if mode == "invalid" {
					return []Message{{Record: fixtureObject(`{"type":"user"}`)}}, nil
				}
				return []Message{{Record: fixtureObject(`{"type":"attachment","attachment":{"type":"plan_mode_exit"}}`)}}, nil
			}
			original := h.Emit
			h.Emit = func(v Yield) error {
				if mode == "consumer" && v.Message != nil && v.Message.Type() == "attachment" {
					return sentinel
				}
				return original(v)
			}
			_, err := h.run(context.Background())
			if mode == "invalid" {
				if !errors.Is(err, ErrUnsupported) {
					t.Fatal(err)
				}
			} else if err != sentinel {
				t.Fatal(err)
			}
			if h.Requests.Load() != 1 {
				t.Fatal("failed attachment caused another sampling request")
			}
		})
	}
}
