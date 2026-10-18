package insights

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestMeaningfulPromptUndefinedAndLiteralNoPromptRemainDistinct(t *testing.T) {
	withoutTags := func(string) (bool, error) { t.Fatal("plain content compiled builtin names"); return false, nil }
	value, err := FirstMeaningfulPrompt(nil, withoutTags)
	if err != nil || value != nil {
		t.Fatal("missing meaningful value must preserve prior memo", string(value), err)
	}
	user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("user")}, protocol.JSONField{Name: "message", Value: json.RawMessage(`{"content":"No prompt"}`)})
	value, err = FirstMeaningfulPrompt([]protocol.JSONObject{user}, withoutTags)
	if err != nil || string(value) != `"No prompt"` {
		t.Fatal("real human text confused with fallback", string(value), err)
	}
}

func TestMeaningfulPromptPropagatesActualBuiltinLookupFailure(t *testing.T) {
	failure := errors.New("compiled names unavailable")
	user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("user")}, protocol.JSONField{Name: "message", Value: json.RawMessage(`{"content":"<command-name>/settings</command-name><command-args>x</command-args>"}`)})
	calls := 0
	value, err := FirstMeaningfulPrompt([]protocol.JSONObject{user}, func(name string) (bool, error) {
		calls++
		if name != "settings" {
			t.Fatal(name)
		}
		return false, failure
	})
	if !errors.Is(err, failure) || value != nil || calls != 1 {
		t.Fatal("builtin failure was replaced with human text", string(value), calls, err)
	}
}
