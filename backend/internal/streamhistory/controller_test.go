package streamhistory

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// These complete observations are recorded by the real production query
// generator and normalizeMessagesForAPI; they are not Go-generated expected
// envelopes. Raw JSON lives in string fields, preserving property order,
// numeric spelling, UTF-16, and yield-time versus final mutation evidence.
func TestActualSourceStreamHistoryCorpus(t *testing.T) {
	raw, err := os.ReadFile("testdata/source_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Cases []struct {
			Name        string          `json:"name"`
			Observation json.RawMessage `json:"observation"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &corpus); err != nil || len(corpus.Cases) < 56 {
		t.Fatalf("missing actual Source corpus: cases=%d error=%v", len(corpus.Cases), err)
	}
	for _, fixture := range corpus.Cases {
		t.Run(fixture.Name, func(t *testing.T) {
			var expected fixtureObservation
			if err := json.Unmarshal(fixture.Observation, &expected); err != nil {
				t.Fatal(err)
			}
			model, nonInteractive := expected.Profile["model"].(string), expected.Profile["nonInteractive"].(bool)
			actual, err := observeFixture(fixtureInput{Wire: expected.Wire, Model: &model, NonInteractive: &nonInteractive})
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(actual)
			if err != nil {
				t.Fatal(err)
			}
			var want, got any
			if err := json.Unmarshal(fixture.Observation, &want); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(encoded, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("actual Source full-envelope contract diverged\nwant: %s\ngot:  %s", fixture.Observation, encoded)
			}
		})
	}
}

func newTestController(t *testing.T) (*Controller, *int, *int) {
	t.Helper()
	uuidCalls, timeCalls := new(int), new(int)
	controller, err := New(Options{UUID: func() string {
		*uuidCalls++
		return fmt.Sprintf("test-id-%d", *uuidCalls)
	}, Now: func() time.Time {
		*timeCalls++
		return time.UnixMilli(1700000000000)
	}})
	if err != nil {
		t.Fatal(err)
	}
	return controller, uuidCalls, timeCalls
}

const testStart = `{"type":"message_start","message":{"id":"provider","type":"message","model":"fixture","usage":{"input_tokens":7,"output_tokens":0},"content":[]}}`
const testTextStart = `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"ignored"}}`
const testTextDelta = `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"visible"}}`
const testTextStop = `{"type":"content_block_stop","index":0}`
const testTerminal = `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}`
const testStop = `{"type":"message_stop"}`
const testServerStart = `{"type":"content_block_start","index":1,"content_block":{"type":"server_tool_use","id":"server","name":"search"}}`
const testServerStop = `{"type":"content_block_stop","index":1}`

func feedAll(t *testing.T, controller *Controller, emit Emit, parts ...string) {
	t.Helper()
	for _, part := range parts {
		if err := controller.Feed(json.RawMessage(part), Timing{}, emit); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConsumerFailureClosesWithoutInventingRecovery(t *testing.T) {
	sentinel := errors.New("downstream stopped")
	for _, point := range []string{"raw_event", "server_drain", "limit_drain", "limit_feedback", "refusal_drain", "refusal_feedback", "finish_drain", "failure_drain", "failure_feedback"} {
		t.Run(point, func(t *testing.T) {
			controller, uuids, clocks := newTestController(t)
			accepted := []Update{}
			emit := func(update Update) error { accepted = append(accepted, update); return nil }
			failing := func(update Update) error {
				if (point == "limit_feedback" || point == "refusal_feedback") && update.Envelope != nil && !update.Envelope.APIError {
					accepted = append(accepted, update)
					return nil
				}
				return sentinel
			}
			var err error
			switch point {
			case "raw_event":
				err = controller.Feed(json.RawMessage(testStart), Timing{}, failing)
			case "server_drain":
				feedAll(t, controller, emit, testStart, testTextStart, testTextDelta, testTextStop, testServerStart)
				err = controller.Feed(json.RawMessage(testServerStop), Timing{}, failing)
			case "limit_drain", "limit_feedback":
				feedAll(t, controller, emit, testStart, testTextStart, testTextDelta, testTextStop)
				err = controller.Feed(json.RawMessage(`{"type":"message_delta","delta":{"stop_reason":"max_tokens"}}`), Timing{}, failing)
			case "refusal_drain", "refusal_feedback":
				controller.options.RefusalProfile = &RefusalProfile{Model: "fixture", NonInteractive: true}
				feedAll(t, controller, emit, testStart, testTextStart, testTextDelta, testTextStop)
				err = controller.Feed(json.RawMessage(`{"type":"message_delta","delta":{"stop_reason":"refusal"}}`), Timing{}, failing)
				if *uuids != 3 || *clocks != 2 {
					t.Fatalf("refusal feedback was not allocated before drain: UUID=%d clock=%d", *uuids, *clocks)
				}
			case "finish_drain":
				feedAll(t, controller, emit, testStart, testTextStart, testTextDelta, testTextStop, testTerminal, testStop)
				err = controller.Finish(nil, failing)
			case "failure_drain":
				feedAll(t, controller, emit, testStart, testTextStart, testTextDelta, testTextStop)
				err = controller.Finish(sourceError("Content block not found"), failing)
			case "failure_feedback":
				err = controller.Finish(nil, failing)
			}
			if !errors.Is(err, sentinel) {
				t.Fatalf("consumer error was replaced: %v", err)
			}
			beforeUUIDs, beforeClocks, beforeAccepted := *uuids, *clocks, len(accepted)
			if !errors.Is(controller.Feed(json.RawMessage(testStart), Timing{}, emit), ErrClosed) || !errors.Is(controller.Finish(nil, emit), ErrClosed) {
				t.Fatal("consumer failure did not close the controller")
			}
			if *uuids != beforeUUIDs || *clocks != beforeClocks || len(accepted) != beforeAccepted {
				t.Fatal("a closed controller allocated or emitted additional messages")
			}
		})
	}
}

func TestResolvedTimingOptionalRequestIDAndCompletedLifecycle(t *testing.T) {
	controller, uuids, clocks := newTestController(t)
	updates := []Update{}
	emit := func(update Update) error { updates = append(updates, update); return nil }
	if err := controller.Feed(json.RawMessage(testStart), Timing{TTFTMillis: 17}, emit); err != nil {
		t.Fatal(err)
	}
	feedAll(t, controller, emit, testTextStart, testTextDelta, testTextStop, testTerminal)
	if err := controller.Feed(json.RawMessage(testStop), Timing{DecodeMillis: 23}, emit); err != nil {
		t.Fatal(err)
	}
	if err := controller.Finish(nil, emit); err != nil {
		t.Fatal(err)
	}
	first, _ := updates[0].EventJSON()
	lastEvent, _ := updates[len(updates)-2].EventJSON()
	if !strings.HasSuffix(string(first), `,"ttftMs":17}`) || !strings.HasSuffix(string(lastEvent), `,"decodeMs":23}`) {
		t.Fatalf("resolved timing was replaced: start=%s stop=%s", first, lastEvent)
	}
	envelope := updates[len(updates)-1].Envelope
	raw, err := envelope.MarshalJSON()
	if err != nil || strings.Contains(string(raw), `"requestId"`) || *uuids != 1 || *clocks != 1 {
		t.Fatalf("optional request ID or allocation changed: %s %v UUID=%d clock=%d", raw, err, *uuids, *clocks)
	}
	if !errors.Is(controller.Finish(nil, emit), ErrClosed) {
		t.Fatal("completed controller may be reused")
	}
}

func TestUnsupportedDecodedInputAndTransportFailureAreExplicit(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("missing allocation sources accepted")
	}
	controller, _, _ := newTestController(t)
	if err := controller.Feed([]byte(testStart), Timing{}, nil); err == nil {
		t.Fatal("nil consumer accepted")
	}
	if err := controller.Finish(nil, nil); err == nil {
		t.Fatal("nil finish consumer accepted")
	}
	emit := func(Update) error { return nil }
	for _, raw := range []string{`{`, `null`, `[]`, `{"type":"message_start","message":null}`, `{"type":"content_block_start","index":-1,"content_block":{}}`, `{"type":"content_block_start","index":0.5,"content_block":{}}`, `{"type":"content_block_start","content_block":{}}`, `{"type":"content_block_start","index":0,"content_block":null}`, `{"type":"content_block_delta","index":-1}`, `{"type":"content_block_stop","index":false}`, `{"type":"message_delta","delta":null}`} {
		if err := controller.Feed([]byte(raw), Timing{}, emit); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("unsupported input was silently interpreted: %s -> %v", raw, err)
		}
	}
	feedAll(t, controller, emit, testStart, testTextStart)
	if err := controller.Feed([]byte(`{"type":"content_block_delta","index":0,"delta":[]}`), Timing{}, emit); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("non-object delta accepted: %v", err)
	}
	if err := controller.Feed([]byte(`{"type":"message_delta","delta":{"stop_reason":"refusal"}}`), Timing{}, emit); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("missing resolved refusal profile invented host defaults: %v", err)
	}
	updates := 0
	if err := controller.Finish(errors.New("transport reset"), func(Update) error { updates++; return nil }); !errors.Is(err, ErrUnsupported) || updates != 0 {
		t.Fatalf("unported transport recovery was invented: %v updates=%d", err, updates)
	}
}

func TestOrderedSerializationRejectsMutatedMalformedRawFields(t *testing.T) {
	controller, _, _ := newTestController(t)
	var envelope *Envelope
	emit := func(update Update) error {
		if update.Envelope != nil {
			envelope = update.Envelope
		}
		return nil
	}
	feedAll(t, controller, emit, testStart, testTextStart, testTextDelta, testTextStop, testTerminal, testStop)
	if err := controller.Finish(nil, emit); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"content", "message", "record", "usage"} {
		t.Run(target, func(t *testing.T) {
			copy := envelope.clone()
			switch target {
			case "content":
				copy.Content = []*Block{{Object: jsonObject(field("text", []byte(`{`)))}}
			case "message":
				copy.Message.Set("future", []byte(`{`))
			case "record":
				copy.Record.Set("future", []byte(`{`))
			case "usage":
				copy.Usage = &Usage{Raw: []byte(`{`)}
			}
			if _, err := copy.MarshalJSON(); err == nil {
				t.Fatal("mutable raw field wrote malformed JSON")
			}
		})
	}
	if _, err := (Update{Event: []byte(`{`)}).EventJSON(); err == nil {
		t.Fatal("malformed event serialized")
	}
}

func TestBoundedNormalizationRejectsBroaderHistoryAndInvalidText(t *testing.T) {
	user := object([]byte(`{"type":"user","message":{"role":"user","content":"question"}}`))
	for _, raw := range []string{`{"type":"assistant","message":{"content":"text"}}`, `{"type":"user","message":{"content":[]}}`, `{"type":"user","isVirtual":true,"message":{"content":"text"}}`} {
		if _, err := NormalizeTurn(object([]byte(raw)), nil); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("broader history accepted: %s %v", raw, err)
		}
	}
	envelope := func(id string, text json.RawMessage) *Envelope {
		return &Envelope{Message: jsonObject(field("id", literal(id))), Content: []*Block{{Object: jsonObject(field("type", literal("text")), field("text", text))}}}
	}
	if _, err := NormalizeTurn(user, []*Envelope{envelope("one", literal("first")), envelope("two", literal("second"))}); !errors.Is(err, ErrUnsupported) {
		t.Fatal("multiple provider responses silently merged")
	}
	if _, err := NormalizeTurn(user, []*Envelope{envelope("one", json.RawMessage(`null`))}); err == nil {
		t.Fatal("mutated non-string text silently normalized")
	}
	badUser := user.Clone()
	badUser.Set("future", []byte(`{`))
	if _, err := (HistoryMessage{User: &badUser}).MarshalJSON(); err == nil {
		t.Fatal("malformed raw user metadata silently normalized")
	}
	badUser.Set("message", []byte(`{`))
	if _, err := (HistoryMessage{User: &badUser}).MessageJSON(); err == nil {
		t.Fatal("malformed raw API user message silently serialized")
	}
}

func TestNullToolFragmentMutatesInputThenStopsBeforeEmission(t *testing.T) {
	for _, value := range []string{"null", "undefined"} {
		t.Run(value, func(t *testing.T) {
			controller, uuids, clocks := newTestController(t)
			updates := []Update{}
			emit := func(update Update) error { updates = append(updates, update); return nil }
			feedAll(t, controller, emit, testStart, `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"uncommitted","name":"fixture"}}`)
			fragment := `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta"}}
`
			if value == "null" {
				fragment = `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":null}}`
			}
			err := controller.Feed([]byte(fragment), Timing{}, emit)
			if err == nil || err.Error() != value+" is not an object (evaluating 'delta.partial_json.length')" {
				t.Fatalf("Source TypeError feedback changed: %v", err)
			}
			if controller.blocks[0].Object.String("input") != value || len(updates) != 2 || *uuids != 0 || *clocks != 0 {
				t.Fatal("fragment must mutate input, then fail before raw event or tool envelope allocation")
			}
			if err := controller.Finish(err, emit); err != nil {
				t.Fatal(err)
			}
			if len(updates) != 3 || updates[2].Envelope == nil || !updates[2].Envelope.APIError || *uuids != 2 || *clocks != 1 {
				t.Fatal("failed fragment emitted an executable tool or changed synthetic allocation")
			}
		})
	}
}

func TestUsageCoercionFailureIsAtomicBeforeLastEnvelopeMutation(t *testing.T) {
	controller, uuids, _ := newTestController(t)
	updates := []Update{}
	emit := func(update Update) error { updates = append(updates, update); return nil }
	feedAll(t, controller, emit, testStart, testTextStart, testTextDelta, testTextStop, testServerStart, testServerStop)
	previousUsage := controller.usage
	last := controller.created[len(controller.created)-1]
	before, err := last.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	beforeUpdates := len(updates)
	err = controller.Feed([]byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":"must not appear"},"usage":{"input_tokens":"11","cache_creation_input_tokens":{"toString":null},"output_tokens":99}}`), Timing{}, emit)
	if err == nil || err.Error() != "No default value" {
		t.Fatalf("Source object-to-number TypeError feedback changed: %v", err)
	}
	after, encodeErr := last.MarshalJSON()
	if encodeErr != nil || string(after) != string(before) || controller.usage != previousUsage || len(updates) != beforeUpdates || *uuids != 2 {
		t.Fatal("failed usage projection partially assigned usage/stop fields or emitted its event")
	}
	if err := controller.Finish(err, emit); err != nil {
		t.Fatal(err)
	}
	if len(updates) != beforeUpdates+1 || *uuids != 4 {
		t.Fatal("usage TypeError feedback lost the already-yielded provider envelopes")
	}
}

func TestCallerAbortLifecycleAndConsumerFailure(t *testing.T) {
	t.Run("empty and closed", func(t *testing.T) {
		controller, ids, clocks := newTestController(t)
		if err := controller.Abort(nil); err == nil {
			t.Fatal("nil consumer accepted")
		}
		updates := 0
		emit := func(Update) error { updates++; return nil }
		if err := controller.Abort(emit); err != nil {
			t.Fatal(err)
		}
		if updates != 0 || *ids != 0 || *clocks != 0 {
			t.Fatal("empty caller abort invented feedback")
		}
		if !errors.Is(controller.Abort(emit), ErrClosed) || !errors.Is(controller.Finish(nil, emit), ErrClosed) || !errors.Is(controller.Feed(json.RawMessage(testStart), Timing{}, emit), ErrClosed) {
			t.Fatal("aborted controller reused")
		}
	})
	t.Run("partial consumer owns failure", func(t *testing.T) {
		controller, ids, clocks := newTestController(t)
		emit := func(Update) error { return nil }
		feedAll(t, controller, emit, testStart, testTextStart, testTextDelta)
		original := errors.New("consumer stopped on preserved partial")
		if err := controller.Abort(func(update Update) error {
			if update.Envelope == nil || update.Envelope.APIError || update.Envelope.Content[0].Object.String("text") != "visible" {
				t.Fatal("caller abort lost visible text")
			}
			return original
		}); err != original {
			t.Fatalf("consumer failure replaced: %v", err)
		}
		if *ids != 1 || *clocks != 1 || !errors.Is(controller.Abort(emit), ErrClosed) {
			t.Fatal("abort failure advanced lifecycle")
		}
	})
}
