package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Source parses decimal prefixes at each actual connection/tool invocation.
// Zero, negative zero and NaN take the || default; negative values remain
// observable in errors even though the timer primitive clamps their delay.
func sourceTimeoutMilliseconds(value string, fallback float64) float64 {
	value = strings.TrimLeftFunc(value, protocol.IsJavaScriptWhitespace)
	end, first := 0, 0
	if len(value) > 0 && (value[0] == '+' || value[0] == '-') {
		end++
		first = end
	}
	for end < len(value) && value[end] >= '0' && value[end] <= '9' {
		end++
	}
	if end == first {
		return fallback
	}
	result, _ := strconv.ParseFloat(value[:end], 64)
	if result == 0 || math.IsNaN(result) {
		return fallback
	}
	return result
}

func sourceTimerDelay(milliseconds float64) time.Duration {
	if milliseconds < 1 || milliseconds > math.MaxInt32 || math.IsInf(milliseconds, 0) || math.IsNaN(milliseconds) {
		return time.Millisecond
	}
	return time.Duration(milliseconds) * time.Millisecond
}

func sourceMillisecondsString(value float64) string {
	if math.IsInf(value, 1) {
		return "Infinity"
	}
	if math.IsInf(value, -1) {
		return "-Infinity"
	}
	if math.IsNaN(value) {
		return "NaN"
	}
	// Reuse the canonical String(number), including exponent thresholds and
	// negative zero, rather than Go's different fixed/exponential formatting.
	encoded, _ := protocol.ExactJavaScriptStringJSON([]byte(strconv.FormatFloat(value, 'g', -1, 64)))
	var text string
	_ = json.Unmarshal(encoded, &text)
	return text
}

func sourceMillisecondsJSON(value float64) json.RawMessage {
	// JSON.stringify represents nonfinite original SDK diagnostic numbers as
	// null. Preserve the timeout property instead of dropping the entire data.
	if math.IsInf(value, 0) || math.IsNaN(value) {
		return json.RawMessage("null")
	}
	return raw(value)
}

func (c *Client) environmentValue(ctx context.Context, name string) (string, error) {
	if c.options.EnvironmentValue != nil {
		return c.options.EnvironmentValue(ctx, name)
	}
	return c.options.Environment[name], nil
}

type ToolTimeoutError struct {
	Server, Tool string
	Milliseconds float64
}

// fetchToolsForClient wraps non-telemetry SDK errors while preserving their
// message. Keep a distinct tool outlet without changing raw request RPC types.
type ToolProtocolError struct{ Cause error }

func (e *ToolProtocolError) Error() string { return e.Cause.Error() }
func (e *ToolProtocolError) Unwrap() error { return e.Cause }

func (e *ToolTimeoutError) Error() string {
	return fmt.Sprintf("MCP server \"%s\" tool \"%s\" timed out after %ss", e.Server, e.Tool, sourceMillisecondsString(math.Floor(e.Milliseconds/1000)))
}

// CallToolSource is the frozen callMCPTool Promise.race. Its own timer starts
// before the SDK timer. The SDK request still owns its separate cancellation;
// onSettled retains a caller's native reference until that request settles.
func (c *Client) CallToolSource(ctx context.Context, name json.RawMessage, input, meta protocol.JSONObject, onSettled func()) (protocol.JSONObject, error) {
	return c.CallToolSourceWithSent(ctx, name, input, meta, onSettled, nil)
}

func (c *Client) CallToolSourceWithSent(ctx context.Context, name json.RawMessage, input, meta protocol.JSONObject, onSettled func(), onSent func(error)) (protocol.JSONObject, error) {
	value, err := c.environmentValue(ctx, "MCP_TOOL_TIMEOUT")
	if err != nil {
		if onSent != nil {
			onSent(err)
		}
		if onSettled != nil {
			onSettled()
		}
		return protocol.JSONObject{}, err
	}
	milliseconds := sourceTimeoutMilliseconds(value, 100_000_000)
	var tool string
	_ = json.Unmarshal(name, &tool)
	failure := &ToolTimeoutError{c.options.Name, tool, milliseconds}
	timer := time.NewTimer(sourceTimerDelay(milliseconds))
	defer timer.Stop()
	type result struct {
		value protocol.JSONObject
		err   error
	}
	done := make(chan result, 1)
	var once sync.Once
	settle := func() {
		once.Do(func() {
			if onSettled != nil {
				onSettled()
			}
		})
	}
	go func() {
		defer settle()
		value, err := c.CallToolJSON(ctx, name, input, meta, RequestOptions{OnSent: onSent, TimeoutMilliseconds: &milliseconds, CancelReason: "AbortError: The operation was aborted."})
		done <- result{value, err}
	}()
	project := func(value result) (protocol.JSONObject, error) {
		if value.err != nil {
			return protocol.JSONObject{}, &ToolProtocolError{Cause: value.err}
		}
		return value.value, value.err
	}
	select {
	case <-timer.C:
		return protocol.JSONObject{}, failure
	case result := <-done:
		var rpc *RPCError
		// The external Source timer was registered before the SDK timer. Go
		// delivery can reorder ready timers, so preserve that original winner.
		if errors.As(result.err, &rpc) && rpc.Code == -32001 && rpc.Message == "Request timed out" {
			return protocol.JSONObject{}, failure
		}
		return project(result)
	case <-ctx.Done():
		// Let the actual SDK abort producer determine its original RPC type
		// and reason, rather than projecting a context error onto the tool.
		result := <-done
		return project(result)
	case <-c.ctx.Done():
		result := <-done
		return project(result)
	}
}
