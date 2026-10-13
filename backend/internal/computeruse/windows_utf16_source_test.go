package computeruse

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/curepl"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualWindowsUTF16NativeInputInventoryDisplayClipboardAndMCPTextAgainstSource(t *testing.T) {
	profiles := []pixelProfile{
		{Replies: object{"list_installed_apps": json.RawMessage(`[[{"bundleId":"greek.exe","displayName":"ΟΣ"},{"bundleId":"dot.exe","displayName":"İ"}]]`)}, Cases: []toolCase{pixelCase("open_application", `{"app":"ος"}`), pixelCase("open_application", `{"app":"i\u0307"}`)}},

		{Replies: object{"read_clipboard": json.RawMessage(`"original\udc00"`)}, Cases: []toolCase{
			pixelCase("type", `{"text":"\ud800"}`), pixelCase("type", `{"text":"\udc00"}`), pixelCase("type", `{"text":"A\ud800B"}`), pixelCase("type", `{"text":"中文👨‍👩‍👧‍👦"}`),
			pixelCase("write_clipboard", `{"text":"\udc00"}`), pixelCase("read_clipboard", `{}`), pixelCase("key", `{"text":"\ud800"}`), pixelCase("hold_key", `{"text":" shift + \ud800 +\ufeff\udc00\u3000 + ctrl ","duration":0}`), pixelCase("left_click", `{"coordinate":[1,2],"text":"ctrl+\udc00"}`),
		}},
		{Replies: object{"list_installed_apps": json.RawMessage(`[[{"bundleId":"app\udc00","displayName":"name\udc00"},{"bundleId":"app\ud800","displayName":"name\ud800"}]]`)}, Cases: []toolCase{pixelCase("open_application", `{"app":" name\ud800 "}`), pixelCase("open_application", `{"app":"app\udc00"}`), pixelCase("open_application", `{"app":"name\ud801"}`)}},
		{Replies: object{"list_displays": json.RawMessage(`[[{"width":1920,"height":1080,"scaleFactor":1,"displayId":1,"label":"screen\ud800"},{"width":1920,"height":1080,"scaleFactor":1,"displayId":2,"label":"screen\udc00"},{"width":1920,"height":1080,"scaleFactor":1,"displayId":3,"name":"screen\ud800"}]]`)}, Cases: []toolCase{pixelCase("screenshot", `{}`), pixelCase("switch_display", `{"display":"screen\udc00"}`), pixelCase("switch_display", `{"display":"SCREEN\ud800 (2)"}`), pixelCase("switch_display", `{"display":"screen\ud801"}`)}},
		{Replies: object{"resolve_prepare_capture": json.RawMessage(`{"displayId":1,"display":{"width":1920,"height":1080,"scaleFactor":1,"displayId":1},"captureError":"capture\ud800"}`)}, Cases: []toolCase{pixelCase("screenshot", `{}`)}},
		{Replies: object{"resolve_prepare_capture": json.RawMessage(`{"displayId":1,"display":{"width":1920,"height":1080,"scaleFactor":1,"displayId":1},"captureError":""}`)}, Cases: []toolCase{pixelCase("screenshot", `{}`)}},
		{Gates: map[string]bool{"clipboardPasteMultiline": false}, Cases: []toolCase{pixelCase("type", `{"text":"\ud800\u0301\udc00"}`)}},
	}
	var source struct{ Profiles []pixelProfileResult }
	if err := json.Unmarshal(pixelSource(t, profiles), &source); err != nil {
		t.Fatal(err)
	}
	if len(source.Profiles) != len(profiles) {
		t.Fatal("Source profile count")
	}
	canonical := exactPixelJSON
	for i, profile := range profiles {
		actual := runPixelGoProfile(t, profile)
		if !bytes.Equal(canonical(encode(actual.Calls)), canonical(encode(source.Profiles[i].Calls))) {
			t.Errorf("profile%d actual native payload Source=%s Go=%s", i, encode(source.Profiles[i].Calls), encode(actual.Calls))
		}
		if !bytes.Equal(canonical(encode(actual.Results)), canonical(encode(source.Profiles[i].Results))) {
			t.Errorf("profile%d raw native result Source=%s Go=%s", i, encode(source.Profiles[i].Results), encode(actual.Results))
		}
		// The same JSON conversion used by Binder.Call → curepl.Result must retain
		// the exact Source text, rather than decode both sides to replacements.
		for j, result := range actual.Results {
			var mcp curepl.Result
			if err := json.Unmarshal(encode(result), &mcp); err != nil {
				t.Fatal(err)
			}
			var expected curepl.Result
			if err := json.Unmarshal(encode(source.Profiles[i].Results[j]), &expected); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(canonical(encode(mcp)), canonical(encode(expected))) {
				t.Errorf("profile%d case%d exact MCP text Source=%s Go=%s", i, j, encode(expected), encode(mcp))
			}
		}
	}
}

func exactPixelJSON(raw json.RawMessage) json.RawMessage {
	value := bytes.TrimSpace(raw)
	switch value[0] {
	case '{':
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(value, &fields); err != nil {
			panic(err)
		}
		for name, row := range fields {
			fields[name] = exactPixelJSON(row)
		}
		result, err := json.Marshal(fields)
		if err != nil {
			panic(err)
		}
		return result
	case '[':
		var rows []json.RawMessage
		if err := json.Unmarshal(value, &rows); err != nil {
			panic(err)
		}
		for i, row := range rows {
			rows[i] = exactPixelJSON(row)
		}
		result, err := json.Marshal(rows)
		if err != nil {
			panic(err)
		}
		return result
	default:
		return protocol.StringifyValidatedJavaScriptJSON(value)
	}
}
