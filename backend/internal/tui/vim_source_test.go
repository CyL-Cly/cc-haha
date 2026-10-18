package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type vimFixtureCase struct {
	Name, Text, Mode string
	Offset, Columns  int
	Keys             []Key
}

func vimFixtureKeys(text string) []Key {
	result := []Key{}
	for _, r := range text {
		result = append(result, Key{Text: string(r)})
	}
	return result
}
func vimFixtureSequence(text string, extra ...Key) []Key {
	return append(vimFixtureKeys(text), extra...)
}
func actualVimFixture(t *testing.T, cases []vimFixtureCase) []protocol.JSONObject {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual frozen Vim Source fixture requires pinned Bun")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-vim-input.fixture.ts")
	input := []map[string]any{}
	for _, row := range cases {
		keys := []map[string]string{}
		for _, key := range row.Keys {
			keys = append(keys, map[string]string{"name": key.Name, "text": key.Text})
		}
		input = append(input, map[string]any{"name": row.Name, "text": row.Text, "mode": row.Mode, "offset": row.Offset, "columns": row.Columns, "keys": keys})
	}
	data, _ := json.Marshal(map[string]any{"cases": input})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	child.Dir = home
	child.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_VIM_INPUT_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	child.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	child.Stderr = &stderr
	output, err := child.Output()
	if err != nil {
		t.Fatalf("actual Vim Source %v %.3000s", err, stderr.String())
	}
	var result []protocol.JSONObject
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("Vim Source response %v %.500s", err, output)
	}
	return result
}
func vimJSONUnits(value []uint16) json.RawMessage { return protocol.QuoteJavaScriptUTF16(value) }
func vimFixtureCount(count int) any {
	if count < 0 {
		return nil
	}
	return count
}
func nativeVimSnapshot(v *VimEditor) map[string]any {
	command := map[string]any{"type": v.Command.Type}
	if v.Mode != "NORMAL" {
		command = nil
	} else {
		switch v.Command.Type {
		case "count":
			command["digits"] = v.Command.Digits
		case "operator", "operatorCount", "operatorFind", "operatorTextObj", "operatorG":
			command["op"] = v.Command.Operator
			command["count"] = vimFixtureCount(v.Command.Count)
		case "find", "g", "replace", "indent":
			command["count"] = vimFixtureCount(v.Command.Count)
		}
		if v.Command.Type == "operatorCount" {
			command["digits"] = v.Command.Digits
		}
		if v.Command.Type == "find" || v.Command.Type == "operatorFind" {
			command["find"] = v.Command.Find
		}
		if v.Command.Type == "operatorTextObj" {
			command["scope"] = v.Command.Scope
		}
		if v.Command.Type == "indent" {
			command["dir"] = v.Command.Direction
		}
	}
	var lastFind, lastChange any
	if v.LastFind != nil {
		lastFind = map[string]any{"type": v.LastFind.Type, "char": vimJSONUnits(v.LastFind.Character)}
	}
	if value := v.LastChange; value != nil {
		change := map[string]any{"type": value.Type}
		switch value.Type {
		case "insert":
			change["text"] = vimJSONUnits(value.Text)
		case "operator":
			change["op"], change["motion"], change["count"] = value.Operator, value.Motion, vimFixtureCount(value.Count)
		case "operatorTextObj":
			change["op"], change["scope"], change["objType"], change["count"] = value.Operator, value.Scope, value.Object, vimFixtureCount(value.Count)
		case "operatorFind":
			change["op"], change["find"], change["char"], change["count"] = value.Operator, value.Find, vimJSONUnits(value.Character), vimFixtureCount(value.Count)
		case "replace":
			change["char"], change["count"] = vimJSONUnits(value.Character), vimFixtureCount(value.Count)
		case "indent":
			change["dir"], change["count"] = value.Direction, vimFixtureCount(value.Count)
		case "openLine":
			change["direction"] = value.Direction
		default:
			change["count"] = vimFixtureCount(value.Count)
		}
		lastChange = change
	}
	return map[string]any{"text": vimJSONUnits(v.Text), "offset": v.Offset, "mode": v.Mode, "command": command, "persistent": map[string]any{"lastChange": lastChange, "lastFind": lastFind, "register": vimJSONUnits(v.Register), "registerIsLinewise": v.RegisterIsLinewise}, "error": nil}
}
func vimFixtureBase(editor *Editor, key Key) {
	switch key.Name {
	case "enter":
		return
	case "left", "ctrl+b":
		editor.Left()
	case "right", "ctrl+f":
		editor.Right()
	case "up":
		editor.Vertical(-1)
	case "down":
		editor.Vertical(1)
	case "ctrl+a":
		editor.Home()
	case "ctrl+e":
		editor.End()
	case "backspace", "ctrl+h":
		editor.Backspace()
	case "delete", "ctrl+d":
		editor.Delete()
	default:
		if !strings.HasPrefix(key.Name, "ctrl+") && key.Text != "" {
			editor.Insert(vimDisplay(vimNFC(vimUnits(key.Text))))
		}
	}
}

// Decode all strings as code units. encoding/json's ordinary string decoder
// would make distinct lone surrogates compare equal as the replacement glyph.
func vimFixtureSemantic(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	raw = bytes.TrimSpace(raw)
	switch raw[0] {
	case '"':
		units, err := protocol.DecodeJavaScriptJSONString(raw)
		if err != nil {
			t.Fatal(err)
		}
		return units
	case '{':
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		result := map[string]any{}
		for k, v := range fields {
			result[k] = vimFixtureSemantic(t, v)
		}
		return result
	case '[':
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			t.Fatal(err)
		}
		result := []any{}
		for _, v := range values {
			result = append(result, vimFixtureSemantic(t, v))
		}
		return result
	default:
		var result any
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
}
func TestVimInputActualSourceStateMachineOperatorsRepeatsAndUTF16(t *testing.T) {
	cases := []vimFixtureCase{}
	add := func(name, text string, offset int, keys []Key) {
		cases = append(cases, vimFixtureCase{Name: name, Text: text, Mode: "NORMAL", Offset: offset, Columns: 80, Keys: keys})
	}
	for _, motion := range []string{"h", "l", "j", "k", "w", "b", "e", "W", "B", "E", "0", "^", "$", "G", "gg", "2gg", "3G", "gj", "gk", "f-", "F-", "t-", "T-", "f-;,"} {
		add("motion/"+motion, "  hello-world!\n中文 next\nlast line", 5, vimFixtureKeys(motion))
	}
	for _, operator := range []string{"dw", "cw", "yw", "d2w", "2dw", "2d3w", "dd", "cc", "yy", "2dd", "D", "C", "Y", "d$", "dj", "dk", "dG", "dgg", "df-", "dt-", "yF-", "cT-", "diw", "daw", "di\"", "ca(", "yi[", "daB", "diW"} {
		add("operator/"+operator, "alpha beta (inside) [child] \"quoted\"\nsecond-line\nlast", 7, vimFixtureKeys(operator))
	}
	for _, keys := range []string{"x.", "2x.", "rZ.", "~.", ">>.", "<<.", "J.", "yyp", "yyP", "dwp", "u", "dd.u", "dgg.", "?"} {
		add("change/"+keys, "alpha-beta\n  next\nlast", 3, vimFixtureKeys(keys))
	}
	for _, literal := range []string{"r", "f", "df", "2", "d", "d2", "g", "dg", ">"} {
		add("cancel/"+literal, "alpha beta", 2, vimFixtureSequence(literal, Key{Name: "backspace"}, Key{Name: "esc"}))
	}
	cases = append(cases, vimFixtureCase{Name: "insert-repeat", Text: "hello ", Mode: "INSERT", Offset: 6, Columns: 80, Keys: []Key{{Text: "new"}, {Name: "esc"}, {Text: "0"}, {Text: "."}}}, vimFixtureCase{Name: "astral-escape-delete", Text: "😀", Mode: "INSERT", Offset: 2, Columns: 80, Keys: []Key{{Name: "esc"}, {Text: "x"}, {Text: "u"}}}, vimFixtureCase{Name: "astral-escape-insert", Text: "😀", Mode: "INSERT", Offset: 2, Columns: 80, Keys: []Key{{Name: "esc"}, {Text: "i"}, {Text: "x"}, {Name: "esc"}}}, vimFixtureCase{Name: "unicode-case", Text: "ßİ😀中文", Mode: "NORMAL", Offset: 0, Columns: 80, Keys: vimFixtureKeys("3~x.")}, vimFixtureCase{Name: "image-chip", Text: "left [Image #7] right", Mode: "NORMAL", Offset: 5, Columns: 80, Keys: vimFixtureKeys("lx0dw.")})

	for _, text := range []string{"one two-three\nfour five\nsix", "  中文😀 abc  \n\tsecond", "longlonglong word abc\nlast line", "e\u0301cole 界😀 z"} {
		for _, command := range []string{"gg", "G", "dj", "dk", "cw", "ce", "de", "d$", "dgg", "2d3w", "diw", "daw", "diW", "daW", "o", "O", "J", ">>", "<<", "yyp", "dwp", "gj", "gk", "2gj", "2gk", "3~", "3rZ", "2x"} {
			for _, offset := range []int{0, 3, len(vimUnits(text))} {
				cases = append(cases, vimFixtureCase{Name: fmt.Sprintf("edges/%x/%s/%d", text, command, offset), Text: text, Mode: "NORMAL", Offset: offset, Columns: 9, Keys: vimFixtureKeys(command)})
			}
		}
	}
	for _, command := range []string{"ab3", "ab3w", "ab31w", "d3x", "d2d", "12z", "9999999999999999w", "d0w", "d0x", "gq", "r", "f", "df", "di"} {
		cases = append(cases, vimFixtureCase{Name: "burst/" + command, Text: "abcdef ghi", Mode: "NORMAL", Offset: 3, Columns: 80, Keys: []Key{{Text: command}, {Text: "2"}, {Text: "w"}}})
	}
	for _, suffix := range []string{"x", "w", "d", "g", "r", ">"} {
		cases = append(cases, vimFixtureCase{Name: "raw-count/" + suffix, Text: "abcdef ghi", Mode: "NORMAL", Offset: 3, Columns: 80, Keys: []Key{{Text: "9999999999999999"}, {Text: suffix}}})
		cases = append(cases, vimFixtureCase{Name: "NaN-count/" + suffix, Text: "abcdef ghi", Mode: "NORMAL", Offset: 3, Columns: 80, Keys: []Key{{Text: "bad3"}, {Text: "2"}, {Text: suffix}}})
	}
	t.Logf("actual Source Vim cases: %d", len(cases))
	source := actualVimFixture(t, cases)
	if len(source) != len(cases) {
		t.Fatal("actual Vim Source cases missing", len(source), len(cases))
	}
	for index, row := range cases {
		t.Run(row.Name, func(t *testing.T) {
			v := NewVimEditor()
			v.Text = vimUnits(row.Text)
			v.Offset = row.Offset
			v.Mode = row.Mode
			var editor Editor
			v.publish(&editor)
			var observations []protocol.JSONObject
			_ = json.Unmarshal(source[index].Get("observations"), &observations)
			for step, key := range row.Keys {
				v.sync(&editor)
				if v.HandleKey(key, row.Columns) {
					v.publish(&editor)
				} else {
					vimFixtureBase(&editor, key)
					v.sync(&editor)
					v.publish(&editor)
				}
				data, _ := json.Marshal(nativeVimSnapshot(v))
				actual := vimFixtureSemantic(t, data)
				sourceJSON, sourceErr := observations[step].MarshalJSON()
				if sourceErr != nil {
					t.Fatal(sourceErr)
				}
				want := vimFixtureSemantic(t, sourceJSON)
				if !reflect.DeepEqual(actual, want) {
					t.Logf("wrap Go %v position %v Source %s", v.cursor(row.Columns).wrapped(), func() []int { a, b := v.cursor(row.Columns).position(); return []int{a, b} }(), source[index].Get("debug"))
					t.Fatalf("actual Source Vim differs step%d key%q/%q\nGo %.2200s\nSource %.2200s", step, key.Name, key.Text, data, sourceJSON)
				}
			}
		})
	}
}
