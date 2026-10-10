package sdkoutput

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type cursorResult struct {
	Value *View `json:"value,omitempty"`
	Done  bool  `json:"done"`
}

func originalProgressData(t *testing.T, message Message) protocol.JSONObject {
	t.Helper()
	var data protocol.JSONObject
	if err := data.UnmarshalJSON(message.Record.Get("data")); err != nil {
		t.Fatal(err)
	}
	return data
}

func cursorNext(t *testing.T, cursor *MessageIterator) cursorResult {
	t.Helper()
	view, done, err := cursor.Next()
	if err != nil {
		t.Fatal(err)
	}
	return cursorResult{view, done}
}

func cursorJSON(t *testing.T, value any) any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result any
	if err = json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

// The expected values come from the unchanged Source sync export. Original
// messages are parsed independently; progress borrows an original nested
// producer, never normalized or rendered data. A raw JSON-only progress codec
// has no later nested-reference mutation contract.
func TestMessageIteratorActualSourceLiveCursor(t *testing.T) {
	raw, err := os.ReadFile("testdata/cursor_source_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Input struct {
			Profile string
			Message json.RawMessage
		}
		Source map[string]any
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, item := range cases {
		t.Run(item.Input.Profile, func(t *testing.T) {
			n, _ := fixture(t)
			session, getterCalls := "00000000-0000-4000-0000-000000000022", 0
			n.deps.SessionID = func() json.RawMessage { getterCalls++; return quote(session) }
			message := original(t, string(item.Input.Message))
			producer := &message
			if item.Input.Profile == "progress-live-parent" {
				data := originalProgressData(t, message)
				nested := original(t, string(data.Get("message")))
				message.Nested, producer = &nested, &nested
			}
			cursor := n.IterateMessage(message)
			if getterCalls != 0 {
				t.Fatal("generator constructor executed its body")
			}
			first := cursorNext(t, cursor)
			firstAtYield := cursorJSON(t, first)
			session = "00000000-0000-4000-0000-000000000033"
			producer.Content[0].Object.Set("text", quote("mutated first block"))
			producer.Content[1].Object.Set("text", quote("newly nonempty second block"))
			producer.API.Set("usage", json.RawMessage(`{"input_tokens":99,"output_tokens":88}`))
			message.Record.Set("parentToolUseID", quote("new-parent"))
			var second cursorResult
			if item.Input.Profile == "return-before-second" {
				cursor.Return()
				second = cursorResult{Done: true}
			} else {
				second = cursorNext(t, cursor)
			}
			done := cursorNext(t, cursor)
			actual := map[string]any{"firstAtYield": firstAtYield, "firstLater": first, "second": second, "done": done}
			if result := cursorJSON(t, actual); !reflect.DeepEqual(result, item.Source) {
				t.Fatalf("original Source live cursor differs\nactual=%v\nSource=%v", result, item.Source)
			}
			wantGetters := 2
			if item.Input.Profile == "return-before-second" {
				wantGetters = 1
			}
			if getterCalls != wantGetters {
				t.Fatalf("per-yield session reads=%d want=%d", getterCalls, wantGetters)
			}
		})
	}
}

func TestMessageIteratorConstructorReturnAndThrownNext(t *testing.T) {
	n, calls := fixture(t)
	message := original(t, `{"type":"user","uuid":"original","message":{"content":[{"type":"text","text":"one"},{"type":"text","text":"two"}]}}`)
	cursor := n.IterateMessage(message)
	cursor.Return()
	if result := cursorNext(t, cursor); !result.Done || *calls != 0 {
		t.Fatal("return before first Next executed normalization")
	}
	cursor = n.IterateMessage(message)
	if result := cursorNext(t, cursor); result.Done || *calls != 4 {
		t.Fatalf("first Next did not eagerly allocate original user blocks: %v %d", result, *calls)
	}
	cursor.Return()
	if result := cursorNext(t, cursor); !result.Done || *calls != 4 {
		t.Fatal("closed cursor evaluated later body")
	}
	bad := n.IterateMessage(original(t, `{"type":"assistant","message":{"content":[{"type":"text","text":3}]}}`))
	if _, _, err := bad.Next(); err == nil {
		t.Fatal("original thrown Next was accepted")
	}
	if result := cursorNext(t, bad); !result.Done {
		t.Fatal("throw did not close generator")
	}
}
