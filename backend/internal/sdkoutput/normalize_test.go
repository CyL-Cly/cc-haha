package sdkoutput

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) (*Normalizer, *int) {
	t.Helper()
	calls := new(int)
	factory, _ := messages.NewFactory(func() string { *calls++; return fmt.Sprintf("uuid%d", *calls) }, func() time.Time { *calls++; return time.UnixMilli(0) })
	n, err := New(Dependencies{factory, func() json.RawMessage { return quote("original-session") }, func() float64 { *calls++; return 30000 }, func(string) string { return "on" }})
	if err != nil {
		t.Fatal(err)
	}
	return n, calls
}
func original(t *testing.T, raw string) Message {
	t.Helper()
	message, err := Decode([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return message
}
func encode(t *testing.T, value interface{ MarshalJSON() ([]byte, error) }) string {
	t.Helper()
	raw, err := value.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
func capError(t *testing.T, err error) {
	t.Helper()
	var capability *CapabilityError
	if !errors.As(err, &capability) || capability.Boundary == "" || !strings.Contains(err.Error(), capability.Boundary) {
		t.Fatalf("expected explicit capability, got %v", err)
	}
}
func TestRequiredOriginalDependencies(t *testing.T) {
	n, _ := fixture(t)
	options := n.deps
	for index := 0; index < 4; index++ {
		copy := options
		switch index {
		case 0:
			copy.Factory = nil
		case 1:
			copy.SessionID = nil
		case 2:
			copy.NowMillis = nil
		case 3:
			copy.OriginalEnv = nil
		}
		if _, err := New(copy); err == nil {
			t.Fatal("missing required dependency silently accepted")
		}
	}
	if err := n.NormalizeMessage(original(t, `{"type":"system"}`), nil); err == nil {
		t.Fatal("nil consumer accepted")
	}
}
func TestUnsupportedInputsBeforeAnyAllocation(t *testing.T) {
	n, calls := fixture(t)
	for _, raw := range []string{`null`, `[]`, `invalid`, `{"type":"user"}`, `{"type":"user","message":{}}`, `{"type":"assistant","message":{"content":"text"}}`, `{"type":"user","message":{"content":null}}`, `{"type":"user","message":{"content":[null]}}`, `{"type":"user","message":{"content":[1]}}`, `{"type":"assistant","message":{"content":{}}}`} {
		_, err := Decode([]byte(raw))
		capError(t, err)
	}
	for _, input := range []Message{{Record: protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("assistant")})}, {Record: protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("user")})}, original(t, `{"type":"tombstone"}`)} {
		_, err := n.NormalizeMessages([]Message{input})
		capError(t, err)
	}
	for _, value := range []*streamhistory.Envelope{nil, {}, {Record: protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("user")})}} {
		_, err := FromEnvelope(value)
		capError(t, err)
	}
	for _, apiError := range []bool{false, true} {
		if _, err := Assistant(nil, storage.Object{}, apiError); err == nil {
			t.Fatal("nil Factory accepted")
		}
		_, err := Assistant(n.deps.Factory, storage.Object{"content": json.RawMessage(`null`)}, apiError)
		capError(t, err)
	}
	_, err := User(nil, storage.Object{})
	capError(t, err)
	for _, raw := range []string{`{"type":"progress"}`, `{"type":"progress","data":null}`, `{"type":"progress","data":{"type":"agent_progress","message":null}}`, `{"type":"progress","parentToolUseID":{},"data":{"type":"bash_progress"}}`, `{"type":"progress","parentToolUseID":3,"data":{"type":"powershell_progress"}}`} {
		capError(t, n.NormalizeMessage(original(t, raw), func(*View) error { return nil }))
	}
	if *calls != 0 {
		t.Fatal("unsupported original input advanced factory/clock")
	}
}
func TestConsumerStopAndEagerBlockAllocation(t *testing.T) {
	n, calls := fixture(t)
	message := original(t, `{"type":"user","uuid":"12345678-1234-4234-8234-123456789abc","message":{"content":[{"type":"text","text":"first"},{"type":"text","text":"second"}]}}`)
	stop := errors.New("original consumer return")
	count := 0
	err := n.NormalizeMessage(message, func(*View) error { count++; return stop })
	if !errors.Is(err, stop) || count != 1 || *calls != 4 {
		t.Fatalf("stop/eager allocation changed: %v outputs%d allocations%d", err, count, *calls)
	}
	message = original(t, `{"type":"progress","data":{"type":"bash_progress","elapsedTimeSeconds":1},"parentToolUseID":"p"}`)
	if !errors.Is(n.NormalizeMessage(message, func(*View) error { return stop }), stop) {
		t.Fatal("progress consumer failure lost")
	}
	before := *calls
	if err := n.NormalizeMessage(message, func(*View) error { t.Fatal("failed delivery incorrectly retried without throttle"); return nil }); err != nil || *calls != before+1 {
		t.Fatal(err)
	}
}
func TestSourceUTF16UUIDAndTrimDomain(t *testing.T) {
	for _, input := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(`3`)} {
		_, err := DerivedUUID(input, 0)
		capError(t, err)
	}
	_, err := DerivedUUID(quote("original"), -1)
	capError(t, err)
	got, err := DerivedUUID(json.RawMessage(`"12345678901234567890123\ud83d\ude00suffix"`), 15)
	if err != nil || string(got) != `"12345678901234567890123\ud83d00000000000f"` {
		t.Fatalf("UTF16 prefix %s %v", got, err)
	}
	n, _ := fixture(t)
	message := original(t, `{"type":"assistant","uuid":"u","message":{"content":[{"type":"text","text":3}]}}`)
	capError(t, n.NormalizeMessage(message, func(*View) error { return nil }))
	for _, value := range []string{"1", " true ", "YES", "\ufeffon\ufeff"} {
		if !envTruthy(value) {
			t.Fatalf("JS trim truthy %q", value)
		}
	}
	for _, value := range []string{"0", "false", "\u0085on\u0085"} {
		if envTruthy(value) {
			t.Fatalf("invalid truthy %q", value)
		}
	}
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(`false`), json.RawMessage(`0`), json.RawMessage(`-0`), json.RawMessage(`""`)} {
		if truthy(raw) {
			t.Fatalf("falsey changed: %s", raw)
		}
	}
	if !truthy(json.RawMessage(`true`)) || !truthy(json.RawMessage(`[]`)) {
		t.Fatal("truthy object changed")
	}
}
func TestActualControllerLiveEnvelopeSpreadPoint(t *testing.T) {
	n, _ := fixture(t)
	controller, err := streamhistory.New(streamhistory.Options{UUID: func() string { return "original-uuid" }, Now: func() time.Time { return time.UnixMilli(0) }})
	if err != nil {
		t.Fatal(err)
	}
	var live *streamhistory.Envelope
	emit := func(update streamhistory.Update) error {
		if update.Envelope != nil && live == nil {
			live = update.Envelope
		}
		return nil
	}
	for _, raw := range []string{`{"type":"message_start","message":{"id":"original","type":"message","model":"offline","usage":{"input_tokens":7,"output_tokens":0},"content":[]}}`, `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"first"}}`, `{"type":"content_block_stop","index":0}`, `{"type":"content_block_start","index":1,"content_block":{"type":"server_tool_use","id":"server","name":"offline"}}`, `{"type":"content_block_stop","index":1}`} {
		if err := controller.Feed(json.RawMessage(raw), streamhistory.Timing{}, emit); err != nil {
			t.Fatal(err)
		}
	}
	if live == nil {
		t.Fatal("actual controller did not publish assistant")
	}
	message, err := FromEnvelope(live)
	if err != nil {
		t.Fatal(err)
	}
	var old *View
	if err := n.NormalizeMessage(message, func(value *View) error { old = value; return nil }); err != nil {
		t.Fatal(err)
	}
	// Production controller updates only its last completed envelope. Normalize
	// that exact envelope, then its original message_delta changes live usage.
	var last *streamhistory.Envelope
	controller2, _ := streamhistory.New(streamhistory.Options{UUID: func() string { return "original-second" }, Now: func() time.Time { return time.UnixMilli(0) }})
	emit2 := func(update streamhistory.Update) error {
		if update.Envelope != nil {
			last = update.Envelope
		}
		return nil
	}
	for _, raw := range []string{`{"type":"message_start","message":{"id":"original","type":"message","model":"offline","usage":{"input_tokens":7,"output_tokens":0},"content":[]}}`, `{"type":"content_block_start","index":0,"content_block":{"type":"server_tool_use","id":"server","name":"offline"}}`, `{"type":"content_block_stop","index":0}`} {
		if err := controller2.Feed(json.RawMessage(raw), streamhistory.Timing{}, emit2); err != nil {
			t.Fatal(err)
		}
	}
	current, err := FromEnvelope(last)
	if err != nil {
		t.Fatal(err)
	}
	var frozen *View
	if err = n.NormalizeMessage(current, func(value *View) error { frozen = value; return nil }); err != nil {
		t.Fatal(err)
	}
	before := encode(t, frozen)
	if err = controller2.Feed(json.RawMessage(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":9}}`), streamhistory.Timing{}, emit2); err != nil {
		t.Fatal(err)
	}
	if encode(t, frozen) != before {
		t.Fatal("old spread consumed reassigned usage/stop")
	}
	observed := encode(t, current)
	if !strings.Contains(observed, `"output_tokens":9`) || !strings.Contains(observed, `"stop_reason":"end_turn"`) {
		t.Fatal("original live envelope did not advance")
	}
	last.Content[0].Object.Set("future", quote("late block"))
	if !strings.Contains(encode(t, frozen), `"future":"late block"`) {
		t.Fatal("selected block was deep cloned")
	}
	if encode(t, old) == "" {
		t.Fatal("text view lost")
	}
	var empty *View
	if encode(t, empty) != "null" {
		t.Fatal("nil SDK view changed")
	}
}
func TestExplicitProfileCapabilitiesAndPassThrough(t *testing.T) {
	n, _ := fixture(t)
	for _, raw := range []string{`{"type":"system","future":1}`, `{"type":"attachment","future":null}`, `{"type":"progress","data":{"type":"unknown"}}`} {
		message := original(t, raw)
		values, err := n.NormalizeMessages([]Message{message})
		if err != nil || len(values) != 1 || encode(t, values[0]) != raw {
			t.Fatal("pass through changed", err)
		}
		if err = n.NormalizeMessage(message, func(*View) error { t.Fatal("non SDK type emitted"); return nil }); err != nil {
			t.Fatal(err)
		}
	}
	message := original(t, `{"type":"user","uuid":"u","imagePasteIds":{},"message":{"content":[{"type":"image"}]}}`)
	_, err := n.NormalizeMessages([]Message{message})
	capError(t, err)
	message = original(t, `{"type":"user","uuid":null,"message":{"content":[{"type":"text","text":"one"},{"type":"text","text":"two"}]}}`)
	_, err = n.NormalizeMessages([]Message{message})
	capError(t, err)
	capError(t, n.NormalizeMessage(message, func(*View) error { return nil }))
	for _, raw := range []json.RawMessage{json.RawMessage(`{`), json.RawMessage(`[`), json.RawMessage(`"`)} {
		if _, err = spread(raw); err == nil {
			t.Fatal("invalid spread JSON accepted")
		}
	}
}

// The initial actual Source corpus proved normal string-first eviction. The
// final original-input corpus additionally proves undefined-first no-delete.
func TestStringFirstTrackingEvictionAndExistingUpdate(t *testing.T) {
	n, _ := fixture(t)
	now := float64(30000)
	n.deps.NowMillis = func() float64 { return now }
	keys := []string{}
	emitted := 0
	send := func(key string) {
		raw := fmt.Sprintf(`{"type":"progress","parentToolUseID":%q,"data":{"type":"bash_progress"}}`, key)
		if err := n.NormalizeMessage(original(t, raw), func(*View) error { emitted++; return nil }); err != nil {
			t.Fatal(err)
		}
	}
	for index := 0; index < 100; index++ {
		key := fmt.Sprint("original-", index)
		keys = append(keys, key)
		send(key)
	}
	now = 60000
	send(keys[50])
	if len(n.progress) != 99 || n.progressOrder[0] != "string:"+string(quote(keys[1])) {
		t.Fatal("capacity existing-key update should delete oldest without adding an extra key")
	}
	now = 60001
	before := emitted
	send(keys[50])
	if emitted != before {
		t.Fatal("same key update re-emitted early")
	}
	send(keys[0])
	if emitted != before+1 || len(n.progress) != 100 {
		t.Fatal("oldest key did not become eligible after eviction")
	}
}
