package queryloop

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"reflect"
	"testing"
)

func TestMaxTurnsContinuationProducerAfterToolResults(t *testing.T) {
	for _, max := range []int{1, 2} {
		t.Run(string(rune('0'+max)), func(t *testing.T) {
			h := testHarness(t, wireHandler(completeTool()))
			h.Options.MaxTurns = max
			out, err := h.run(context.Background())
			if err != nil || out.Reason != "max_turns" || int(h.Requests.Load()) != max {
				t.Fatalf("continuation=%+v requests=%d err=%v", out, h.Requests.Load(), err)
			}
			var kinds []string
			for _, y := range h.Yielded {
				if y.Message != nil {
					kinds = append(kinds, y.Message.Type())
				}
			}
			want := []string{}
			for n := 0; n < max; n++ {
				want = append(want, "assistant", "user")
			}
			want = append(want, "attachment")
			if !reflect.DeepEqual(kinds, want) {
				t.Fatalf("yield order=%v want=%v", kinds, want)
			}
			attachment := h.Yielded[len(h.Yielded)-1].Message.Record
			a := fixtureObject(string(attachment.Get("attachment")))
			if a.String("type") != "max_turns_reached" || string(a.Get("maxTurns")) != string(rune('0'+max)) || string(a.Get("turnCount")) != string(rune('1'+max)) || attachment.String("uuid") == "" || attachment.String("timestamp") != "2023-11-14T22:13:20.000Z" {
				t.Fatalf("attachment=%s", attachment.Get("attachment"))
			}
			if len(out.History) != 1+2*max || out.History[len(out.History)-1].Type() != "user" {
				t.Fatalf("model history polluted by terminal: %+v", out)
			}
			if h.Order[len(h.Order)-1] != "message:attachment" {
				t.Fatalf("attachment before actual tool completion: %v", h.Order)
			}
		})
	}
}
func TestMaxTurnsFinalAssistantAndTeamPauseRemainSuccess(t *testing.T) {
	for _, tool := range []bool{false, true} {
		t.Run(map[bool]string{false: "assistant", true: "pause"}[tool], func(t *testing.T) {
			wire := completeText()
			if tool {
				wire = completeTool()
			}
			h := testHarness(t, wireHandler(wire))
			h.Options.MaxTurns = 1
			h.Host.PauseAfterTools = func(context.Context, Round) (bool, error) { return true, nil }
			out, err := h.run(context.Background())
			if err != nil || out.Reason != "completed" || h.Requests.Load() != 1 {
				t.Fatalf("successful final boundary %+v %v", out, err)
			}
			for _, y := range h.Yielded {
				if y.Message != nil && y.Message.Type() == "attachment" {
					t.Fatal("limit applied to a final successful turn")
				}
			}
		})
	}
}
func TestMaxTurnsToolFailureDoesNotBecomeLimit(t *testing.T) {
	h := testHarness(t, wireHandler(completeTool()))
	h.Options.MaxTurns = 1
	sentinel := errors.New("actual tool boundary failure")
	h.Host.ExecuteBatch = func(context.Context, Round, []Invocation, func(Message) error) (any, error) { return nil, sentinel }
	_, err := h.run(context.Background())
	if err != sentinel {
		t.Fatal(err)
	}
	for _, y := range h.Yielded {
		if y.Message != nil && y.Message.Type() == "attachment" {
			t.Fatal("failed batch produced a false max-turn completion")
		}
	}
}
func TestMaxTurnsAbortAfterActualToolsKeepsSubmitCauseAndBoundary(t *testing.T) {
	for _, submit := range []bool{false, true} {
		for _, max := range []int{1, 2} {
			h := testHarness(t, wireHandler(completeTool()))
			h.Options.MaxTurns = max
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			execute := h.Host.ExecuteBatch
			h.Host.ExecuteBatch = func(c context.Context, r Round, u []Invocation, emit func(Message) error) (any, error) {
				out, err := execute(c, r, u, emit)
				if submit {
					cancel(SubmitInterrupt)
				} else {
					cancel(errors.New("user stop"))
				}
				return out, err
			}
			out, err := h.run(ctx)
			if err != nil || out.Reason != "aborted_tools" || h.Requests.Load() != 1 {
				t.Fatalf("abort=%+v %v", out, err)
			}
			var interrupt, limit int
			for _, y := range h.Yielded {
				if y.Message == nil {
					continue
				}
				if y.Message.Type() == "attachment" {
					limit++
				}
				if y.Message.Type() == "user" {
					var content []protocol.JSONObject
					_ = json.Unmarshal(fixtureObject(string(y.Message.Record.Get("message"))).Get("content"), &content)
					if len(content) == 1 && content[0].String("text") == "[Request interrupted by user for tool use]" {
						interrupt++
					}
				}
			}
			wantInt := 1
			if submit {
				wantInt = 0
			}
			wantLimit := 0
			if max == 1 {
				wantLimit = 1
			}
			if interrupt != wantInt || limit != wantLimit {
				t.Fatalf("submit=%v max=%d interruption=%d limit=%d", submit, max, interrupt, limit)
			}
		}
	}
}
func TestMaxTurnsAttachmentConsumerFailureAndReturnRetainOwnership(t *testing.T) {
	h := testHarness(t, wireHandler(completeTool()))
	h.Options.MaxTurns = 1
	sentinel := errors.New("terminal attachment consumer")
	original := h.Emit
	h.Emit = func(y Yield) error {
		if y.Message != nil && y.Message.Type() == "attachment" {
			return sentinel
		}
		return original(y)
	}
	_, err := h.run(context.Background())
	if err != sentinel {
		t.Fatal(err)
	}
	h = testHarness(t, wireHandler(completeTool()))
	h.Options.MaxTurns = 1
	out, err := RunControlled(context.Background(), h.History, h.Options, h.Host, func(y Yield) (requestretry.NoticeAction, error) {
		if y.Message != nil && y.Message.Type() == "attachment" {
			return requestretry.Stop, nil
		}
		return requestretry.Resume, nil
	})
	if err != nil || out.Reason != "consumer_stopped" || h.Requests.Load() != 1 || len(out.History) != 3 {
		t.Fatalf("consumer return=%+v %v", out, err)
	}
}
