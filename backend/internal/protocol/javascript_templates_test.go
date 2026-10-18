package protocol

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestJavaScriptTemplatesRetainRawPropertyIdentityAndInsertedText(t *testing.T) {
	var values JSONObject
	if err := values.UnmarshalJSON([]byte(`{"\ud800":"\udc00\u0000${user_config.next}","�":"wrong","next":"wrong"}`)); err != nil {
		t.Fatal(err)
	}
	calls := []string{}
	got, err := ReplaceJavaScriptTemplatesJSON(json.RawMessage(`"before ${user_config.\ud800} / ${user_config.absent} / ${user_config.} after"`), "${user_config.", func(key json.RawMessage) (json.RawMessage, error) {
		calls = append(calls, string(key))
		return values.GetPropertyJSON(key)
	})
	if err != nil || string(got) != `"before \udc00\u0000${user_config.next} / ${user_config.absent} / ${user_config.} after"` || !reflect.DeepEqual(calls, []string{`"\ud800"`, `"absent"`}) {
		t.Fatal(string(got), calls, err)
	}
	copy, err := values.GetPropertyJSON(json.RawMessage(`"\ud800"`))
	if err != nil {
		t.Fatal(err)
	}
	copy[1] = 'x'
	retained, err := values.GetPropertyJSON(json.RawMessage(`"\ud800"`))
	if err != nil || string(retained) != `"\udc00\u0000${user_config.next}"` {
		t.Fatal("returned value changed original property", string(retained), err)
	}
	if _, err := values.GetPropertyJSON(json.RawMessage(`3`)); err == nil {
		t.Fatal("non-string property identity accepted")
	}
}

func TestJavaScriptLiteralReplacementIsLazyAndDollarPatternsStayLiteral(t *testing.T) {
	calls := 0
	replace := func() (json.RawMessage, error) {
		calls++
		return json.RawMessage(`"$&$$$'\ud800${CLAUDE_PLUGIN_ROOT}"`), nil
	}
	got, err := ReplaceJavaScriptLiteralJSON(json.RawMessage(`"${outer ${CLAUDE_PLUGIN_ROOT}} / ${CLAUDE_PLUGIN_ROOT}"`), "${CLAUDE_PLUGIN_ROOT}", replace)
	if err != nil || string(got) != `"${outer $&$$$'\ud800${CLAUDE_PLUGIN_ROOT}} / $&$$$'\ud800${CLAUDE_PLUGIN_ROOT}"` || calls != 2 {
		t.Fatal(string(got), calls, err)
	}
	if _, err := ReplaceJavaScriptLiteralJSON(json.RawMessage(`"untouched"`), "${CLAUDE_PLUGIN_ROOT}", replace); err != nil || calls != 2 {
		t.Fatal("absent reference invoked directory producer", calls, err)
	}
}

func TestJavaScriptReplacementStringUsesOriginalPrefixAndSuffix(t *testing.T) {
	got, err := ReplaceJavaScriptStringJSON(json.RawMessage(`"A\ud800XBX\udc00C"`), "X", json.RawMessage("\"$$:$&:$`:$':$1\""))
	want := `"A\ud800$:X:A\ud800:BX\udc00C:$1B$:X:A\ud800XB:\udc00C:$1\udc00C"`
	if err != nil || string(got) != want {
		t.Fatal(string(got), err)
	}
}

func TestJavaScriptTemplateBoundariesPropagateFailures(t *testing.T) {
	failure := errors.New("owned producer failed")
	for _, literal := range []bool{false, true} {
		replace := func(json.RawMessage) (json.RawMessage, error) { return nil, failure }
		var err error
		if literal {
			_, err = ReplaceJavaScriptLiteralJSON(json.RawMessage(`"${x}"`), "${x}", func() (json.RawMessage, error) { return replace(nil) })
		} else {
			_, err = ReplaceJavaScriptTemplatesJSON(json.RawMessage(`"${x}"`), "${", replace)
		}
		if !errors.Is(err, failure) {
			t.Fatal("producer failure lost", literal, err)
		}
	}
	if _, err := ReplaceJavaScriptTemplatesJSON(json.RawMessage(`false`), "${", func(json.RawMessage) (json.RawMessage, error) {
		t.Fatal("invalid input invoked producer")
		return nil, nil
	}); err == nil {
		t.Fatal("invalid input accepted")
	}
	if _, err := ReplaceJavaScriptTemplatesJSON(json.RawMessage(`"${x}"`), "${", func(json.RawMessage) (json.RawMessage, error) { return json.RawMessage(`{}`), nil }); err == nil {
		t.Fatal("non-string replacement accepted")
	}
}
