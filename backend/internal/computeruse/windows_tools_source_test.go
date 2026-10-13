package computeruse

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

type pixelProfile struct {
	Mode     string          `json:"mode,omitempty"`
	Gates    map[string]bool `json:"gates,omitempty"`
	Disabled bool            `json:"disabled,omitempty"`
	Replies  object          `json:"replies,omitempty"`
	Cases    []toolCase      `json:"cases"`
}
type pixelNativeCall struct {
	Command string          `json:"command"`
	Payload json.RawMessage `json:"payload"`
}
type pixelProfileResult struct {
	Results  []NativeToolResult `json:"results"`
	Calls    []pixelNativeCall  `json:"calls"`
	Selected *float64           `json:"selected"`
	Pinned   bool               `json:"pinned"`
}

func pixelSource(t *testing.T, profiles []pixelProfile) []byte {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Windows pixel parity requires the pinned actual Source Bun")
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-cu-windows-pixel.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	child.Dir = home
	child.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + os.TempDir(), "BUN_OPTIONS=--no-env-file", "CC_HAHA_CU_WINDOWS_PIXEL_FIXTURE=1", "NODE_ENV=production", "PATH=/usr/bin:/bin", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	child.Stdin = bytes.NewReader(encode(map[string]any{"profiles": profiles}))
	output, err := child.CombinedOutput()
	if err != nil {
		t.Fatal("actual Windows Source producer", err, string(output))
	}
	return output
}
func pixelFixtureReply(command string, payload object) json.RawMessage {
	display := map[string]any{"width": 1920, "height": 1080, "scaleFactor": 1, "originX": 0, "originY": 0, "displayId": 1, "label": "Primary"}
	switch command {
	case "frontmost_app":
		return encode(map[string]any{"bundleId": "owned.fixture", "displayName": "Owned fixture", "pid": 1234})
	case "get_display_size":
		return encode(display)
	case "list_displays":
		return encode([]any{display})
	case "cursor_position":
		return json.RawMessage(`{"x":480,"y":270}`)
	case "list_installed_apps":
		return json.RawMessage(`[{"bundleId":"owned.fixture","displayName":"Owned fixture"}]`)
	case "read_clipboard":
		return encode("owned original clipboard")
	case "prepare_for_action":
		return json.RawMessage(`[]`)
	case "screenshot", "resolve_prepare_capture":
		image := bytes.Repeat([]byte{1}, 1025)
		return encode(map[string]any{"base64": base64.StdEncoding.EncodeToString(image), "width": payload["targetWidth"], "height": payload["targetHeight"], "displayWidth": 1920, "displayHeight": 1080, "originX": 0, "originY": 0, "displayId": 1, "display": display})
	case "zoom":
		return encode(map[string]any{"base64": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 1025)), "width": payload["targetWidth"], "height": payload["targetHeight"]})
	default:
		return json.RawMessage(`null`)
	}
}
func runPixelGoProfile(t *testing.T, profile pixelProfile) pixelProfileResult {
	t.Helper()
	o := fixtureOptions(t)
	o.Platform = "win32"
	s := fixtureService(t, o)
	if err := s.UpdateConfig(context.Background(), encode(map[string]any{"enabled": !profile.Disabled})); err != nil {
		t.Fatal(err)
	}
	result := pixelProfileResult{Results: []NativeToolResult{}, Calls: []pixelNativeCall{}}
	counts := map[string]int{}
	options := WindowsToolsOptions{Native: NativeToolsOptions{Service: s, SessionID: "actual-pixel-owner", NativeCall: func(_ context.Context, command string, payload json.RawMessage) (json.RawMessage, error) {
		result.Calls = append(result.Calls, pixelNativeCall{command, append(json.RawMessage{}, payload...)})
		count := counts[command]
		counts[command]++
		if reply, ok := profile.Replies[command]; ok {
			var series []json.RawMessage
			if len(reply) > 0 && reply[0] == '[' && json.Unmarshal(reply, &series) == nil {
				reply = series[min(count, len(series)-1)]
			}
			if value, ok := decodeObject(reply); ok {
				if message, ok := stringValue(value["throw"]); ok {
					return nil, errors.New(message)
				}
			}
			return reply, nil
		}
		args, _ := decodeObject(payload)
		return pixelFixtureReply(command, args), nil
	}}, CoordinateMode: profile.Mode, Sleep: func(context.Context, time.Duration) error { return nil }, SubGates: func() WindowsSubGates {
		g := WindowsSubGates{ClipboardPasteMultiline: true, MouseAnimation: true, HideBeforeAction: true, AutoTargetDisplay: true, ClipboardGuard: true}
		for key, value := range profile.Gates {
			switch key {
			case "pixelValidation":
				g.PixelValidation = value
			case "clipboardPasteMultiline":
				g.ClipboardPasteMultiline = value
			case "mouseAnimation":
				g.MouseAnimation = value
			case "hideBeforeAction":
				g.HideBeforeAction = value
			case "autoTargetDisplay":
				g.AutoTargetDisplay = value
			case "clipboardGuard":
				g.ClipboardGuard = value
			}
		}
		return g
	}}
	p, err := NewWindowsTools(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := p.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	for _, row := range profile.Cases {
		value, err := p.Call(context.Background(), row.Name, row.Args)
		if err != nil {
			t.Fatal(err)
		}
		result.Results = append(result.Results, value)
	}
	result.Selected, result.Pinned = p.selected, p.pinned
	return result
}
func pixelCase(name, args string) toolCase { return toolCase{Name: name, Args: json.RawMessage(args)} }
func TestActualWindowsPixelAllToolsStateCoordinatesAndFailuresMatchSource(t *testing.T) {
	valid := []toolCase{
		pixelCase("request_access", `{}`), pixelCase("list_granted_applications", `{}`), pixelCase("cursor_position", `{}`),
		pixelCase("left_click", `{"coordinate":[481,270],"text":" shift + ctrl "}`), pixelCase("double_click", `{"coordinate":[3.5,4.5]}`), pixelCase("triple_click", `{"coordinate":[0,0]}`), pixelCase("right_click", `{"coordinate":[12,13]}`), pixelCase("middle_click", `{"coordinate":[12,13]}`),
		pixelCase("screenshot", `{}`), pixelCase("zoom", `{"region":[0,0,100,100]}`), pixelCase("left_click", `{"coordinate":[500,281]}`), pixelCase("cursor_position", `{}`), pixelCase("mouse_move", `{"coordinate":[5,6]}`),
		pixelCase("left_mouse_down", `{}`), pixelCase("left_mouse_down", `{}`), pixelCase("left_click", `{"coordinate":[50,60]}`), pixelCase("left_mouse_down", `{}`), pixelCase("left_click_drag", `{"coordinate":[100,200],"start_coordinate":[1,2]}`), pixelCase("left_click_drag", `{"coordinate":[100,200]}`), pixelCase("left_mouse_up", `{}`),
		pixelCase("scroll", `{"coordinate":[0,0],"scroll_direction":"down","scroll_amount":3}`), pixelCase("scroll", `{"coordinate":[0,0],"scroll_direction":"up","scroll_amount":2}`), pixelCase("scroll", `{"coordinate":[0,0],"scroll_direction":"left","scroll_amount":1}`), pixelCase("scroll", `{"coordinate":[0,0],"scroll_direction":"right","scroll_amount":0}`),
		pixelCase("type", `{"text":"hello\r\nworld\t!"}`), pixelCase("type", `{"text":"中文👨‍👩‍👧‍👦"}`), pixelCase("type", `{"text":""}`), pixelCase("key", `{"text":"ctrl+a","repeat":2}`), pixelCase("hold_key", `{"text":" shift + ctrl + a ","duration":0}`), pixelCase("wait", `{"duration":0}`),
		pixelCase("read_clipboard", `{}`), pixelCase("write_clipboard", `{"text":"测试<>&\u2028\u2029"}`), pixelCase("open_application", `{"app":" OWNED FIXTURE "}`), pixelCase("switch_display", `{"display":"auto"}`), pixelCase("switch_display", `{"display":"Primary"}`),
		pixelCase("computer_batch", `{"actions":[{"action":"key","text":"ctrl+a"},{"action":"type","text":"ABC"},{"action":"screenshot"},{"action":"left_click","coordinate":[5,6]},{"action":"wait","duration":0}]}`), pixelCase("computer_batch", `{"actions":[{"action":"left_mouse_down"},{"action":"left_click","coordinate":[-1,2]},{"action":"type","text":"must not dispatch"}]}`),
	}
	invalid := []toolCase{}
	for _, name := range []string{"left_click", "right_click", "middle_click", "double_click", "triple_click", "left_click_drag", "mouse_move", "scroll"} {
		for _, args := range []string{`{}`, `{"coordinate":null}`, `{"coordinate":[1]}`, `{"coordinate":[1,2,3]}`, `{"coordinate":[1,"2"]}`, `{"coordinate":[-1,2]}`} {
			invalid = append(invalid, pixelCase(name, args))
		}
	}
	for _, row := range [][2]string{
		{"left_click", `{"coordinate":[1,2],"text":null}`}, {"left_click_drag", `{"coordinate":[1,2],"start_coordinate":null}`},
		{"scroll", `{"coordinate":[1,2],"scroll_direction":"invalid","scroll_amount":2}`}, {"scroll", `{"coordinate":[1,2],"scroll_direction":"down","scroll_amount":-1}`}, {"scroll", `{"coordinate":[1,2],"scroll_direction":"down","scroll_amount":1.5}`}, {"scroll", `{"coordinate":[1,2],"scroll_direction":"down","scroll_amount":101}`},
		{"type", `{}`}, {"type", `{"text":17}`}, {"key", `{}`}, {"key", `{"text":"a","repeat":0}`}, {"key", `{"text":"a","repeat":1.5}`}, {"key", `{"text":"a","repeat":null}`}, {"key", `{"text":"a","repeat":101}`},
		{"hold_key", `{"text":"a"}`}, {"hold_key", `{"text":"a","duration":-1}`}, {"hold_key", `{"text":"a","duration":101}`}, {"wait", `{"duration":null}`}, {"wait", `{"duration":-1}`}, {"wait", `{"duration":101}`},
		{"zoom", `{}`}, {"zoom", `{"region":[0,0,1]}`}, {"zoom", `{"region":[0,0,"1",2]}`}, {"zoom", `{"region":[-1,0,1,2]}`}, {"zoom", `{"region":[1,0,1,2]}`}, {"zoom", `{"region":[0,1,2,1]}`}, {"zoom", `{"region":[0,0,1,2]}`},
		{"open_application", `{}`}, {"open_application", `{"app":"not installed.exe"}`}, {"switch_display", `{}`}, {"write_clipboard", `{"text":false}`}, {"unknown", `{}`}, {"request_teach_access", `{}`}, {"request_teach_access", `{"reason":"owned","apps":false}`}, {"request_teach_access", `{"reason":"owned","apps":[]}`},
		{"computer_batch", `{}`}, {"computer_batch", `{"actions":[]}`}, {"computer_batch", `{"actions":[null]}`}, {"computer_batch", `{"actions":[{"action":1}]}`}, {"computer_batch", `{"actions":[{"action":"type","text":"must not dispatch"},{"action":"open_application","app":"owned.fixture"}]}`},
	} {
		invalid = append(invalid, pixelCase(row[0], row[1]))
	}
	profiles := []pixelProfile{
		{Cases: valid}, {Mode: "normalized_0_100", Cases: valid}, {Gates: map[string]bool{"autoTargetDisplay": false, "mouseAnimation": false, "hideBeforeAction": false, "clipboardPasteMultiline": false}, Cases: valid}, {Cases: invalid},
		{Disabled: true, Cases: []toolCase{pixelCase("request_access", `{}`), pixelCase("screenshot", `{}`), pixelCase("key", `{"text":"a"}`)}},
		{Replies: object{"frontmost_app": json.RawMessage("null")}, Cases: []toolCase{pixelCase("type", `{"text":"owned"}`), pixelCase("left_mouse_up", `{}`), pixelCase("left_click", `{"coordinate":[1,2]}`)}},
		{Replies: object{"frontmost_app": encode(map[string]any{"throw": "foreground failed"})}, Cases: []toolCase{pixelCase("key", `{"text":"a"}`)}},
		{Replies: object{"key": encode(map[string]any{"throw": "paste failed"})}, Cases: []toolCase{pixelCase("type", `{"text":"中文"}`)}},
		{Gates: map[string]bool{"pixelValidation": true}, Cases: []toolCase{pixelCase("left_click", `{"coordinate":[1,2]}`), pixelCase("screenshot", `{}`), pixelCase("left_click", `{"coordinate":[1,2]}`)}},
		{Replies: object{"list_displays": encode(map[string]any{"throw": "display failed"})}, Cases: []toolCase{pixelCase("switch_display", `{"display":"Secondary"}`), pixelCase("screenshot", `{}`), pixelCase("open_application", `{"app":"owned.fixture"}`)}},
	}
	output := pixelSource(t, profiles)
	var source struct {
		Profiles []pixelProfileResult `json:"profiles"`
	}
	if err := json.Unmarshal(output, &source); err != nil || len(source.Profiles) != len(profiles) {
		t.Fatal(err, string(output))
	}
	count := 0
	for i, profile := range profiles {
		t.Run(fmtPixelProfile(i), func(t *testing.T) {
			actual := runPixelGoProfile(t, profile)
			if !bytes.Equal(exactPixelJSON(encode(actual)), exactPixelJSON(encode(source.Profiles[i]))) {
				for j, value := range actual.Results {
					if j < len(source.Profiles[i].Results) && !bytes.Equal(exactPixelJSON(encode(value)), exactPixelJSON(encode(source.Profiles[i].Results[j]))) {
						t.Errorf("case%d %s Source=%s Go=%s", j, profile.Cases[j].Name, encode(source.Profiles[i].Results[j]), encode(value))
					}
				}
				if !bytes.Equal(exactPixelJSON(encode(actual.Calls)), exactPixelJSON(encode(source.Profiles[i].Calls))) {
					t.Errorf("native OS primitive order/payload Source=%s Go=%s", encode(source.Profiles[i].Calls), encode(actual.Calls))
				}
				if !reflect.DeepEqual(actual.Selected, source.Profiles[i].Selected) || actual.Pinned != source.Profiles[i].Pinned {
					t.Errorf("display state Source=%s Go=%s", encode(source.Profiles[i]), encode(actual))
				}
			}
		})
		count += len(profile.Cases)
	}
	t.Logf("actual frozen MAIN Windows pixel dispatcher cases=%d profiles=%d", count, len(profiles))
}
func fmtPixelProfile(i int) string { return string(rune('A' + i)) }

func TestActualWindowsPixelCaptureMonitorRetryAndZoomKeepSourceAnchors(t *testing.T) {
	shot := func(width, height, dw, dh, ox, oy, id float64, small bool) json.RawMessage {
		size := 1025
		if small {
			size = 3
		}
		return encode(map[string]any{"base64": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, size)), "width": width, "height": height, "displayWidth": dw, "displayHeight": dh, "originX": ox, "originY": oy, "displayId": id, "display": map[string]any{"width": dw, "height": dh, "scaleFactor": 2, "originX": ox, "originY": oy, "displayId": id}})
	}
	geometry := []any{map[string]any{"width": 1920, "height": 1080, "scaleFactor": 2, "originX": 0, "originY": 0, "id": 2, "name": "Matched"}, map[string]any{"width": 1600, "height": 900, "scaleFactor": 1.25, "originX": -1600, "originY": 0, "displayId": 1, "label": "Matched"}}
	profiles := []pixelProfile{
		{Replies: object{"get_display_size": encode(map[string]any{"width": 1600, "height": 900, "scaleFactor": 1.25, "originX": -1600, "originY": 0, "id": 1, "name": "Matched"}), "resolve_prepare_capture": shot(1456, 819, 1600, 900, -1600, 0, 1, false), "list_displays": encode([]any{geometry})}, Cases: []toolCase{pixelCase("screenshot", `{}`), pixelCase("left_click", `{"coordinate":[728,409.5]}`), pixelCase("cursor_position", `{}`), pixelCase("switch_display", `{"display":"Matched (2)"}`), pixelCase("screenshot", `{}`), pixelCase("switch_display", `{"display":"missing"}`), pixelCase("switch_display", `{"display":"AUTO"}`), pixelCase("screenshot", `{}`), pixelCase("zoom", `{"region":[0,0,100,100]}`), pixelCase("zoom", `{"region":[0,0,1457,820]}`), pixelCase("open_application", `{"app":"owned.fixture"}`)}},
		{Gates: map[string]bool{"autoTargetDisplay": false}, Replies: object{"screenshot": encode([]json.RawMessage{shot(1456, 819, 1920, 1080, 0, 0, 1, true), shot(1456, 819, 1920, 1080, 0, 0, 1, false)})}, Cases: []toolCase{pixelCase("screenshot", `{}`), pixelCase("zoom", `{"region":[0,0,100,100]}`), pixelCase("left_click", `{"coordinate":[728,410]}`)}},
		{Replies: object{"resolve_prepare_capture": json.RawMessage(`{"base64":"","width":1456,"height":819,"displayWidth":1920,"displayHeight":1080,"originX":0,"originY":0,"displayId":1,"display":{"width":1920,"height":1080,"scaleFactor":1,"originX":0,"originY":0,"id":1},"captureError":"owned capture failed"}`)}, Cases: []toolCase{pixelCase("screenshot", `{}`), pixelCase("left_click", `{"coordinate":[11,12]}`), pixelCase("zoom", `{"region":[0,0,100,100]}`)}},
		{Replies: object{"get_display_size": encode(map[string]any{"width": 2560, "height": 1440, "scaleFactor": 2, "originX": 1920, "originY": -720, "displayId": 2}), "resolve_prepare_capture": shot(1456, 819, 2560, 1440, 1920, -720, 2, false), "cursor_position": json.RawMessage(`{"x":3200,"y":0}`)}, Cases: []toolCase{pixelCase("screenshot", `{}`), pixelCase("cursor_position", `{}`), pixelCase("computer_batch", `{"actions":[{"action":"screenshot"},{"action":"left_click","coordinate":[728,409.5]}]}`), pixelCase("left_click", `{"coordinate":[728,409.5]}`)}},
		{Mode: "normalized_0_100", Replies: object{"get_display_size": encode(map[string]any{"width": 1600, "height": 900, "scaleFactor": 1.25, "originX": -1600, "originY": -900, "id": 1})}, Cases: []toolCase{pixelCase("left_click", `{"coordinate":[50,50]}`), pixelCase("mouse_move", `{"coordinate":[100,100]}`), pixelCase("left_click_drag", `{"coordinate":[50,50],"start_coordinate":[0,0]}`)}},
	}
	var source struct {
		Profiles []pixelProfileResult `json:"profiles"`
	}
	output := pixelSource(t, profiles)
	if err := json.Unmarshal(output, &source); err != nil || len(source.Profiles) != len(profiles) {
		t.Fatal(err, string(output))
	}
	for i, profile := range profiles {
		t.Run(fmtPixelProfile(i), func(t *testing.T) {
			actual := runPixelGoProfile(t, profile)
			if !bytes.Equal(exactPixelJSON(encode(actual)), exactPixelJSON(encode(source.Profiles[i]))) {
				t.Fatalf("Source=%s\nGo=%s", encode(source.Profiles[i]), encode(actual))
			}
		})
	}
}
