package computeruse

import (
	"context"
	"fmt"
	"math"
	"time"
)

func (tools *NativeTools) runSequence(ctx context.Context, request *parsedTool, target object) NativeToolResult {
	started := tools.now()
	completed := 0
	durations := []int64{}
	milliseconds := func(duration time.Duration) int64 {
		return int64(math.Round(float64(duration) / float64(time.Millisecond)))
	}
	summary := func(status string, extra map[string]any) map[string]any {
		result := map[string]any{"status": status, "completedSteps": completed, "totalSteps": len(request.steps), "elapsedMs": milliseconds(tools.now().Sub(started)), "stepDurationsMs": append([]int64{}, durations...)}
		for key, value := range extra {
			result[key] = value
		}
		return result
	}
	stop := func(observationSkipped bool) *NativeToolResult {
		status := ""
		if ctx.Err() != nil {
			status = "cancelled"
		} else if tools.Disabled(ctx) {
			status = "disabled"
		} else if tools.now().Sub(started) >= 60*time.Second {
			status = "deadline_exceeded"
		}
		if status == "" {
			return nil
		}
		result := toolError(fmt.Sprintf("Sequence %s; stopped after %d completed steps. No further actions dispatched. Inspect the current state before continuing; do not replay completed steps.", status, completed), "other")
		result.StructuredContent = summary(status, map[string]any{"resultUnknown": false, "observationSkipped": observationSkipped, "observationDelivered": false})
		return &result
	}
	for _, step := range request.steps {
		if stopped := stop(true); stopped != nil {
			return *stopped
		}
		stepStarted := tools.now()
		result, err := tools.runTool(ctx, step, target)
		durations = append(durations, milliseconds(tools.now().Sub(stepStarted)))
		if err != nil {
			result = toolError(fmt.Sprintf("Sequence step %d failed: %s. Inspect the app before retrying; completed steps were not rolled back.", completed, err.Error()), "executor_threw")
			result.StructuredContent = summary("failed", map[string]any{"failedStepIndex": completed, "resultUnknown": true, "observationSkipped": true})
			return result
		}
		if result.IsError {
			result.StructuredContent = summary("failed", map[string]any{"failedStepIndex": completed, "resultUnknown": true, "observationSkipped": true})
			return result
		}
		completed++
		if stopped := stop(true); stopped != nil {
			return *stopped
		}
	}
	state, err := tools.options.NativeCall(ctx, "get_app_state", encode(target))
	if err != nil {
		result := toolError(fmt.Sprintf("All %d sequence steps completed, but final observation failed: %s. Call get_app_state before continuing; do not replay completed steps.", completed, err.Error()), "capture_failed")
		result.StructuredContent = summary("observation_failed", map[string]any{"resultUnknown": false, "observationSkipped": false})
		return result
	}
	if stopped := stop(false); stopped != nil {
		return *stopped
	}
	result, err := tools.frameState(state, request.requestedApp)
	if err != nil {
		result = toolError(fmt.Sprintf("All %d sequence steps completed, but final observation failed: %s. Call get_app_state before continuing; do not replay completed steps.", completed, err.Error()), "capture_failed")
		result.StructuredContent = summary("observation_failed", map[string]any{"resultUnknown": false, "observationSkipped": false})
		return result
	}
	hasImage := false
	for _, block := range result.Content {
		if block.Type == "image" && block.Data != "" {
			hasImage = true
		}
	}
	if !hasImage {
		result.IsError = true
		result.Telemetry = map[string]string{"error_kind": "capture_failed"}
		result.StructuredContent = summary("observation_failed", map[string]any{"resultUnknown": false})
		result.Content = append([]ToolContent{{Type: "text", Text: fmt.Sprintf("All %d steps completed, but the final screenshot is unavailable. The AX state alone does not verify the visual result. Call get_app_state before continuing; do not replay completed steps.", completed)}}, result.Content...)
		return result
	}
	result.StructuredContent = summary("completed", map[string]any{"resultUnknown": false})
	result.Content = append([]ToolContent{{Type: "text", Text: fmt.Sprintf("Sequence completed %d steps. The following is the actual final app state.", completed)}}, result.Content...)
	return result
}
