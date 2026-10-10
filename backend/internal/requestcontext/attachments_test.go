package requestcontext

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestRetainedAttachmentPreparationIsReusedAcrossAttempts(t *testing.T) {
	input := basicContext(t)
	attachment, err := parseObject(json.RawMessage(`{"type":"attachment","attachment":{"type":"skill_listing","content":"original retained instructions"}}`))
	if err != nil {
		t.Fatal(err)
	}
	input.History = append(input.History, attachment)
	before, _ := json.Marshal(input.History)
	allocations := []string{}
	prepared, err := Prepare(input, Dependencies{UUID: func() string {
		allocations = append(allocations, "uuid")
		return "retained-user"
	}, Now: func() time.Time {
		allocations = append(allocations, "date")
		return time.UnixMilli(1700000000000)
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(allocations) != 2 || allocations[0] != "uuid" || allocations[1] != "date" {
		t.Fatalf("attachment allocation differs from Source UUID then Date: %v", allocations)
	}
	for attempt := 0; attempt < 3; attempt++ {
		result, err := prepared.BuildAttempt(AttemptOptions{Model: input.Options.Model})
		if err != nil || !bytes.Contains(result.PreparedRequest.Body, []byte("original retained instructions")) {
			t.Fatalf("attempt %d lost retained attachment: %v", attempt, err)
		}
		if len(allocations) != 2 {
			t.Fatalf("attempt %d repeated preparation side effects: %v", attempt, allocations)
		}
	}
	after, _ := json.Marshal(input.History)
	if !bytes.Equal(before, after) {
		t.Fatal("attachment preparation changed mutable session history")
	}
}

func TestRetainedMalformedAttachmentDropsBeforeAllocation(t *testing.T) {
	for _, payload := range []string{`{"type":"skill_listing","content":{"toString":null}}`, `{"type":"invoked_skills","skills":null}`, `{"type":"invoked_skills","skills":[]}`} {
		t.Run(payload, func(t *testing.T) {
			input := basicContext(t)
			attachment, _ := parseObject(json.RawMessage(`{"type":"attachment","attachment":` + payload + `}`))
			input.History = append(input.History, attachment)
			calls := 0
			prepared, err := Prepare(input, Dependencies{UUID: func() string { calls++; return "unused" }, Now: func() time.Time { calls++; return time.UnixMilli(0) }})
			if err != nil || calls != 0 || len(prepared.NormalizedHistory()) != 1 {
				t.Fatalf("Source malformed/empty drop acquired allocation or lost ordinary history: calls %d error %v", calls, err)
			}
		})
	}
}

func TestRetainedUnsupportedAttachmentIsNeverSilentlyDropped(t *testing.T) {
	input := basicContext(t)
	attachment, _ := parseObject(json.RawMessage(`{"type":"attachment","attachment":{"type":"unported_kind","content":"must not disappear"}}`))
	input.History = []protocol.JSONObject{attachment}
	prepared, err := Prepare(input, dependencies())
	var capability *CapabilityError
	if prepared != nil || !errors.As(err, &capability) {
		t.Fatalf("unported model-visible data silently disappeared: %v", err)
	}
}
