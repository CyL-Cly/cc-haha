package requestmapping

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func value(t *testing.T, source string) *Value {
	t.Helper()
	parsed, err := ParseValue(json.RawMessage(source))
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
func wire(t *testing.T, input *Value) string {
	t.Helper()
	raw, err := input.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
func control() Options {
	return Options{CacheControl: func(*string) (json.RawMessage, error) { return json.RawMessage(`{"type":"ephemeral"}`), nil }}
}

func TestUserNoncacheClonesArrayButAssistantSharesIt(t *testing.T) {
	source := value(t, `{"message":{"content":[{"type":"text","text":"before","nested":{"value":"shared"}}]}}`)
	original := source.Property("message").Property("content")
	user, err := MapUserMessage(source, false, true, Options{})
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := MapAssistantMessage(source, false, true, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if user.Content == original || assistant.Content != original || user.Content.Element(0) != original.Element(0) {
		t.Fatal("source array identity rules changed")
	}
	user.Content.Append(value(t, `{"type":"text","text":"only user array"}`))
	if length, _ := original.ArrayLength(); length != 1 {
		t.Fatal("user array append contaminated history")
	}
	user.Content.Element(0).SetProperty("text", value(t, `"shared block update"`))
	if wire(t, assistant.Content.Element(0).Property("text")) != `"shared block update"` {
		t.Fatal("shallow cloning should retain block object")
	}
	user.Content.SetElement(0, value(t, `{"type":"text","text":"replacement"}`))
	if user.Content.Element(0) == original.Element(0) {
		t.Fatal("array-slot replacement should remain isolated")
	}
	assistant.Content.Append(value(t, `{"type":"text","text":"shared append"}`))
	if length, _ := original.ArrayLength(); length != 2 {
		t.Fatal("assistant noncache content shares the actual array")
	}
	for _, mapper := range []func(*Value, bool, bool, Options) (*MessageParam, error){MapUserMessage, MapAssistantMessage} {
		mapped, err := mapper(value(t, `{"message":{}}`), false, false, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if mapped.Content != nil || strings.Contains(wire(t, mapped.Value()), "content") {
			t.Fatal("missing content must remain missing")
		}
	}
}

func TestCacheMappingCopiesBlocksAndKeepsNestedReferencesAndPropertyOrder(t *testing.T) {
	source := value(t, `{"message":{"content":[{"cache_control":{"old":true},"type":"text","text":"before","nested":{"value":"shared"}}]}}`)
	original := source.Property("message").Property("content")
	for _, mapper := range []func(*Value, bool, bool, Options) (*MessageParam, error){MapUserMessage, MapAssistantMessage} {
		mapped, err := mapper(source, true, true, control())
		if err != nil {
			t.Fatal(err)
		}
		block := mapped.Content.Element(0)
		if mapped.Content == original || block == original.Element(0) || block.Property("nested") != original.Element(0).Property("nested") {
			t.Fatal("cache mapping should shallow-clone array and each block")
		}
		if !strings.HasPrefix(wire(t, block), `{"cache_control":{"type":"ephemeral"},"type":"text"`) {
			t.Fatal("overwriting an existing cache_control should retain its property position")
		}
		block.SetProperty("text", value(t, `"isolated"`))
		if wire(t, original.Element(0).Property("text")) != `"before"` {
			t.Fatal("cloned block property contaminated history")
		}
		block.Property("nested").SetProperty("value", value(t, `"shared change"`))
		if wire(t, original.Element(0).Property("nested").Property("value")) != `"shared change"` {
			t.Fatal("nested object identity should remain shared")
		}
	}
}

func TestCacheStringEmptyArraysThinkingAndPrimitiveSpread(t *testing.T) {
	options := control()
	for _, mapper := range []func(*Value, bool, bool, Options) (*MessageParam, error){MapUserMessage, MapAssistantMessage} {
		mapped, err := mapper(value(t, `{"message":{"content":""}}`), true, false, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if wire(t, mapped.Content) != `[{"type":"text","text":""}]` {
			t.Fatal("addCache wraps even empty strings when caching is off")
		}
		mapped, err = mapper(value(t, `{"message":{"content":[]}}`), true, true, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if wire(t, mapped.Content) != `[]` {
			t.Fatal("empty content has no factory invocation")
		}
	}
	for _, kind := range []string{"thinking", "redacted_thinking"} {
		mapped, err := MapAssistantMessage(value(t, `{"message":{"content":[{"type":"`+kind+`","cache_control":{"old":true}}]}}`), true, true, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if wire(t, mapped.Content.Element(0).Property("cache_control")) != `{"old":true}` {
			t.Fatal("thinking exclusions must preserve existing controls")
		}
	}
	mapped, err := MapAssistantMessage(value(t, `{"message":{"content":[{"type":"connector_text","text":"release feature disabled"}]}}`), true, true, options)
	if err != nil {
		t.Fatal(err)
	}
	if mapped.Content.Element(0).Property("cache_control") == nil {
		t.Fatal("CONNECTOR_TEXT is disabled in the default release")
	}
	mapped, err = MapUserMessage(value(t, `{"message":{"content":[null,false,12,["nested",false],"🙂\ud800"]}}`), true, true, options)
	if err != nil {
		t.Fatal(err)
	}
	expected := `[{}, {}, {}, {"0":"nested","1":false}, {"0":"\ud83d","1":"\ude42","2":"\ud800","cache_control":{"type":"ephemeral"}}]`
	expected = strings.ReplaceAll(expected, ", ", ",")
	if actual := wire(t, mapped.Content); actual != expected {
		t.Fatalf("Object spread UTF-16/array rules changed: %s", actual)
	}
	mapped, err = MapUserMessage(value(t, `{"message":{"content":[null]}}`), true, true, options)
	if err != nil {
		t.Fatal(err)
	}
	if wire(t, mapped.Content) != `[{"cache_control":{"type":"ephemeral"}}]` {
		t.Fatal("user null block spreads into an object")
	}
	mapped, err = MapAssistantMessage(value(t, `{"message":{"content":[false]}}`), true, true, options)
	if err != nil || wire(t, mapped.Content) != `[{"cache_control":{"type":"ephemeral"}}]` {
		t.Fatal("non-null primitive type property is undefined, so it is cacheable")
	}
}

func TestBreakpointsFactoryOrderAndShiftedMarker(t *testing.T) {
	source := []*Value{value(t, `{"type":"user","message":{"content":"one"}}`), value(t, `{"type":"assistant","message":{"content":[{"type":"text","text":"two"}]}}`), value(t, `{"type":"user","message":{"content":"three"}}`)}
	querySource := "sdk"
	for _, skip := range []bool{false, true} {
		calls := []string{}
		options := Options{QuerySource: &querySource, SkipCacheWrite: skip, OnEvent: func(event BreakpointEvent) {
			if event.TotalMessageCount != 3 || !event.CachingEnabled || event.SkipCacheWrite != skip {
				t.Fatal("telemetry changed")
			}
			calls = append(calls, "event")
		}, CacheControl: func(source *string) (json.RawMessage, error) {
			if source != &querySource {
				t.Fatal("query source seam changed")
			}
			calls = append(calls, "factory")
			return json.RawMessage(`{"type":"ephemeral","ttl":"1h"}`), nil
		}}
		mapped, err := AddCacheBreakpoints(source, true, options)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(calls, ",") != "event,factory" {
			t.Fatalf("telemetry must precede factory: %v", calls)
		}
		marker := 2
		if skip {
			marker = 1
		}
		for index, message := range mapped {
			_, array := message.Content.ArrayLength()
			marked := array && message.Content.Element(0).Property("cache_control") != nil
			if marked != (index == marker) {
				t.Fatalf("marker at wrong index: %d", index)
			}
		}
	}
	for _, input := range [][]*Value{nil, {source[0]}} {
		mapped, err := AddCacheBreakpoints(input, true, Options{SkipCacheWrite: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(mapped) != len(input) {
			t.Fatal("empty/short fork should retain message count without a marker")
		}
	}
	mapped, err := AddCacheBreakpoints([]*Value{value(t, `{"type":"future","message":{"content":"fallback"}}`)}, true, control())
	if err != nil || mapped[0].Role != "assistant" {
		t.Fatal("all nonuser types follow the original assistant fallback")
	}
}

func TestMalformedInputsKeepSourceErrorsAndEventOrdering(t *testing.T) {
	for _, test := range []struct {
		source    string
		assistant bool
		fragment  string
	}{
		{`null`, false, "evaluating 'message.message'"}, {`{}`, false, "undefined is not an object"}, {`{"message":null}`, false, "evaluating 'message.message.content'"}, {`{"message":{}}`, false, "evaluating 'message.message.content.map'"}, {`{"message":{"content":null}}`, false, "null is not an object"}, {`{"message":{"content":false}}`, false, "'message.message.content.map' is undefined"}, {`{"message":{"content":[null]}}`, true, "evaluating '_.type'"},
		{`{"message":{"content":{"map":null}}}`, false, "is null)"}, {`{"message":{"content":{"map":{}}}}`, false, "is an instance of Object)"}, {`{"message":{"content":{"map":[]}}}`, true, "is an instance of Array)"}, {`{"message":{"content":{"map":false}}}`, false, "is false)"}, {`{"message":{"content":{"map":1e400}}}`, true, "is Infinity)"},
	} {
		var err error
		if test.assistant {
			_, err = MapAssistantMessage(value(t, test.source), true, false, Options{})
		} else {
			_, err = MapUserMessage(value(t, test.source), true, false, Options{})
		}
		var failure *MappingError
		if !errors.As(err, &failure) || failure.Name != "TypeError" || !strings.Contains(failure.Error(), test.fragment) {
			t.Fatalf("source error changed: %v", err)
		}
		if !json.Valid(failure.MessageJSON()) {
			t.Fatal("error feedback must remain a quoted JSON string")
		}
	}
	_, err := MapUserMessage(nil, false, false, Options{})
	if err == nil || !strings.Contains(err.Error(), "undefined is not an object") {
		t.Fatal("missing envelope differs from null")
	}
	calls := []string{}
	_, err = AddCacheBreakpoints([]*Value{value(t, `null`)}, true, Options{OnEvent: func(BreakpointEvent) { calls = append(calls, "event") }})
	if err == nil || !strings.Contains(err.Error(), "evaluating 'msg.type'") || strings.Join(calls, ",") != "event" {
		t.Fatal("event must be emitted before malformed-envelope failure")
	}
	calls = nil
	_, err = AddCacheBreakpoints([]*Value{value(t, `{"type":"user","message":{"content":"fork marker"}}`), value(t, `{"type":"assistant","message":null}`)}, true, Options{SkipCacheWrite: true, OnEvent: func(BreakpointEvent) { calls = append(calls, "event") }, CacheControl: func(*string) (json.RawMessage, error) {
		calls = append(calls, "factory")
		return json.RawMessage(`{"type":"ephemeral"}`), nil
	}})
	if err == nil || strings.Join(calls, ",") != "event,factory" {
		t.Fatal("earlier marker effects must survive a later malformed-message failure")
	}
	_, err = MapUserMessage(value(t, `{"message":{"content":{"map":"\ud800"}}}`), true, false, Options{})
	var failure *MappingError
	if !errors.As(err, &failure) {
		t.Fatal(err)
	}
	if !strings.Contains(string(failure.MessageJSON()), `\ud800`) || !strings.Contains(failure.Message, "�") || strings.Contains(string(failure.MessageJSON()), `\\ud800`) {
		t.Fatal("lone UTF-16 error feedback must remain a code unit, with UTF-8 replacement only for display")
	}
}

func TestFactoryIsExplicitAndFailuresPropagateWithoutHistoryMutation(t *testing.T) {
	source := value(t, `{"message":{"content":"literal"}}`)
	if _, err := MapUserMessage(source, true, true, Options{}); err == nil {
		t.Fatal("mapping must not invent a TTL/default factory")
	}
	sentinel := errors.New("fixture factory failed")
	for _, content := range []string{`"literal"`, `[{"type":"text","text":"last"}]`} {
		source = value(t, `{"message":{"content":`+content+`}}`)
		before := wire(t, source)
		for _, mapper := range []func(*Value, bool, bool, Options) (*MessageParam, error){MapUserMessage, MapAssistantMessage} {
			_, err := mapper(source, true, true, Options{CacheControl: func(*string) (json.RawMessage, error) { return nil, sentinel }})
			if !errors.Is(err, sentinel) || wire(t, source) != before {
				t.Fatal("factory failure should propagate without modifying history")
			}
		}
	}
	if _, err := MapAssistantMessage(source, true, true, Options{CacheControl: func(*string) (json.RawMessage, error) { return json.RawMessage(`invalid`), nil }}); err == nil {
		t.Fatal("factory's invalid JSON must be surfaced")
	}
}
