package computeruse

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type parsedTool struct {
	name, requestedApp string
	target, payload    object
	mutating           bool
	steps              []*parsedTool
}
type toolArgumentError struct{ message, kind string }

func (err *toolArgumentError) Error() string { return err.message }
func badArgument(message string) error       { return &toolArgumentError{message, "bad_args"} }
func jsString(raw json.RawMessage) string {
	value, err := protocol.JavaScriptStringJSON(raw)
	if err != nil {
		return "undefined"
	}
	text, _ := stringValue(value)
	return text
}
func absent(raw json.RawMessage) bool {
	return len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}
func optionalNumber(raw json.RawMessage, key string) (*float64, error) {
	if absent(raw) {
		return nil, nil
	}
	text := trim(string(raw))
	if text == "" || !(text[0] == '-' || text[0] >= '0' && text[0] <= '9') {
		return nil, badArgument(key + " must be a number")
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return nil, badArgument(key + " must be a number")
	}
	return &value, nil
}
func coordinate(raw json.RawMessage, key string) (*float64, error) {
	value, err := optionalNumber(raw, key)
	if err != nil {
		return nil, err
	}
	if value != nil && *value < 0 {
		return nil, badArgument(fmt.Sprintf("%s must be within the screenshot (>= 0), got %s", key, jsString(raw)))
	}
	return value, nil
}
func requireString(args object, key string, emptyOK bool) (string, error) {
	value, ok := stringValue(args[key])
	if !ok || (!emptyOK && value == "") {
		if emptyOK {
			return "", badArgument(`Missing required string "` + key + `"`)
		}
		return "", badArgument(`Missing or empty required string "` + key + `"`)
	}
	return value, nil
}

var elementPattern = regexp.MustCompile(`^g(?:0|[1-9][0-9]*):(?:0|[1-9][0-9]*)$`)

func elementHandle(raw json.RawMessage) (*string, error) {
	if absent(raw) {
		return nil, nil
	}
	value, ok := stringValue(raw)
	if !ok {
		return nil, badArgument(`element_index must be an opaque handle string from get_app_state (e.g. "g17:4")`)
	}
	if value == "" {
		return nil, nil
	}
	if !elementPattern.MatchString(value) {
		return nil, badArgument(`element_index must be an opaque handle from get_app_state (e.g. "g17:4"), got "` + value + `"`)
	}
	return &value, nil
}
func aliasConflict(args object, key, alias string) error {
	if args[key] != nil && args[alias] != nil {
		return badArgument(`Pass either "` + key + `" or "` + alias + `", not both`)
	}
	return nil
}
func optionalBool(raw json.RawMessage, key string) (*bool, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	value, ok := boolean(raw)
	if !ok {
		return nil, badArgument(key + " must be a boolean")
	}
	return &value, nil
}

var buttons = map[string]string{"left": "left", "middle": "middle", "right": "right", "back": "back", "forward": "forward", "l": "left", "m": "middle", "r": "right"}

func mouseButton(raw json.RawMessage) (*string, error) {
	if absent(raw) {
		return nil, nil
	}
	if text, ok := stringValue(raw); ok {
		if value, found := buttons[strings.ToLower(text)]; found {
			return &value, nil
		}
		return nil, badArgument(`Unknown mouse_button "` + text + `"`)
	}
	if number, err := optionalNumber(raw, "mouse_button"); err == nil && number != nil {
		values := []string{"left", "middle", "right", "back", "forward"}
		if *number >= 1 && *number <= 5 && *number == math.Trunc(*number) {
			value := values[int(*number)-1]
			return &value, nil
		}
		return nil, badArgument("mouse_button " + jsString(raw) + " is out of range (1..5)")
	}
	return nil, badArgument("mouse_button must be a string or 1..5")
}

var directions = map[string]string{"up": "up", "down": "down", "left": "left", "right": "right", "u": "up", "d": "down", "l": "left", "r": "right"}

func direction(raw json.RawMessage) (string, error) {
	text, ok := stringValue(raw)
	if ok {
		if value, found := directions[strings.ToLower(text)]; found {
			return value, nil
		}
	}
	return "", badArgument("direction must be one of up/down/left/right")
}
func point(args object, nested, keyX, keyY string) (object, error) {
	if value, ok := decodeObject(args[nested]); ok {
		x, err := coordinate(value["x"], nested+".x")
		if err != nil {
			return nil, err
		}
		y, err := coordinate(value["y"], nested+".y")
		if err != nil {
			return nil, err
		}
		if x != nil && y != nil {
			return object{"x": encode(*x), "y": encode(*y)}, nil
		}
	}
	x, err := coordinate(args[keyX], keyX)
	if err != nil {
		return nil, err
	}
	y, err := coordinate(args[keyY], keyY)
	if err != nil {
		return nil, err
	}
	if x != nil && y != nil {
		return object{"x": encode(*x), "y": encode(*y)}, nil
	}
	return nil, badArgument("Expected " + nested + "{x,y} or " + keyX + "/" + keyY)
}

var numericApp = regexp.MustCompile(`^[0-9]+$`)

func appTarget(args object) (object, string, error) {
	app, ok := stringValue(args["app"])
	app = trim(app)
	if !ok || app == "" {
		return nil, "", badArgument(`Missing required string "app" — name the application to target`)
	}
	if numericApp.MatchString(app) {
		value, err := strconv.ParseFloat(app, 64)
		if err != nil || math.IsInf(value, 0) {
			return object{"pid": encode(nil)}, app, nil
		}
		return object{"pid": encode(value)}, app, nil
	}
	return object{"app": encode(app)}, app, nil
}

var mutableTools = map[string]bool{"click": true, "perform_secondary_action": true, "set_value": true, "select_text": true, "scroll": true, "drag": true, "press_key": true, "type_text": true, "paste": true}

func knownNativeTool(name string) bool {
	return mutableTools[name] || name == "get_app_state" || name == "list_apps" || name == "sequence"
}

var jsSpacePlus = regexp.MustCompile(`[\t\n\v\f\r \x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]*\+[\t\n\v\f\r \x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]*`)
var jsSpace = regexp.MustCompile(`[\t\n\v\f\r \x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]+`)

func chords(key string) []string {
	return jsSpace.Split(trim(jsSpacePlus.ReplaceAllString(key, "+")), -1)
}
func parseNativeTool(name string, args object, flags object, platform string) (*parsedTool, error) {
	if !knownNativeTool(name) {
		return nil, badArgument(`Unknown computer-use tool "` + name + `".`)
	}
	request := &parsedTool{name: name, payload: object{}, mutating: mutableTools[name]}
	if name == "list_apps" {
		return request, nil
	}
	if name == "sequence" {
		if platform != "darwin" {
			return nil, badArgument("sequence is only available on macOS")
		}
		for key := range args {
			if key != "app" && key != "steps" {
				return nil, badArgument("sequence accepts only app and steps")
			}
		}
	}
	target, app, err := appTarget(args)
	if err != nil {
		return nil, err
	}
	request.target, request.requestedApp = target, app
	put := func(key string, value any) { request.payload[key] = encode(value) }
	switch name {
	case "get_app_state":
		if err = aliasConflict(args, "disableDiff", "disable_diff"); err != nil {
			return nil, err
		}
		value, err := optionalBool(args["disableDiff"], "disableDiff")
		if err != nil {
			return nil, err
		}
		if value == nil {
			value, err = optionalBool(args["disable_diff"], "disable_diff")
			if err != nil {
				return nil, err
			}
		}
		if value != nil {
			put("disableDiff", *value)
		}
	case "click", "scroll":
		index, err := elementHandle(args["element_index"])
		if err != nil {
			return nil, err
		}
		x, err := coordinate(args["x"], "x")
		if err != nil {
			return nil, err
		}
		y, err := coordinate(args["y"], "y")
		if err != nil {
			return nil, err
		}
		if name == "click" {
			if (x == nil) != (y == nil) {
				return nil, badArgument("click x and y must be provided together")
			}
			if index == nil && x == nil {
				return nil, badArgument("click requires element_index, or both x and y")
			}
		} else if index == nil && (x == nil || y == nil) {
			return nil, badArgument("scroll requires element_index, or both x and y")
		}
		if index != nil {
			put("index", *index)
		}
		if x != nil {
			put("x", *x)
		}
		if y != nil {
			put("y", *y)
		}
		if name == "click" {
			count, err := optionalNumber(args["click_count"], "click_count")
			if err != nil {
				return nil, err
			}
			if count != nil {
				if *count != math.Trunc(*count) || *count < 1 || *count > 3 {
					return nil, badArgument("click_count must be an integer from 1 to 3")
				}
				put("click_count", *count)
			}
			button, err := mouseButton(args["mouse_button"])
			if err != nil {
				return nil, err
			}
			if button != nil {
				put("button", *button)
			}
		} else {
			direction, err := direction(args["direction"])
			if err != nil {
				return nil, err
			}
			put("direction", direction)
			if err = aliasConflict(args, "pages", "amount"); err != nil {
				return nil, err
			}
			pages, err := optionalNumber(args["pages"], "pages")
			if err != nil {
				return nil, err
			}
			if pages == nil {
				pages, err = optionalNumber(args["amount"], "amount")
				if err != nil {
					return nil, err
				}
			}
			if pages != nil {
				if *pages <= 0 || *pages > 10 {
					return nil, badArgument("pages must be greater than 0 and at most 10")
				}
				put("pages", *pages)
			}
		}
	case "set_value", "perform_secondary_action", "select_text":
		index, err := elementHandle(args["element_index"])
		if err != nil {
			return nil, err
		}
		if index == nil {
			return nil, badArgument(name + " requires element_index")
		}
		put("index", *index)
		key := "value"
		if name == "perform_secondary_action" {
			key = "action"
		} else if name == "select_text" {
			key = "text"
		}
		value, err := requireString(args, key, name == "set_value")
		if err != nil {
			return nil, err
		}
		put(key, value)
		if name == "select_text" {
			for _, key := range []string{"prefix", "suffix"} {
				if text, ok := stringValue(args[key]); ok {
					put(key, text)
				}
			}
			if err = aliasConflict(args, "selection", "selection_type"); err != nil {
				return nil, err
			}
			raw := args["selection"]
			if absent(raw) {
				raw = args["selection_type"]
			}
			selection := "text"
			if !absent(raw) {
				text, ok := stringValue(raw)
				if !ok || (text != "text" && text != "cursor_before" && text != "cursor_after") {
					return nil, badArgument("Invalid text selection mode: " + jsString(raw))
				}
				selection = text
			}
			put("selection", selection)
		}
	case "drag":
		nested := args["from"] != nil || args["to"] != nil
		flat := args["from_x"] != nil || args["from_y"] != nil || args["to_x"] != nil || args["to_y"] != nil
		if nested && flat {
			return nil, badArgument(`Pass either "from_x/from_y/to_x/to_y" or "from/to", not both`)
		}
		from, err := point(args, "from", "from_x", "from_y")
		if err != nil {
			return nil, err
		}
		to, err := point(args, "to", "to_x", "to_y")
		if err != nil {
			return nil, err
		}
		put("from", from)
		put("to", to)
		button, err := mouseButton(args["mouse_button"])
		if err != nil {
			return nil, err
		}
		if button != nil {
			put("button", *button)
		}
	case "press_key":
		key, err := requireString(args, "key", false)
		if err != nil {
			return nil, err
		}
		if platform == "darwin" && len(chords(key)) > 128 {
			return nil, badArgument("press_key supports at most 128 key chords; use shorter sequences")
		}
		granted, _ := boolean(flags["systemKeyCombos"])
		if !granted && IsSystemKeyCombo(key, platform) {
			return nil, &toolArgumentError{`"` + key + `" crosses application boundaries or can terminate apps. Enable system key combinations in Computer Use settings, then retry.`, "grant_flag_required"}
		}
		put("key", key)
		put("systemKeyCombos", granted)
	case "type_text", "paste":
		text, err := requireString(args, "text", true)
		if err != nil {
			return nil, err
		}
		put("text", text)
		if name == "paste" {
			format, err := requireString(args, "format", false)
			if err != nil {
				return nil, err
			}
			if format != "text" && format != "md" && format != "html" {
				return nil, badArgument(`paste format must be "text", "md", or "html"`)
			}
			put("format", format)
		}
	case "sequence":
		if platform != "darwin" {
			return nil, badArgument("sequence is only available on macOS")
		}
		for key := range args {
			if key != "app" && key != "steps" {
				return nil, badArgument("sequence accepts only app and steps")
			}
		}
		var steps []json.RawMessage
		if absent(args["steps"]) || json.Unmarshal(args["steps"], &steps) != nil || len(steps) < 1 || len(steps) > 256 {
			return nil, badArgument("sequence requires 1–256 steps")
		}
		chordCount := 0
		for index, raw := range steps {
			step, _ := decodeObject(raw)
			tool, ok := stringValue(step["tool"])
			if !ok || !mutableTools[tool] {
				return nil, badArgument(fmt.Sprintf("sequence step %d: expected an existing mutation tool", index))
			}
			for key := range step {
				if key != "tool" && !sequenceArguments[tool][key] {
					return nil, badArgument(fmt.Sprintf("sequence step %d: unknown argument or per-step app override", index))
				}
			}
			if tool == "press_key" {
				if key, ok := stringValue(step["key"]); ok {
					count := len(chords(key))
					chordCount += count
					if count > 128 || chordCount > 2048 {
						return nil, badArgument("sequence exceeds its keyboard chord budget")
					}
				}
			}
			step = copyObject(step)
			delete(step, "tool")
			step["app"] = args["app"]
			parsed, err := parseNativeTool(tool, step, flags, platform)
			if err != nil {
				return nil, err
			}
			request.steps = append(request.steps, parsed)
		}
		request.mutating = true
	}
	return request, nil
}

var sequenceArguments = map[string]map[string]bool{
	"click": {"element_index": true, "x": true, "y": true, "click_count": true, "mouse_button": true}, "perform_secondary_action": {"element_index": true, "action": true}, "set_value": {"element_index": true, "value": true}, "select_text": {"element_index": true, "text": true, "prefix": true, "suffix": true, "selection": true, "selection_type": true}, "scroll": {"element_index": true, "direction": true, "pages": true, "x": true, "y": true}, "drag": {"from_x": true, "from_y": true, "to_x": true, "to_y": true, "from": true, "to": true, "mouse_button": true}, "press_key": {"key": true}, "type_text": {"text": true}, "paste": {"text": true, "format": true},
}
