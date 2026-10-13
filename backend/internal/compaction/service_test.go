package compaction

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strings"
	"testing"
	"time"
)

func compactFixture(t *testing.T) (Dependencies, []protocol.JSONObject) {
	t.Helper()
	n := 0
	uuid := func() string { n++; return string(rune('a' + n)) }
	factory, _ := messages.NewFactory(uuid, func() time.Time { return time.UnixMilli(1791766923456) })
	history := []protocol.JSONObject{messages.OrderedUser(factory.CreateUser(mapRaw("content", "keep explicit user request"))), objectTest(`{"type":"assistant","uuid":"old-a","message":{"id":"round1","role":"assistant","content":[{"type":"text","text":"technical decision"}],"usage":{"input_tokens":123456,"output_tokens":10}}}`), messages.OrderedUser(factory.CreateUser(mapRaw("content", "latest request"))), objectTest(`{"type":"assistant","uuid":"latest-a","message":{"id":"round2","role":"assistant","content":[{"type":"text","text":"work state"}],"usage":{"input_tokens":8888,"output_tokens":20}}}`)}
	return Dependencies{Factory: factory, UUID: uuid, Now: func() time.Time { return time.UnixMilli(1791766923456) }, Summarize: func(ctx context.Context, in SummaryInput) (Summary, error) {
		if e := in.Budget.Consume(); e != nil {
			return Summary{}, e
		}
		return Summary{Record: objectTest(`{"type":"assistant"}`), Text: "<analysis>private draft</analysis><summary>the user's task and next work</summary>"}, nil
	}}, history
}
func objectTest(s string) protocol.JSONObject {
	var v protocol.JSONObject
	_ = v.UnmarshalJSON([]byte(s))
	return v
}
func mapRaw(key, value string) map[string]json.RawMessage {
	return map[string]json.RawMessage{key: encode(value)}
}
func TestCompactManualPartialHistoryAndHookOrder(t *testing.T) {
	for _, direction := range []string{"", "from", "up_to"} {
		t.Run(direction, func(t *testing.T) {
			dep, history := compactFixture(t)
			order := []string{}
			dep.PreHook = func(context.Context, string, string) (HookResult, error) {
				order = append(order, "pre")
				return HookResult{Instructions: "keep pending constraints", Display: "pre ok"}, nil
			}
			summary := dep.Summarize
			dep.Summarize = func(ctx context.Context, in SummaryInput) (Summary, error) {
				order = append(order, "model")
				if !strings.Contains(in.Prompt, "keep pending constraints") {
					t.Fatal("hook instruction missing")
				}
				if direction == "up_to" && len(in.History) != 2 {
					t.Fatal("up_to summarized preserved tail")
				}
				if direction == "from" && len(in.History) != 4 {
					t.Fatal("from lost cached prefix")
				}
				return summary(ctx, in)
			}
			dep.Restore = func(context.Context, []protocol.JSONObject) ([]protocol.JSONObject, error) {
				order = append(order, "restore")
				return nil, nil
			}
			dep.SessionStart = func(context.Context) ([]protocol.JSONObject, error) {
				order = append(order, "sessionStart")
				return nil, nil
			}
			dep.PostHook = func(context.Context, string, string) (HookResult, error) {
				order = append(order, "post")
				return HookResult{Display: "post ok"}, nil
			}
			got, e := Run(context.Background(), Input{History: history, Custom: "explicit", Direction: direction, Pivot: 2, Env: map[string]string{"CLAUDE_CODE_MAX_RETRIES": "0"}}, dep)
			if e != nil {
				t.Fatal(e)
			}
			if strings.Join(order, ",") != "pre,model,restore,sessionStart,post" {
				t.Fatal(order)
			}
			if got.History[0].String("subtype") != "compact_boundary" || string(got.History[1].Get("isCompactSummary")) != "true" {
				t.Fatal(got)
			}
			if direction != "" {
				if len(got.History) != 4 {
					t.Fatal("lost preserved segment")
				}
				metadata, _ := got.Boundary.Object("compactMetadata")
				segment, _ := metadata.Object("preservedSegment")
				if segment.String("headUuid") != "" && segment.String("headUuid") != history[map[string]int{"from": 0, "up_to": 2}[direction]].String("uuid") {
					t.Fatal(segment)
				}
				api, _ := got.History[3].Object("message")
				usage, _ := api.Object("usage")
				if string(usage.Get("input_tokens")) != "0" {
					t.Fatal("stale preserved usage", usage)
				}
			}
			if strings.Contains(UserSummary(got.Summary, false, "/private/tmp/transcript", false), "private draft") {
				t.Fatal("analysis retained")
			}
		})
	}
}
func TestCompactSharedBudgetNoFallbackAfterExhaustionAndCancelNoHistory(t *testing.T) {
	dep, history := compactFixture(t)
	calls := 0
	dep.Summarize = func(_ context.Context, in SummaryInput) (Summary, error) {
		calls++
		if e := in.Budget.Consume(); e != nil {
			return Summary{}, e
		}
		return Summary{}, errors.New("physical request failed")
	}
	_, e := Run(context.Background(), Input{History: history, Env: map[string]string{"CLAUDE_CODE_MAX_RETRIES": "0"}}, dep)
	if e == nil || calls != 1 {
		t.Fatal("fallback reset budget", calls, e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	dep.Summarize = func(_ context.Context, in SummaryInput) (Summary, error) {
		cancel()
		return Summary{Record: objectTest(`{"isApiErrorMessage":true}`), Text: "Request was aborted."}, nil
	}
	got, e := Run(ctx, Input{History: history}, dep)
	if !errors.Is(e, context.Canceled) || len(got.History) != 0 {
		t.Fatal("canceled compact accepted fake summary", got, e)
	}
}
func TestCompactPTLRetryKeepsRoundToolPairAndSharedBudget(t *testing.T) {
	dep, history := compactFixture(t)
	calls := 0
	dep.Summarize = func(_ context.Context, in SummaryInput) (Summary, error) {
		calls++
		if e := in.Budget.Consume(); e != nil {
			return Summary{}, e
		}
		if calls == 1 {
			return Summary{Record: objectTest(`{"type":"assistant","errorDetails":"prompt is too long: 30000 tokens > 10000 maximum"}`), Text: "Prompt is too long"}, nil
		}
		if len(in.History) >= len(history) {
			t.Fatal("PTL didn't drop API rounds")
		}
		return Summary{Record: objectTest(`{"type":"assistant"}`), Text: "<summary>preserved task</summary>"}, nil
	}
	got, e := Run(context.Background(), Input{History: history, Env: map[string]string{"CLAUDE_CODE_MAX_RETRIES": "1"}}, dep)
	if e != nil || calls != 2 || len(got.History) != 2 {
		t.Fatal(calls, got, e)
	}
}
