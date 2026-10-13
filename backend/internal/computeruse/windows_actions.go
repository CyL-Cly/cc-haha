package computeruse

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/dop251/goja"
	"github.com/rivo/uniseg"
)

func pixelJSON(value any) NativeToolResult {
	return textResult(string(pixelJSONStringify(encode(value))))
}
func pixelJSONStringify(raw json.RawMessage) json.RawMessage {
	if value, err := protocol.ParseAndStringifyJavaScriptJSON(raw); err == nil {
		return value
	}
	return raw
}
func pixelString(args object, key string) (string, error) {
	value, ok := stringValue(args[key])
	if !ok {
		return "", fmt.Errorf("\"%s\" must be a string.", key)
	}
	return value, nil
}
func pixelExactStringJSON(raw json.RawMessage) (json.RawMessage, error) {
	units, err := protocol.DecodeJavaScriptJSONString(raw)
	if err != nil {
		return nil, err
	}
	return protocol.QuoteJavaScriptUTF16(units), nil
}
func pixelStringJSON(args object, key string) (json.RawMessage, error) {
	raw, err := pixelExactStringJSON(args[key])
	if err != nil {
		return nil, fmt.Errorf("\"%s\" must be a string.", key)
	}
	return raw, nil
}
func pixelLowerJSON(raw json.RawMessage, trimmed bool) json.RawMessage {
	units, err := protocol.DecodeJavaScriptJSONString(raw)
	if err != nil {
		return nil
	}
	if trimmed {
		for len(units) > 0 && protocol.IsJavaScriptWhitespace(rune(units[0])) {
			units = units[1:]
		}
		for len(units) > 0 && protocol.IsJavaScriptWhitespace(rune(units[len(units)-1])) {
			units = units[:len(units)-1]
		}
	}
	vm := goja.New()
	value, err := vm.RunString("(function(s){return JSON.stringify(s.toLowerCase())})")
	if err != nil {
		panic(err)
	}
	fn, _ := goja.AssertFunction(value)
	output := []uint16{}
	lower := func(segment []uint16) {
		if len(segment) == 0 {
			return
		}
		result, err := fn(goja.Undefined(), goja.StringFromUTF16(segment))
		if err != nil {
			panic(err)
		}
		lowered, err := protocol.DecodeJavaScriptJSONString([]byte(result.String()))
		if err != nil {
			panic(err)
		}
		output = append(output, lowered...)
	}
	// Goja's Unicode string lowering decodes lone surrogate units to U+FFFD.
	// Keep those non-cased boundaries unchanged and lower valid segments only.
	start := 0
	for i := 0; i < len(units); i++ {
		unit := units[i]
		if unit >= 0xd800 && unit <= 0xdbff && i+1 < len(units) && units[i+1] >= 0xdc00 && units[i+1] <= 0xdfff {
			i++
			continue
		}
		if unit >= 0xd800 && unit <= 0xdfff {
			lower(units[start:i])
			output = append(output, unit)
			start = i + 1
		}
	}
	lower(units[start:])
	return protocol.QuoteJavaScriptUTF16(output)
}

func pixelRawMessage(parts ...json.RawMessage) NativeToolResult {
	raw, err := protocol.JoinJavaScriptValuesJSON(parts, "")
	if err != nil {
		panic(err)
	}
	units, err := protocol.DecodeJavaScriptJSONString(raw)
	if err != nil {
		panic(err)
	}
	return NativeToolResult{Content: []ToolContent{{Type: "text", Text: jsString(raw), textUnits: append([]uint16{}, units...)}}}
}
func pixelQuoted(text string) json.RawMessage { raw, _ := json.Marshal(text); return raw }
func pixelRawError(kind string, parts ...json.RawMessage) NativeToolResult {
	result := pixelRawMessage(parts...)
	result.IsError = true
	if kind != "" {
		result.Telemetry = map[string]string{"error_kind": kind}
	}
	return result
}
func pixelRawChord(raw json.RawMessage) []json.RawMessage {
	units, err := protocol.DecodeJavaScriptJSONString(raw)
	if err != nil {
		return nil
	}
	keys := []json.RawMessage{}
	for start := 0; start <= len(units); {
		end := start
		for end < len(units) && units[end] != '+' {
			end++
		}
		first, last := start, end
		for first < last && protocol.IsJavaScriptWhitespace(rune(units[first])) {
			first++
		}
		for last > first && protocol.IsJavaScriptWhitespace(rune(units[last-1])) {
			last--
		}
		if first < last {
			keys = append(keys, protocol.QuoteJavaScriptUTF16(units[first:last]))
		}
		start = end + 1
	}
	return keys
}
func pixelNumber(raw json.RawMessage) (float64, bool) {
	var value float64
	valid := json.Unmarshal(raw, &value) == nil && len(raw) > 0 && string(raw) != "null" && !math.IsInf(value, 0) && !math.IsNaN(value)
	return value, valid
}
func pixelCoordinate(args object, key string) ([2]float64, error) {
	var out [2]float64
	if args[key] == nil {
		return out, fmt.Errorf("%s is required", key)
	}
	var values []json.RawMessage
	if json.Unmarshal(args[key], &values) != nil || len(values) != 2 {
		return out, fmt.Errorf("%s must be an array of length 2", key)
	}
	for i, raw := range values {
		value, ok := pixelNumber(raw)
		if !ok || value < 0 {
			return out, fmt.Errorf("%s must be a tuple of non-negative numbers", key)
		}
		out[i] = value
	}
	return out, nil
}
func pixelDuration(args object) (float64, error) {
	duration, ok := pixelNumber(args["duration"])
	if !ok {
		return 0, fmt.Errorf("duration must be a number")
	}
	if duration < 0 {
		return 0, fmt.Errorf("duration must be non-negative")
	}
	if duration > 100 {
		return 0, fmt.Errorf("duration is too long. Duration is in seconds.")
	}
	return duration, nil
}

func (p *WindowsTools) inputGate(ctx context.Context, gates WindowsSubGates) (*NativeToolResult, error) {
	if gates.HideBeforeAction {
		if _, err := p.native(ctx, "prepare_for_action", object{}); err != nil {
			return nil, err
		}
	}
	frontmost, err := p.native(ctx, "frontmost_app", object{})
	if err != nil {
		return nil, err
	}
	if string(frontmost) == "null" || len(frontmost) == 0 {
		result := pixelError("The foreground application could not be identified. Refusing input until a supported application is brought to the front.", "state_conflict")
		return &result, nil
	}
	return nil, nil
}
func (p *WindowsTools) dispatch(ctx context.Context, name string, args object, gates WindowsSubGates) (NativeToolResult, error) {
	switch name {
	case "request_access":
		return pixelJSON(struct {
			Enabled             bool   `json:"enabled"`
			AppAuthorization    string `json:"appAuthorization"`
			ScreenshotFiltering string `json:"screenshotFiltering"`
		}{true, "all_supported_apps", "none"}), nil
	case "list_granted_applications":
		return pixelJSON(struct {
			AllowedApps []any `json:"allowedApps"`
			GrantFlags  struct {
				ClipboardRead   bool `json:"clipboardRead"`
				ClipboardWrite  bool `json:"clipboardWrite"`
				SystemKeyCombos bool `json:"systemKeyCombos"`
			} `json:"grantFlags"`
		}{AllowedApps: []any{}, GrantFlags: struct {
			ClipboardRead   bool `json:"clipboardRead"`
			ClipboardWrite  bool `json:"clipboardWrite"`
			SystemKeyCombos bool `json:"systemKeyCombos"`
		}{true, true, true}}), nil
	case "screenshot":
		return p.screenshot(ctx, gates, true)
	case "zoom":
		return p.zoom(ctx, args)
	case "left_click", "double_click", "triple_click", "right_click", "middle_click", "left_click_drag", "mouse_move", "scroll":
		return p.mouseAction(ctx, name, args, gates)
	case "type", "key", "hold_key":
		return p.keyboardAction(ctx, name, args, gates)
	case "wait":
		duration, err := pixelDuration(args)
		if err != nil {
			return pixelError(err.Error(), "bad_args"), nil
		}
		if err = p.options.Sleep(ctx, time.Duration(duration*float64(time.Second))); err != nil {
			return NativeToolResult{}, err
		}
		return textResult(fmt.Sprintf("Waited %gs.", duration)), nil
	case "left_mouse_down", "left_mouse_up":
		if name == "left_mouse_down" && p.held {
			return pixelError("mouse button already held, call left_mouse_up first", "state_conflict"), nil
		}
		gate, err := p.inputGate(ctx, gates)
		if err != nil {
			return NativeToolResult{}, err
		}
		if gate != nil && name != "left_mouse_up" {
			return *gate, nil
		}
		command, message := "mouse_down", "Mouse button pressed."
		if name == "left_mouse_up" {
			command, message = "mouse_up", "Mouse button released."
		}
		if _, err = p.native(ctx, command, object{}); err != nil {
			return NativeToolResult{}, err
		}
		p.held = name == "left_mouse_down"
		if gate != nil {
			return *gate, nil
		}
		return textResult(message), nil
	case "cursor_position":
		return p.cursor(ctx)
	case "open_application":
		return p.openApplication(ctx, args)
	case "switch_display":
		return p.switchDisplay(ctx, args)
	case "read_clipboard":
		raw, err := p.native(ctx, "read_clipboard", object{})
		if err != nil {
			return NativeToolResult{}, err
		}
		return textResult(`{"text":` + string(pixelJSONStringify(raw)) + `}`), nil
	case "write_clipboard":
		value, err := pixelStringJSON(args, "text")
		if err != nil {
			return pixelError(err.Error(), "bad_args"), nil
		}
		_, err = p.native(ctx, "write_clipboard", map[string]any{"text": value})
		return textResult("Clipboard written."), err
	case "computer_batch":
		return p.batch(ctx, args, gates)
	case "request_teach_access":
		if _, err := pixelString(args, "reason"); err != nil {
			return pixelError(err.Error(), "bad_args"), nil
		}
		var apps []json.RawMessage
		valid := len(args["apps"]) > 0 && string(args["apps"]) != "null" && json.Unmarshal(args["apps"], &apps) == nil
		for _, app := range apps {
			if _, ok := stringValue(app); !ok {
				valid = false
			}
		}
		if !valid {
			return pixelError(`"apps" must be an array of strings.`, "bad_args"), nil
		}
		return pixelError("Teach mode is not available in this session.", "feature_unavailable"), nil
	default:
		return pixelError(fmt.Sprintf("Unknown tool \"%s\".", name), "bad_args"), nil
	}
}
func (p *WindowsTools) mouseAction(ctx context.Context, name string, args object, gates WindowsSubGates) (NativeToolResult, error) {
	click := name == "left_click" || name == "double_click" || name == "triple_click" || name == "right_click" || name == "middle_click"
	if click || name == "left_click_drag" {
		if _, err := p.releaseHeld(ctx); err != nil {
			return NativeToolResult{}, err
		}
	}
	to, err := pixelCoordinate(args, "coordinate")
	if err != nil {
		return pixelError(err.Error(), "bad_args"), nil
	}
	var from *[2]float64
	if name == "left_click_drag" && args["start_coordinate"] != nil {
		value, err := pixelCoordinate(args, "start_coordinate")
		if err != nil {
			return pixelError(err.Error(), "bad_args"), nil
		}
		from = &value
	}
	var modifiers []json.RawMessage
	if click && args["text"] != nil {
		value, err := pixelStringJSON(args, "text")
		if err != nil {
			return pixelError("text must be a string", "bad_args"), nil
		}
		modifiers = pixelRawChord(value)
	}
	dx, dy := float64(0), float64(0)
	if name == "scroll" {
		dir, _ := stringValue(args["scroll_direction"])
		if dir != "up" && dir != "down" && dir != "left" && dir != "right" {
			return pixelError("scroll_direction must be 'up', 'down', 'left', or 'right'", "bad_args"), nil
		}
		amount, ok := pixelNumber(args["scroll_amount"])
		if !ok || amount != math.Trunc(amount) || amount < 0 {
			return pixelError("scroll_amount must be a non-negative int", "bad_args"), nil
		}
		if amount > 100 {
			return pixelError("scroll_amount exceeds maximum of 100", "bad_args"), nil
		}
		switch dir {
		case "up":
			dy = amount
		case "down":
			dy = -amount
		case "left":
			dx = -amount
		case "right":
			dx = amount
		}
	}
	gate, err := p.inputGate(ctx, gates)
	if err != nil {
		return NativeToolResult{}, err
	}
	if gate != nil {
		return *gate, nil
	}
	display, err := p.display(ctx, p.selected)
	if err != nil {
		return NativeToolResult{}, err
	}
	if click && gates.PixelValidation {
		if warning := p.validateClick(ctx, to); warning != "" {
			return textResult(warning), nil
		}
	}
	point := p.scale(to, display)
	command, message := "click", "Clicked."
	payload := map[string]any{"x": point["x"], "y": point["y"], "animate": gates.MouseAnimation}
	switch {
	case click:
		button, count := "left", 1
		switch name {
		case "right_click":
			button = "right"
		case "middle_click":
			button = "middle"
		case "double_click":
			count = 2
		case "triple_click":
			count = 3
		}
		payload["button"], payload["count"] = button, count
		if modifiers != nil {
			payload["modifiers"] = modifiers
		}
	case name == "mouse_move":
		command, message = "move_mouse", "Moved."
	case name == "scroll":
		command, message = "scroll", "Scrolled."
		payload["deltaX"], payload["deltaY"] = dx, dy
	case name == "left_click_drag":
		command, message = "drag", "Dragged."
		payload = map[string]any{"to": point, "animate": gates.MouseAnimation}
		if from != nil {
			payload["from"] = p.scale(*from, display)
		}
	}
	if _, err = p.native(ctx, command, payload); err != nil {
		return NativeToolResult{}, err
	}
	if name != "scroll" {
		err = p.options.Sleep(ctx, 50*time.Millisecond)
	}
	return textResult(message), err
}
func (p *WindowsTools) keyboardAction(ctx context.Context, name string, args object, gates WindowsSubGates) (NativeToolResult, error) {
	text, err := pixelString(args, "text")
	if err != nil {
		if name == "key" {
			return pixelError("text is required", "bad_args"), nil
		}
		return pixelError(err.Error(), "bad_args"), nil
	}
	repeat, duration := float64(1), float64(0)
	if name == "key" && args["repeat"] != nil {
		value, ok := pixelNumber(args["repeat"])
		if !ok || value != math.Trunc(value) || value < 1 {
			return pixelError("repeat must be a positive integer", "bad_args"), nil
		}
		if value > 100 {
			return pixelError("repeat exceeds maximum of 100", "bad_args"), nil
		}
		repeat = value
	}
	if name == "hold_key" {
		duration, err = pixelDuration(args)
		if err != nil {
			return pixelError(err.Error(), "bad_args"), nil
		}
	}
	gate, err := p.inputGate(ctx, gates)
	if err != nil {
		return NativeToolResult{}, err
	}
	if gate != nil {
		return *gate, nil
	}
	if name == "key" {
		_, err = p.native(ctx, "key", map[string]any{"keySequence": args["text"], "repeat": repeat})
		return textResult("Key pressed."), err
	}
	if name == "hold_key" {
		_, err = p.native(ctx, "hold_key", map[string]any{"keyNames": pixelRawChord(args["text"]), "durationMs": duration * 1000})
		return textResult("Key held."), err
	}
	nonASCII := false
	for _, runeValue := range text {
		if runeValue >= utf8.RuneSelf {
			nonASCII = true
			break
		}
	}
	if gates.ClipboardPasteMultiline && (strings.Contains(text, "\n") || nonASCII) {
		return p.paste(ctx, args["text"])
	}
	count := uniseg.GraphemeClusterCount(text)
	if ctx.Err() != nil {
		return pixelError(fmt.Sprintf("Typing aborted after 0 of %d graphemes (user interrupt).", count), ""), nil
	}
	if count > 0 {
		_, err = p.native(ctx, "type", map[string]any{"text": args["text"]})
	}
	return textResult(fmt.Sprintf("Typed %d grapheme(s).", count)), err
}
func (p *WindowsTools) paste(ctx context.Context, text json.RawMessage) (result NativeToolResult, err error) {
	saved, saveErr := p.native(ctx, "read_clipboard", object{})
	previous, previousErr := pixelExactStringJSON(saved)
	defer func() {
		if saveErr == nil && previousErr == nil {
			_, _ = p.native(context.WithoutCancel(ctx), "write_clipboard", map[string]any{"text": previous})
		}
	}()
	if _, err = p.native(ctx, "write_clipboard", map[string]any{"text": text}); err != nil {
		return NativeToolResult{}, err
	}
	if _, err = p.native(ctx, "key", map[string]any{"keySequence": "ctrl+v", "repeat": 1}); err != nil {
		return NativeToolResult{}, err
	}
	err = p.options.Sleep(ctx, 100*time.Millisecond)
	return textResult("Typed (via clipboard)."), err
}
