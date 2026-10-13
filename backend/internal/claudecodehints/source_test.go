package claudecodehints

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func sourceHints(t *testing.T, input any) protocol.JSONObject {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	tmp := filepath.Join(home, "tmp")
	_ = os.MkdirAll(tmp, 0700)
	data, _ := json.Marshal(input)
	var value protocol.JSONObject
	_ = value.UnmarshalJSON(data)
	value.Set("home", json.RawMessage(strconvQuote(home)))
	data, _ = value.MarshalJSON()
	fixture, _ := filepath.Abs("../../testdata/native-shell-hints.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + tmp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SHELL_HINT_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(data)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.4000s", e, out)
	}
	if e = value.UnmarshalJSON(out); e != nil {
		t.Fatalf("Source JSON %v %.4000s", e, out)
	}
	return value
}
func strconvQuote(v string) string { raw, _ := json.Marshal(v); return string(raw) }
func canonical(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()
	return protocol.StringifyValidatedJavaScriptJSON(raw)
}
func TestActualSourceExtractionWholeLinesAttributesUTF16AndWhitespace(t *testing.T) {
	rows := []json.RawMessage{}
	outputs := []string{`"plain\n\n\n"`, `"<claude-code-hint v=1 type=plugin value=p@agent-skills/>"`, `"\n<claude-code-hint v=2 type=plugin value=x/>\n\n\nend"`, `"before <claude-code-hint v=1 type=plugin value=x/>"`, `"<claude-code-hint v=0x1 type=plugin value=first value=last/>"`, `"<claude-code-hint v=0b1 type=plugin value=\"two words\"/>"`, `"<claude-code-hint v=1 type=plugin value=\"\"/>"`, `"a\r\n<claude-code-hint v=1 type=plugin value=x/>\r\nb"`, `"a\r<claude-code-hint v=1 type=plugin value=x/>\rb"`, `"a\u2028<claude-code-hint\u00a0v=1 type=plugin value=\"\ud800\"/>\u2029b"`, `"<claude-code-hint v=1\ntype=plugin value=x/>"`, `"<claude-code-hint v=\" 1 \" type=plugin value=x/>"`, `"<claude-code-hint v=1 type=other value=x/>"`, `"<claude-code-hint v=1 TYPE=plugin value=x/>"`, `"<claude-code-hint v=1 type=plugin value=\"a>b\"/>"`}
	commands := []string{`"\t\ufeffvendor run"`, `"'two words' --flag"`, `"cmd\udc00\u2000arg"`, `""`}
	for _, output := range outputs {
		for _, command := range commands {
			rows = append(rows, json.RawMessage(`{"output":`+output+`,"command":`+command+`}`))
		}
	}
	source := sourceHints(t, map[string]any{"rows": rows})
	var expected []json.RawMessage
	_ = json.Unmarshal(source.Get("rows"), &expected)
	for i, row := range rows {
		var in protocol.JSONObject
		_ = in.UnmarshalJSON(row)
		got, e := Extract(in.Get("output"), in.Get("command"))
		if e != nil {
			t.Fatal(e)
		}
		data, _ := json.Marshal(got)
		if !bytes.Equal(canonical(t, data), canonical(t, expected[i])) {
			t.Fatalf("row %d input %s\nGo %s\nSource %s", i, row, data, expected[i])
		}
	}
}

func TestActualSourceStripEmptyLinesKeepsNonemptyLinesRaw(t *testing.T) {
	rows := []json.RawMessage{json.RawMessage(`"\n \t\n  leading  \n\t\n"`), json.RawMessage(`"tabs\t\t"`), json.RawMessage(`"\ud800 \t\nsecond\udc00  "`), json.RawMessage(`"\ufeff\n\u0085\n\u2000"`), json.RawMessage(`"\r\n\t \r\n"`)}
	source := sourceHints(t, map[string]any{"stripRows": rows})
	var want []json.RawMessage
	_ = json.Unmarshal(source.Get("strippedRows"), &want)
	for i, row := range rows {
		got, e := StripEmptyLines(row)
		if e != nil {
			t.Fatal(e)
		}
		if !bytes.Equal(canonical(t, got), canonical(t, want[i])) {
			t.Fatalf("Source strip row%d Go %s Source %s", i, got, want[i])
		}
	}
}

func TestActualSourceHintStoreAndOldGlobalResponsePersistence(t *testing.T) {
	global := protocol.NewJSONObject()
	_ = global.UnmarshalJSON([]byte(`{"future":{"keep":"\ud800"},"claudeCodeHints":{"futureFlag":17,"plugin":["old@agent-skills"]}}`))
	hint := func(id string) Hint {
		return Hint{1, "plugin", json.RawMessage(strconvQuote(id)), json.RawMessage(`"vendor"`)}
	}
	actions := []map[string]any{}
	for _, id := range []string{"no-market", "@agent-skills", "bad@other", "old@agent-skills", "installed@agent-skills", "policy@agent-skills", "first@agent-skills", "second@AGENT-SKILLS@ignored", "second@AGENT-SKILLS@ignored"} {
		a := map[string]any{"kind": "record", "enabled": true, "hint": hint(id)}
		if id == "installed@agent-skills" {
			a["installed"] = []string{id}
		}
		if id == "policy@agent-skills" {
			a["blocked"] = []string{id}
		}
		actions = append(actions, a)
	}
	actions = append(actions, map[string]any{"kind": "clear"}, map[string]any{"kind": "record", "hint": hint("first@agent-skills")}, map[string]any{"kind": "record", "hint": hint("third@agent-skills")}, map[string]any{"kind": "shown"}, map[string]any{"kind": "record", "hint": hint("fourth@agent-skills")}, map[string]any{"kind": "response", "id": "third@agent-skills", "disable": true}, map[string]any{"kind": "response", "id": "third@agent-skills", "disable": true})
	source := sourceHints(t, map[string]any{"global": global, "actions": actions})
	var states []protocol.JSONObject
	_ = json.Unmarshal(source.Get("states"), &states)
	store := Store{}
	for i, a := range actions {
		switch a["kind"] {
		case "record":
			h := a["hint"].(Hint)
			gate := StateFromGlobal(global)
			gate.Enabled = true
			_, gate.Installed = a["installed"]
			_, gate.Blocked = a["blocked"]
			store.Record(h, gate)
		case "clear":
			_, sequence := store.Pending()
			store.Clear(sequence)
		case "shown":
			store.MarkShown()
		case "response":
			global = Respond(global, json.RawMessage(strconvQuote(a["id"].(string))), true)
		}
		pending, _ := store.Pending()
		raw, _ := json.Marshal(map[string]any{"pending": pending, "shown": store.Shown(), "config": global})
		var result protocol.JSONObject
		_ = result.UnmarshalJSON(raw)
		for _, key := range []string{"pending", "shown", "config"} {
			if !bytes.Equal(canonical(t, result.Get(key)), canonical(t, states[i].Get(key))) {
				t.Fatalf("action %d field %s Go %s Source %s", i, key, result.Get(key), states[i].Get(key))
			}
		}
	}
}
func TestPublicGateIgnoresCacheAndAntOverridePrecedence(t *testing.T) {
	var global protocol.JSONObject
	_ = global.UnmarshalJSON([]byte(`{"cachedGrowthBookFeatures":{"tengu_lapis_finch":true},"growthBookOverrides":{"tengu_lapis_finch":true}}`))
	for _, row := range []struct {
		env  map[string]string
		want bool
	}{
		{map[string]string{}, false}, {map[string]string{"USER_TYPE": "external", "CLAUDE_INTERNAL_FC_OVERRIDES": `{"tengu_lapis_finch":true}`}, false},
		{map[string]string{"USER_TYPE": "ant"}, true}, {map[string]string{"USER_TYPE": "ant", "CLAUDE_INTERNAL_FC_OVERRIDES": `{"tengu_lapis_finch":false}`}, false},
		{map[string]string{"USER_TYPE": "ant", "CLAUDE_INTERNAL_FC_OVERRIDES": `{"other":true}`}, true},
	} {
		if got := Enabled(row.env, global); got != row.want {
			t.Fatalf("gate %v got %v want %v", row.env, got, row.want)
		}
		actual := sourceHints(t, map[string]any{"realGate": true, "global": global, "environment": row.env})
		if Truthy(actual.Get("resolvedGate")) != row.want {
			t.Fatalf("actual Source gate %v %s", row.env, actual.Get("resolvedGate"))
		}
	}
	store := Store{}
	gate := GateState{Enabled: true, Shown: make([]json.RawMessage, 100)}
	if store.Record(Hint{1, "plugin", json.RawMessage(`"x@agent-skills"`), json.RawMessage(`"x"`)}, gate) {
		t.Fatal("100 shown cap ignored")
	}
}

func TestActualSourceMenuChoicesAndTimeout(t *testing.T) {
	choices := []string{"yes", "no", "disable", "cancel", "timeout", "unexpected"}
	source := sourceHints(t, map[string]any{"menuResponses": choices})
	var rows []struct {
		Title     string
		Options   []struct{ Label, Value string }
		Responses []string
		TimeoutMs int
	}
	_ = json.Unmarshal(source.Get("menuRows"), &rows)
	if len(rows) != len(choices) {
		t.Fatal(string(source.Get("menuRows")))
	}
	for i, row := range rows {
		want := choices[i]
		if want == "cancel" || want == "timeout" || want == "unexpected" {
			want = "no"
		}
		if row.Title != "Plugin Recommendation" || row.TimeoutMs != 30000 || len(row.Responses) != 1 || row.Responses[0] != want || len(row.Options) != 3 {
			t.Fatalf("actual Source menu %#v", row)
		}
		for j, value := range []string{"yes", "no", "disable"} {
			if row.Options[j].Value != value {
				t.Fatal(row)
			}
		}
	}
}
