package toolfeedback

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func object(raw string) storage.Object {
	value, _, err := storage.NormalizeObject(json.RawMessage(raw))
	if err != nil {
		panic(err)
	}
	return value
}
func factory(t *testing.T) *messages.Factory {
	t.Helper()
	sequence := 0
	value, err := messages.NewFactory(func() string { sequence++; return fmt.Sprintf("fixture-id-%d", sequence) }, func() time.Time { return time.UnixMilli(1700000000000) })
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestUnparsedMarkerIsExactReservedShape(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{}`, `42`, `"raw"`, `{"__unparsedToolInput":null}`, `{"__unparsedToolInput":"bad"}`, `{"__unparsedToolInput":{"raw":"bad","len":"3"}}`, `{"__unparsedToolInput":{"raw":null,"len":3}}`, `{"__unparsedToolInput":{"raw":"bad","len":null}}`, `{"__unparsedToolInput":{"raw":"bad","len":3},"legitimate":true}`, `not JSON`} {
		if IsUnparsedToolInput(json.RawMessage(raw)) {
			t.Fatal("ordinary input was rejected as marker", raw)
		}
		if value, handled, err := InvalidInput(factory(t), object(`{"id":"call","name":"fixture","input":`+validOrNull(raw)+`}`), text("assistant")); err != nil || handled || value != nil {
			t.Fatal(value, handled, err)
		}
	}
	for _, raw := range []string{`{"__unparsedToolInput":{"raw":"","len":0}}`, `{"__unparsedToolInput":{"raw":"bad","len":3,"future":true}}`, `{"__unparsedToolInput":{"raw":"bad","len":1.5}}`, `{"__unparsedToolInput":{"raw":"bad","len":1e400}}`} {
		if !IsUnparsedToolInput(json.RawMessage(raw)) {
			t.Fatal(raw)
		}
	}
}
func validOrNull(raw string) string {
	if json.Valid([]byte(raw)) {
		return raw
	}
	return "null"
}

func TestInvalidFeedbackPreservesBoundedUTF16AndAssistantIdentity(t *testing.T) {
	raw := text(strings.Repeat("x", 199) + "😀" + strings.Repeat("z", 3000))
	input, err := MarkerFromJSONText(raw)
	if err != nil {
		t.Fatal(err)
	}
	use := storage.Object{"id": text("call"), "name": json.RawMessage(`"bad\udfff<&>"`), "input": input}
	message, handled, err := InvalidInput(factory(t), use, text("assistant-source"))
	if err != nil || !handled {
		t.Fatal(handled, err)
	}
	api := object(string(message["message"]))
	var blocks []storage.Object
	_ = json.Unmarshal(api["content"], &blocks)
	if len(blocks) != 1 || string(blocks[0]["is_error"]) != "true" || string(blocks[0]["tool_use_id"]) != `"call"` || string(message["sourceToolAssistantUUID"]) != `"assistant-source"` {
		t.Fatal(message)
	}
	// Compare well-formed JS string content, independent of JSON's equivalent
	// HTML escape spellings in intermediate Go envelopes.
	units, err := protocol.DecodeJavaScriptJSONString(blocks[0]["content"])
	if err != nil {
		t.Fatal(err)
	}
	content := string(protocol.QuoteJavaScriptUTF16(units))
	if !strings.Contains(content, `bad\udfff<&>`) || !strings.Contains(content, "first 200 of 3201 bytes") || !strings.Contains(content, `\ud83d`) || strings.Contains(content, "zz") || string(message["toolUseResult"]) != `"InputValidationError: JSON parse failed (3201 bytes)"` {
		t.Fatal(content, message["toolUseResult"])
	}
	if _, _, err := InvalidInput(nil, use, text("assistant")); err == nil {
		t.Fatal("missing message factory accepted")
	}
	if _, err := MarkerFromJSONText(json.RawMessage(`{}`)); err == nil {
		t.Fatal("invalid marker source accepted")
	}
}

func TestUnknownToolFeedbackDoesNotAlterModelVisibleName(t *testing.T) {
	use := object(`{"id":"call","name":"unknown\ud800<&>"}`)
	message, err := UnknownTool(factory(t), use, text("assistant-source"))
	if err != nil {
		t.Fatal(err)
	}
	if string(message["toolUseResult"]) != `"Error: No such tool available: unknown\ud800<&>"` {
		t.Fatal(string(message["toolUseResult"]))
	}
	if _, err := UnknownTool(nil, use, text("assistant-source")); err == nil {
		t.Fatal("missing factory accepted")
	}
}
