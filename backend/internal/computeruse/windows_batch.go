package computeruse

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

var windowsBatchActions = []string{"key", "type", "mouse_move", "left_click", "left_click_drag", "right_click", "middle_click", "double_click", "triple_click", "scroll", "hold_key", "screenshot", "cursor_position", "left_mouse_down", "left_mouse_up", "wait"}

type windowsBatchResult struct {
	Action string `json:"action"`
	OK     bool   `json:"ok"`
	Output string `json:"output"`
}

func (p *WindowsTools) batch(ctx context.Context, args object, gates WindowsSubGates) (NativeToolResult, error) {
	var actions []json.RawMessage
	if json.Unmarshal(args["actions"], &actions) != nil || len(actions) == 0 {
		return pixelError("actions must be a non-empty array", "bad_args"), nil
	}
	rows, names := []object{}, []string{}
	for i, raw := range actions {
		row, ok := decodeObject(raw)
		if !ok {
			return pixelError(fmt.Sprintf("actions[%d] must be an object", i), "bad_args"), nil
		}
		name, ok := stringValue(row["action"])
		if !ok {
			return pixelError(fmt.Sprintf("actions[%d].action must be a string", i), "bad_args"), nil
		}
		allowed := false
		for _, candidate := range windowsBatchActions {
			if name == candidate {
				allowed = true
				break
			}
		}
		if !allowed {
			return pixelError(fmt.Sprintf("actions[%d].action=\"%s\" is not allowed in a batch. Allowed: %s.", i, name, strings.Join(windowsBatchActions, ", ")), "bad_args"), nil
		}
		rows, names = append(rows, row), append(names, name)
	}
	if gates.HideBeforeAction {
		if _, err := p.native(ctx, "prepare_for_action", object{}); err != nil {
			return NativeToolResult{}, err
		}
	}
	innerGates := gates
	innerGates.HideBeforeAction, innerGates.PixelValidation, innerGates.AutoTargetDisplay = false, false, false
	results := []windowsBatchResult{}
	for i, row := range rows {
		if ctx.Err() != nil {
			if _, err := p.releaseHeld(context.WithoutCancel(ctx)); err != nil {
				return NativeToolResult{}, err
			}
			return pixelError(fmt.Sprintf("Batch aborted after %d of %d actions (user interrupt).", len(results), len(rows)), ""), nil
		}
		if i > 0 {
			if err := p.options.Sleep(ctx, 10*time.Millisecond); err != nil {
				return NativeToolResult{}, err
			}
		}
		var inner NativeToolResult
		var err error
		if names[i] == "screenshot" {
			inner, err = p.screenshot(ctx, innerGates, false)
		} else {
			inner, err = p.dispatch(ctx, names[i], row, innerGates)
		}
		if err != nil {
			return NativeToolResult{}, err
		}
		text := ""
		if len(inner.Content) > 0 && inner.Content[0].Type == "text" {
			text = inner.Content[0].Text
		}
		result := windowsBatchResult{Action: names[i], OK: !inner.IsError, Output: text}
		if inner.IsError {
			if _, err := p.releaseHeld(ctx); err != nil {
				return NativeToolResult{}, err
			}
			output := pixelJSON(struct {
				Completed []windowsBatchResult `json:"completed"`
				Failed    windowsBatchResult   `json:"failed"`
				Remaining int                  `json:"remaining"`
			}{results, result, len(rows) - i - 1})
			output.Telemetry = inner.Telemetry
			return output, nil
		}
		results = append(results, result)
	}
	return pixelJSON(struct {
		Completed []windowsBatchResult `json:"completed"`
	}{results}), nil
}

// The Source CLI injects a null crop primitive. Hosts that have a decoder can
// supply one without changing the exact crop geometry or equality policy.
type WindowsCropRect struct{ X, Y, Width, Height int }

func (p *WindowsTools) validateClick(ctx context.Context, point [2]float64) (warning string) {
	if p.last == nil {
		return ""
	}
	last := p.last
	fresh, _, err := p.capture(ctx, &last.DisplayID, false)
	if err != nil {
		return ""
	}
	x, y := point[0], point[1]
	if p.options.CoordinateMode == "pixels" {
		x, y = x/last.Width*100, y/last.Height*100
	}
	x, y = math.Max(0, math.Min(100, x)), math.Max(0, math.Min(100, y))
	cropX, cropY := math.Max(0, pixelRound(x/100*fresh.Width)-4), math.Max(0, pixelRound(y/100*fresh.Height)-4)
	cropW, cropH := math.Min(9, fresh.Width-cropX), math.Min(9, fresh.Height-cropY)
	matched := false
	if fresh.Width != 0 && fresh.Height != 0 && cropW > 0 && cropH > 0 && p.options.CropRawPatch != nil {
		rect := WindowsCropRect{int(cropX), int(cropY), int(cropW), int(cropH)}
		before, beforeErr := p.options.CropRawPatch(last.Base64, rect)
		after, afterErr := p.options.CropRawPatch(fresh.Base64, rect)
		if beforeErr != nil || afterErr != nil {
			return ""
		}
		matched = before != nil && after != nil && bytes.Equal(before, after)
	}
	if matched {
		return ""
	}
	return "Screen content at the target location changed since the last screenshot. Take a new screenshot before clicking."
}
