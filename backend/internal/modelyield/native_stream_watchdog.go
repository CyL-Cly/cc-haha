package modelyield

import (
	"encoding/json"
	"math"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
)

// All timer policy and stream state stay on the same owner as Next. A timer
// releases the actual SDK body, never the user's original cancellation signal.
type nativeStreamWatchdog struct {
	scope                   *cooperative.Scope
	stream                  *provider.OwnedMessageStream
	state                   *streamhistory.WatchdogState
	idle, first, tool       float64
	current                 float64
	idleTimer, maximumTimer *cooperative.TimerTicket
	toolTimers              map[int]*cooperative.TimerTicket
	failure                 *streamhistory.WatchdogTimeoutError
}

func watchdogNumber(env map[string]string, name string, fallback float64) float64 {
	if env[name] == "" {
		return fallback
	}
	n := retrypolicy.DefaultMaximum(env[name])
	if math.IsNaN(n) || n == 0 {
		return fallback
	}
	return n
}
func watchdogDuration(ms float64) time.Duration {
	// Bun's timers clamp invalid, negative and overflowing delays to 1ms.
	if math.IsInf(ms, 0) || ms < 1 || ms > math.MaxInt32 {
		return time.Millisecond
	}
	return time.Duration(ms * float64(time.Millisecond))
}
func newNativeStreamWatchdog(scope *cooperative.Scope, stream *provider.OwnedMessageStream, raw *provider.RawMessageStream, env map[string]string) (*nativeStreamWatchdog, error) {
	if !streamPolicyTruthy(env["CLAUDE_ENABLE_STREAM_WATCHDOG"]) {
		return nil, nil
	}
	w := &nativeStreamWatchdog{scope: scope, stream: stream, state: streamhistory.NewWatchdogState(), toolTimers: map[int]*cooperative.TimerTicket{}}
	w.idle = watchdogNumber(env, "CLAUDE_STREAM_IDLE_TIMEOUT_MS", 90000)
	w.first = watchdogNumber(env, "CLAUDE_STREAM_FIRST_TOKEN_TIMEOUT_MS", watchdogNumber(env, "API_TIMEOUT_MS", w.idle))
	w.first = providerproxy.ResolveOpenAICodexFirstTokenTimeout(raw.Headers.Get(providerproxy.OpenAICodexStreamMarkerHeader), w.first, env["CC_HAHA_OPENAI_OAUTH_FIRST_TOKEN_TIMEOUT_MS"])
	w.tool = watchdogNumber(env, "CLAUDE_STREAM_TOOL_INPUT_MAX_DURATION_MS", 0)
	w.current = w.first
	if err := w.armIdle(); err != nil {
		w.close()
		return nil, err
	}
	maximum := watchdogNumber(env, "CLAUDE_STREAM_MAX_DURATION_MS", 0)
	if maximum > 0 {
		var err error
		w.maximumTimer, err = scope.ArmTimer(watchdogDuration(maximum), func() { w.fire("max_duration", maximum) })
		if err != nil {
			w.close()
			return nil, err
		}
	}
	return w, nil
}
func (w *nativeStreamWatchdog) fire(reason string, ms float64) {
	if w.failure != nil {
		return
	}
	w.failure = &streamhistory.WatchdogTimeoutError{Reason: reason, TimeoutMS: ms, Snapshot: w.state.Value.Clone()}
	_ = w.stream.AbortSDK(w.scope)
}
func (w *nativeStreamWatchdog) armIdle() error {
	if w.idleTimer != nil {
		if err := w.scope.ClearTimer(w.idleTimer); err != nil {
			return err
		}
	}
	ms := w.current
	var err error
	w.idleTimer, err = w.scope.ArmTimer(watchdogDuration(ms), func() { w.fire("idle", ms) })
	return err
}
func (w *nativeStreamWatchdog) stopTool(index int) {
	if ticket := w.toolTimers[index]; ticket != nil {
		_ = w.scope.ClearTimer(ticket)
		delete(w.toolTimers, index)
	}
}
func (w *nativeStreamWatchdog) armTool(index int) error {
	w.stopTool(index)
	if w.tool <= 0 {
		return nil
	}
	ticket, err := w.scope.ArmTimer(watchdogDuration(w.tool), func() { delete(w.toolTimers, index); w.fire("tool_input_duration", w.tool) })
	if err == nil {
		w.toolTimers[index] = ticket
	}
	return err
}
func (w *nativeStreamWatchdog) record(raw json.RawMessage) error {
	if w == nil {
		return nil
	}
	first, err := w.state.Record(raw)
	if err != nil {
		return err
	}
	if first {
		w.current = w.idle
	}
	if err = w.armIdle(); err != nil {
		return err
	}
	var event protocol.JSONObject
	if err = event.UnmarshalJSON(raw); err != nil {
		return err
	}
	index := 0
	if json.Unmarshal(event.Get("index"), &index) != nil {
		return nil
	}
	switch event.String("type") {
	case "content_block_start":
		var block protocol.JSONObject
		_ = block.UnmarshalJSON(event.Get("content_block"))
		if block.String("type") == "tool_use" {
			return w.armTool(index)
		}
	case "content_block_delta":
		var delta protocol.JSONObject
		_ = delta.UnmarshalJSON(event.Get("delta"))
		if delta.String("type") == "input_json_delta" && w.toolTimers[index] != nil {
			if units, e := protocol.DecodeJavaScriptJSONString(delta.Get("partial_json")); e == nil && len(units) > 0 {
				return w.armTool(index)
			}
		}
	case "content_block_stop":
		w.stopTool(index)
	}
	return nil
}
func (w *nativeStreamWatchdog) close() {
	if w == nil {
		return
	}
	if w.idleTimer != nil {
		_ = w.scope.ClearTimer(w.idleTimer)
	}
	if w.maximumTimer != nil {
		_ = w.scope.ClearTimer(w.maximumTimer)
	}
	for index := range w.toolTimers {
		w.stopTool(index)
	}
}
