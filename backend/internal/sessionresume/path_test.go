package sessionresume

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNativePathBoundaryRetainsOriginalSurrogateAndRejectsInvalidStrings(t *testing.T) {
	for _, item := range []struct{ cwd, process, target, want string }{
		{"/fixture", "/fixture", `"/fixture"`, `""`},
		{"relative", "/fixture", `"relative/sub"`, `"sub"`},
		{"/fixture", "/fixture", `"child\ud800"`, `"child�"`},
		{"/fixture", "/private/fixture", `"sub"`, `"../private/fixture/sub"`},
	} {
		got, err := RelativePath(item.cwd, item.process, json.RawMessage(item.target))
		mustNative(t, err)
		if string(got) != item.want {
			t.Fatalf("path %s differs: %s", item.target, got)
		}
	}
	for _, raw := range []string{`null`, `3`, `{`, `[]`} {
		if _, err := RelativePath("/fixture", "/fixture", json.RawMessage(raw)); err == nil {
			t.Fatal("non-string path accepted")
		}
	}
	if _, err := RelativePath("/absolute", "relative", json.RawMessage(`"child"`)); err == nil {
		t.Fatal("inconsistent native path context accepted")
	}
	factory := testReducer(t).options.Factory
	marker := typeError("native path failure")
	observed := []error{}
	reducer, err := New(Options{Factory: factory, RelativePath: func(json.RawMessage) (json.RawMessage, error) { return nil, marker }, OnDiagnostic: func(Diagnostic) { t.Fatal("unexpected diagnostic") }, OnError: func(err error) { observed = append(observed, err) }})
	mustNative(t, err)
	history, err := ParseMessages([]json.RawMessage{json.RawMessage(`{"type":"attachment","attachment":{"type":"new_file","filename":"/private/fixture/one"}}`)})
	mustNative(t, err)
	if _, err := reducer.Deserialize(history); err != marker || len(observed) != 1 || observed[0] != marker {
		t.Fatal("path capability failure ownership lost")
	}
	reducer.options.RelativePath = func(json.RawMessage) (json.RawMessage, error) { return json.RawMessage(`{`), nil }
	if _, err := reducer.Deserialize(history); err == nil || !strings.Contains(err.Error(), "valid JSON") {
		t.Fatal("malformed path capability output accepted")
	}
}
