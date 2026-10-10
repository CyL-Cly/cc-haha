package sdkhost_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
)

// Real Host, actual SDK consumer, unique queryloop/modelyield and physical
// loopback transport. Only a public generated delta advances the raw clock.
func TestSDKProducerClockGenerationAtCompletedMessage(t *testing.T) {
	for _, varying := range []bool{false, true} {
		t.Run(map[bool]string{false: "constant", true: "public-delta"}[varying], func(t *testing.T) {
			clock := float64(1000)
			var final protocol.JSONObject
			var activeScope *cooperative.Scope
			starts, stops := 0, 0
			_, result := originalBoundary(t, nativeControls{Configure: func(o *sdkhost.Options) {
				o.NowMillis = func() float64 { return clock }
				o.OriginalEnv["CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK"] = "1"
				o.OriginalEnv["CLAUDE_STREAM_TRANSIENT_RETRY_MAX"] = "0"
				o.OriginalEnv["CLAUDE_ENABLE_STREAM_WATCHDOG"] = "0"
				enqueue := o.Output.Enqueue
				o.Output.Enqueue = func(data []byte) error {
					value := object(data)
					if value.String("type") == "stream_event" {
						event := object(value.Get("event"))
						ledger, e := o.Controller.ModelAccountingFor(o.Owner)
						if e != nil {
							return e
						}
						view, e := ledger.GenerationSnapshot(activeScope)
						if e != nil {
							return e
						}
						var decode float64
						if e = json.Unmarshal(view.Get("totalDecodeDuration"), &decode); e != nil {
							return e
						}
						completed := stops
						if event.String("type") == "message_stop" {
							completed++
						}
						want := float64(0)
						if varying {
							want = float64(completed) * 73
						}
						if decode != want {
							t.Errorf("generation update must precede only public message_stop: event=%s decode=%v want=%v", event.String("type"), decode, want)
						}
						switch event.String("type") {
						case "message_start":
							starts++
						case "content_block_delta":
							if varying && object(event.Get("delta")).String("type") == "text_delta" {
								clock += 73
							}
						case "message_stop":
							stops++
						}
					}
					return enqueue(data)
				}
			}, Run: func(o sdkhost.Options, h *sdkhost.Host, inputs []protocol.JSONObject) error {
				return o.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
					activeScope = scope
					for _, input := range inputs {
						if e := h.RunOne(scope, context.Background(), input); e != nil {
							return e
						}
					}
					ledger, e := o.Controller.ModelAccountingFor(o.Owner)
					if e != nil {
						return e
					}
					final, e = ledger.GenerationSnapshot(scope)
					if e != nil {
						return e
					}
					_, e = ledger.GenerationSnapshot(nil)
					if !errors.Is(e, cooperative.ErrRestricted) {
						t.Fatal("nil Scope accessed Session generation")
					}
					return nil
				})
			}})
			if result["runError"] != nil {
				t.Fatal(result["runError"])
			}
			if starts != 2 || stops != 2 {
				t.Fatalf("actual two ASK boundaries %d/%d", starts, stops)
			}
			var decode, ttft float64
			if e := json.Unmarshal(final.Get("totalDecodeDuration"), &decode); e != nil {
				t.Fatal(e)
			}
			if e := json.Unmarshal(final.Get("totalTtftDuration"), &ttft); e != nil {
				t.Fatal(e)
			}
			want := float64(0)
			if varying {
				want = 146
			}
			if decode != want || ttft != 0 {
				t.Fatalf("generation did not follow actual completed messages: decode=%v want=%v ttft=%v", decode, want, ttft)
			}
		})
	}
}

func TestSDKProducerClockOrdinaryStreamWithUnresolvedPolicy(t *testing.T) {
	for _, value := range []string{"YES", "", "0"} {
		t.Run("value="+value, func(t *testing.T) {
			_, result := originalBoundary(t, nativeControls{Configure: func(o *sdkhost.Options) { o.OriginalEnv["CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK"] = value }})
			if result["runError"] != nil {
				t.Fatal(result["runError"])
			}
			requests := result["requests"].([]string)
			if len(requests) != 2 {
				t.Fatalf("ordinary unresolved policy did not reach both ASK: %v", requests)
			}
			for _, body := range requests {
				if strings.Contains(body, "CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK") || strings.Contains(body, "CLAUDE_STREAM_TRANSIENT_RETRY_MAX") || strings.Contains(body, "CLAUDE_ENABLE_STREAM_WATCHDOG") {
					t.Fatal("producer environment leaked into actual HTTP body")
				}
			}
		})
	}
}

func TestSDKProducerClockTerminalPrintExitFromActualResult(t *testing.T) {
	raw, e := os.ReadFile("testdata/producer-clock.original-profiles.json")
	if e != nil {
		t.Fatal(e)
	}
	var originals []struct {
		ID        string                          `json:"id"`
		Responses []struct{ Prefix, Tail string } `json:"responses"`
	}
	if e = json.Unmarshal(raw, &originals); e != nil {
		t.Fatal(e)
	}
	var wire *originalLoopbackWire
	for _, original := range originals {
		if original.ID == "sdk-nonretryable-after-delta" {
			wire = &originalLoopbackWire{Prefix: original.Responses[0].Prefix, FirstTail: original.Responses[0].Tail, SecondWire: original.Responses[1].Prefix + original.Responses[1].Tail}
		}
	}
	if wire == nil {
		t.Fatal("missing original external terminal responses")
	}
	code, errorResults, messageStops := 0, 0, 0
	_, result := originalBoundary(t, nativeControls{ExternalWire: wire, Configure: func(o *sdkhost.Options) {
		o.OriginalEnv["CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK"] = "1"
		o.OriginalEnv["CLAUDE_STREAM_TRANSIENT_RETRY_MAX"] = "0"
		o.OriginalEnv["CLAUDE_ENABLE_STREAM_WATCHDOG"] = "0"
		enqueue := o.Output.Enqueue
		o.Output.Enqueue = func(data []byte) error {
			value := object(data)
			if value.String("type") == "result" && string(value.Get("is_error")) == "true" {
				errorResults++
			}
			if value.String("type") == "stream_event" && object(value.Get("event")).String("type") == "message_stop" {
				messageStops++
			}
			return enqueue(data)
		}
	}, Run: func(o sdkhost.Options, h *sdkhost.Host, inputs []protocol.JSONObject) error {
		return o.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			for _, input := range inputs {
				if e := h.RunOne(scope, context.Background(), input); e != nil {
					return e
				}
			}
			code = h.ExitCode()
			ledger, e := o.Controller.ModelAccountingFor(o.Owner)
			if e != nil {
				return e
			}
			generation, e := ledger.GenerationSnapshot(scope)
			if e != nil {
				return e
			}
			if string(generation.Get("totalDecodeDuration")) != "0" || string(generation.Get("totalTtftDuration")) != "0" {
				t.Fatal("terminal without message_stop must not charge generation", generation)
			}
			return nil
		})
	}})
	if result["runError"] != nil || code != 1 || errorResults != 2 || messageStops != 0 {
		t.Fatalf("actual terminal caller code=%d errorResults=%d stops=%d err=%v", code, errorResults, messageStops, result["runError"])
	}
}
