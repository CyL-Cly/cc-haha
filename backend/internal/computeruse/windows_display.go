package computeruse

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"math"
	"sort"
)

type windowsDisplay struct {
	Width               float64  `json:"width"`
	Height              float64  `json:"height"`
	ScaleFactor         float64  `json:"scaleFactor"`
	OriginX             float64  `json:"originX"`
	OriginY             float64  `json:"originY"`
	DisplayID           float64  `json:"displayId"`
	ID                  *float64 `json:"id"`
	Label               *string  `json:"label"`
	Name                *string  `json:"name"`
	labelJSON, nameJSON json.RawMessage
}

func (d *windowsDisplay) UnmarshalJSON(raw []byte) error {
	type plain windowsDisplay
	var value plain
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	*d = windowsDisplay(value)
	var object protocol.JSONObject
	if err := object.UnmarshalJSON(raw); err != nil {
		return err
	}
	d.labelJSON, _ = pixelExactStringJSON(object.Get("label"))
	d.nameJSON, _ = pixelExactStringJSON(object.Get("name"))
	return nil
}

func normalizeWindowsDisplay(display windowsDisplay) windowsDisplay {
	if display.DisplayID == 0 && display.ID != nil {
		display.DisplayID = *display.ID
	}
	if display.Label == nil {
		display.Label = display.Name
		display.labelJSON = display.nameJSON
	}
	return display
}
func (p *WindowsTools) display(ctx context.Context, id *float64) (windowsDisplay, error) {
	payload := map[string]any{}
	if id != nil {
		payload["displayId"] = *id
	}
	raw, err := p.native(ctx, "get_display_size", payload)
	if err != nil {
		return windowsDisplay{}, err
	}
	var display windowsDisplay
	err = json.Unmarshal(raw, &display)
	return normalizeWindowsDisplay(display), err
}
func (p *WindowsTools) displays(ctx context.Context) ([]windowsDisplay, error) {
	raw, err := p.native(ctx, "list_displays", object{})
	if err != nil {
		return nil, err
	}
	var displays []windowsDisplay
	if err = json.Unmarshal(raw, &displays); err != nil {
		return nil, err
	}
	for i := range displays {
		displays[i] = normalizeWindowsDisplay(displays[i])
	}
	return displays, nil
}
func pixelRound(value float64) float64 { return math.Floor(value + .5) }
func windowsTargetSize(width, height, scale float64) (float64, float64) {
	width, height = pixelRound(width*scale), pixelRound(height*scale)
	return windowsResize(width, height)
}
func windowsResize(width, height float64) (float64, float64) {
	tokens := func(w, h float64) float64 { return (math.Floor((w-1)/28) + 1) * (math.Floor((h-1)/28) + 1) }
	if width <= 1568 && height <= 1568 && tokens(width, height) <= 1568 {
		return width, height
	}
	if height > width {
		h, w := windowsResize(height, width)
		return w, h
	}
	// Native display/capture dimensions are integers. Preserve the original
	// aspect ratio, tile budget and rounding in the binary search.
	lower, upper, aspect := float64(1), width, width/height
	for lower+1 < upper {
		middle := math.Floor((lower + upper) / 2)
		middleHeight := math.Max(pixelRound(middle/aspect), 1)
		if middle <= 1568 && tokens(middle, middleHeight) <= 1568 {
			lower = middle
		} else {
			upper = middle
		}
	}
	return lower, math.Max(pixelRound(lower/aspect), 1)
}
func (p *WindowsTools) scale(raw [2]float64, display windowsDisplay) map[string]float64 {
	if p.options.CoordinateMode == "normalized_0_100" {
		return map[string]float64{"x": pixelRound(raw[0]/100*display.Width) + display.OriginX, "y": pixelRound(raw[1]/100*display.Height) + display.OriginY}
	}
	if p.last != nil {
		return map[string]float64{"x": pixelRound(raw[0]*p.last.DisplayWidth/p.last.Width) + p.last.OriginX, "y": pixelRound(raw[1]*p.last.DisplayHeight/p.last.Height) + p.last.OriginY}
	}
	return map[string]float64{"x": pixelRound(raw[0]/display.ScaleFactor) + display.OriginX, "y": pixelRound(raw[1]/display.ScaleFactor) + display.OriginY}
}
func (p *WindowsTools) capture(ctx context.Context, id *float64, atomic bool) (*windowsScreenshot, object, error) {
	display, err := p.display(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	w, h := windowsTargetSize(display.Width, display.Height, display.ScaleFactor)
	payload := map[string]any{"targetWidth": w, "targetHeight": h, "jpegQuality": .75}
	command, displayKey := "screenshot", "displayId"
	if atomic {
		command, displayKey = "resolve_prepare_capture", "preferredDisplayId"
	}
	if id != nil {
		payload[displayKey] = *id
	}
	raw, err := p.native(ctx, command, payload)
	if err != nil {
		return nil, nil, err
	}
	var shot windowsScreenshot
	if err = json.Unmarshal(raw, &shot); err != nil {
		return nil, nil, err
	}
	fields, _ := decodeObject(raw)
	return &shot, fields, nil
}
func (p *WindowsTools) screenshot(ctx context.Context, gates WindowsSubGates, save bool) (NativeToolResult, error) {
	if !gates.AutoTargetDisplay && gates.HideBeforeAction {
		if _, err := p.native(ctx, "prepare_for_action", object{}); err != nil {
			return NativeToolResult{}, err
		}
	}
	shot, fields, err := p.capture(ctx, p.selected, gates.AutoTargetDisplay)
	if err != nil {
		return NativeToolResult{}, err
	}
	if gates.AutoTargetDisplay {
		id := shot.DisplayID
		p.selected = &id
		if !p.pinned {
			p.resolved = "all-supported-apps"
		}
		if captureError, err := pixelExactStringJSON(fields["captureError"]); err == nil {
			return pixelRawError("capture_failed", captureError), nil
		}
	} else {
		decoded, _ := base64.StdEncoding.DecodeString(shot.Base64)
		if len(decoded) < 1024 {
			shot, _, err = p.capture(ctx, p.selected, false)
			if err != nil {
				return NativeToolResult{}, err
			}
		}
	}
	result := NativeToolResult{Content: []ToolContent{}}
	if note := p.monitorNote(ctx, shot.DisplayID); note != nil {
		result.Content = append(result.Content, pixelRawMessage(note).Content...)
	}
	result.Content = append(result.Content, ToolContent{Type: "image", Data: shot.Base64, MimeType: "image/jpeg"})
	if save {
		p.last = shot
	}
	return result, nil
}
func windowsDisplayLabels(displays []windowsDisplay) map[float64]json.RawMessage {
	sorted := append([]windowsDisplay{}, displays...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].DisplayID < sorted[j].DisplayID })
	labels, counts := map[float64]json.RawMessage{}, map[string]int{}
	for _, display := range sorted {
		base := pixelQuoted(fmt.Sprintf("display %g", display.DisplayID))
		if display.labelJSON != nil {
			base = display.labelJSON
		}
		counts[string(base)]++
		if counts[string(base)] > 1 {
			labels[display.DisplayID], _ = protocol.JoinJavaScriptValuesJSON([]json.RawMessage{base, pixelQuoted(fmt.Sprintf(" (%d)", counts[string(base)]))}, "")
		} else {
			labels[display.DisplayID] = base
		}
	}
	return labels
}
func (p *WindowsTools) monitorNote(ctx context.Context, current float64) json.RawMessage {
	displays, err := p.displays(ctx)
	if err != nil || len(displays) < 2 {
		return nil
	}
	labels := windowsDisplayLabels(displays)
	name := func(id float64) json.RawMessage {
		if label, ok := labels[id]; ok {
			return label
		}
		return pixelQuoted(fmt.Sprintf("display %g", id))
	}
	others := []json.RawMessage{}
	for _, display := range displays {
		if display.DisplayID != current {
			label, _ := protocol.JoinJavaScriptValuesJSON([]json.RawMessage{pixelQuoted(`"`), name(display.DisplayID), pixelQuoted(`"`)}, "")
			others = append(others, label)
		}
	}
	last := float64(0)
	if p.last != nil {
		last = p.last.DisplayID
	}
	message := json.RawMessage(nil)
	if last == 0 {
		message, _ = protocol.JoinJavaScriptValuesJSON([]json.RawMessage{pixelQuoted(`This screenshot was taken on monitor "`), name(current), pixelQuoted(`".`)}, "")
	} else if last != current {
		message, _ = protocol.JoinJavaScriptValuesJSON([]json.RawMessage{pixelQuoted(`This screenshot was taken on monitor "`), name(current), pixelQuoted(`", which is different from your previous screenshot (taken on "`), name(last), pixelQuoted(`").`)}, "")
	}
	if message != nil && len(others) > 0 {
		otherNames, _ := protocol.JoinJavaScriptValuesJSON(others, ", ")
		message, _ = protocol.JoinJavaScriptValuesJSON([]json.RawMessage{message, pixelQuoted(" Other attached monitors: "), otherNames, pixelQuoted(". Use switch_display to capture a different monitor.")}, "")
	}
	return message
}
func (p *WindowsTools) zoom(ctx context.Context, args object) (NativeToolResult, error) {
	var region []json.RawMessage
	if json.Unmarshal(args["region"], &region) != nil || len(region) != 4 {
		return pixelError("region must be an array of length 4: [x0, y0, x1, y1]", "bad_args"), nil
	}
	values := make([]float64, 4)
	for i, raw := range region {
		value, ok := pixelNumber(raw)
		if !ok || value < 0 {
			return pixelError("region values must be non-negative numbers", "bad_args"), nil
		}
		values[i] = value
	}
	if values[2] <= values[0] {
		return pixelError("region x1 must be greater than x0", "bad_args"), nil
	}
	if values[3] <= values[1] {
		return pixelError("region y1 must be greater than y0", "bad_args"), nil
	}
	if p.last == nil {
		return pixelError("take a screenshot before zooming (region coords are relative to it)", "state_conflict"), nil
	}
	shot := p.last
	if values[2] > shot.Width || values[3] > shot.Height {
		return pixelError(fmt.Sprintf("region exceeds screenshot bounds (%g×%g)", shot.Width, shot.Height), "bad_args"), nil
	}
	xRatio, yRatio := shot.DisplayWidth/shot.Width, shot.DisplayHeight/shot.Height
	x, y, w, h := values[0]*xRatio, values[1]*yRatio, (values[2]-values[0])*xRatio, (values[3]-values[1])*yRatio
	display, err := p.display(ctx, &shot.DisplayID)
	if err != nil {
		return NativeToolResult{}, err
	}
	outW, outH := windowsTargetSize(w, h, display.ScaleFactor)
	raw, err := p.native(ctx, "zoom", map[string]any{"x": x, "y": y, "width": w, "height": h, "targetWidth": outW, "targetHeight": outH})
	if err != nil {
		return NativeToolResult{}, err
	}
	var zoom windowsScreenshot
	if err = json.Unmarshal(raw, &zoom); err != nil {
		return NativeToolResult{}, err
	}
	return NativeToolResult{Content: []ToolContent{{Type: "image", Data: zoom.Base64, MimeType: "image/jpeg"}}}, nil
}
func (p *WindowsTools) cursor(ctx context.Context) (NativeToolResult, error) {
	raw, err := p.native(ctx, "cursor_position", object{})
	if err != nil {
		return NativeToolResult{}, err
	}
	var position struct{ X, Y float64 }
	if err = json.Unmarshal(raw, &position); err != nil {
		return NativeToolResult{}, err
	}
	result := struct {
		X               float64 `json:"x"`
		Y               float64 `json:"y"`
		CoordinateSpace string  `json:"coordinateSpace"`
		Note            string  `json:"note,omitempty"`
	}{position.X, position.Y, "logical_points", "take a screenshot first for image-pixel coordinates"}
	if shot := p.last; shot != nil {
		x, y := position.X-shot.OriginX, position.Y-shot.OriginY
		if x < 0 || y < 0 || x > shot.DisplayWidth || y > shot.DisplayHeight {
			result.Note = "cursor is on a different monitor than your last screenshot; take a fresh screenshot"
		} else {
			result.X, result.Y = pixelRound(x*shot.Width/shot.DisplayWidth), pixelRound(y*shot.Height/shot.DisplayHeight)
			result.CoordinateSpace, result.Note = "image_pixels", ""
		}
	}
	return pixelJSON(result), nil
}
func (p *WindowsTools) openApplication(ctx context.Context, args object) (NativeToolResult, error) {
	app, err := pixelStringJSON(args, "app")
	if err != nil {
		return pixelError(err.Error(), "bad_args"), nil
	}
	raw, err := p.native(ctx, "list_installed_apps", object{})
	if err != nil {
		return NativeToolResult{}, err
	}
	var apps []protocol.JSONObject
	if err = json.Unmarshal(raw, &apps); err != nil {
		return NativeToolResult{}, err
	}
	wanted, match := pixelLowerJSON(app, true), json.RawMessage(nil)
	for _, candidate := range apps {
		if string(pixelLowerJSON(candidate.Get("bundleId"), false)) == string(wanted) || string(pixelLowerJSON(candidate.Get("displayName"), false)) == string(wanted) {
			match = candidate.Get("bundleId")
			break
		}
	}
	if match == nil || string(match) == `""` {
		return pixelRawError("bad_args", pixelQuoted(`"`), app, pixelQuoted(`" was not found in the installed application inventory.`)), nil
	}
	if _, err = p.native(ctx, "open_app", object{"bundleId": match}); err != nil {
		return NativeToolResult{}, err
	}
	parts := []json.RawMessage{pixelQuoted(`Opened "`), app, pixelQuoted(`".`)}
	if displays, err := p.displays(ctx); err == nil && len(displays) >= 2 {
		parts = append(parts, pixelQuoted(" If it isn't visible in the next screenshot, it may have opened on a different monitor — use switch_display to check."))
	}
	return pixelRawMessage(parts...), nil
}

func (p *WindowsTools) switchDisplay(ctx context.Context, args object) (NativeToolResult, error) {
	value, err := pixelStringJSON(args, "display")
	if err != nil {
		return pixelError(err.Error(), "bad_args"), nil
	}
	wanted := pixelLowerJSON(value, false)
	if string(wanted) == `"auto"` {
		p.selected, p.pinned = nil, false
		return textResult("Returned to automatic monitor selection. Call screenshot to continue."), nil
	}
	displays, err := p.displays(ctx)
	if err != nil {
		return pixelRawError("display_error", pixelQuoted("Failed to enumerate displays: Error: "), nativeErrorMessageJSON(err)), nil
	}
	if len(displays) < 2 {
		return pixelError("Only one monitor is connected. There is nothing to switch to.", "bad_args"), nil
	}
	labels, available := windowsDisplayLabels(displays), []json.RawMessage{}
	for _, display := range displays {
		label := labels[display.DisplayID]
		if string(pixelLowerJSON(label, false)) == string(wanted) {
			id := display.DisplayID
			p.selected, p.pinned = &id, true
			return pixelRawMessage(pixelQuoted(`Switched to monitor "`), label, pixelQuoted(`". Call screenshot to see it.`)), nil
		}
		quoted, _ := protocol.JoinJavaScriptValuesJSON([]json.RawMessage{pixelQuoted(`"`), label, pixelQuoted(`"`)}, "")
		available = append(available, quoted)
	}
	names, _ := protocol.JoinJavaScriptValuesJSON(available, ", ")
	return pixelRawError("bad_args", pixelQuoted(`No monitor named "`), value, pixelQuoted(`" is connected. Available monitors: `), names, pixelQuoted(".")), nil
}
