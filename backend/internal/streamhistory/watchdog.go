package streamhistory

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type WatchdogSnapshot struct {
	Phase                 string `json:"phase"`
	EventCount            int    `json:"eventCount"`
	ContentDeltaCount     int    `json:"contentDeltaCount"`
	TextDeltaCount        int    `json:"textDeltaCount"`
	ThinkingDeltaCount    int    `json:"thinkingDeltaCount"`
	ToolInputDeltaCount   int    `json:"toolInputDeltaCount"`
	LastEventType         string `json:"lastEventType,omitempty"`
	LastDeltaType         string `json:"lastDeltaType,omitempty"`
	LastBlockType         string `json:"lastBlockType,omitempty"`
	MessageStopReceived   bool   `json:"messageStopReceived"`
	ToolUseStarted        bool   `json:"toolUseStarted"`
	LocalToolUseStarted   bool   `json:"localToolUseStarted"`
	LocalToolUseCompleted bool   `json:"localToolUseCompleted"`
	ServerToolUseStarted  bool   `json:"serverToolUseStarted"`
	original              protocol.JSONObject
}

func (s WatchdogSnapshot) MarshalJSON() ([]byte, error) {
	if s.original.IsObject() {
		return s.original.MarshalJSON()
	}
	type plain WatchdogSnapshot
	return json.Marshal(plain(s))
}
func (s WatchdogSnapshot) Clone() WatchdogSnapshot { s.original = s.original.Clone(); return s }

type WatchdogState struct {
	Value  WatchdogSnapshot
	blocks map[int]string
}

func NewWatchdogState() *WatchdogState {
	state := &WatchdogState{Value: WatchdogSnapshot{Phase: "before_first_event"}, blocks: map[int]string{}}
	raw, _ := json.Marshal(state.Value)
	_ = state.Value.original.UnmarshalJSON(raw)
	return state
}
func (s *WatchdogState) Record(raw json.RawMessage) (bool, error) {
	var event protocol.JSONObject
	if err := event.UnmarshalJSON(raw); err != nil {
		return false, err
	}
	typ := event.String("type")
	if typ == "" {
		return false, nil
	}
	before := s.Value.ContentDeltaCount
	s.Value.EventCount++
	s.Value.LastEventType = typ
	index, validIndex := 0, false
	if value := event.Get("index"); value != nil {
		validIndex = json.Unmarshal(value, &index) == nil
	}
	switch typ {
	case "message_stop":
		s.Value.MessageStopReceived = true
	case "content_block_start":
		block := object(event.Get("content_block"))
		kind := block.String("type")
		if kind != "" {
			s.Value.LastBlockType = kind
			s.Value.ToolUseStarted = s.Value.ToolUseStarted || kind == "tool_use" || kind == "server_tool_use"
			s.Value.LocalToolUseStarted = s.Value.LocalToolUseStarted || kind == "tool_use"
			s.Value.ServerToolUseStarted = s.Value.ServerToolUseStarted || kind == "server_tool_use"
			if validIndex {
				s.blocks[index] = kind
			}
		}
	case "content_block_stop":
		if validIndex {
			if s.blocks[index] == "tool_use" {
				s.Value.LocalToolUseCompleted = true
				s.Value.LastBlockType = "tool_use"
			}
			delete(s.blocks, index)
		}
	case "content_block_delta":
		delta := object(event.Get("delta"))
		kind := delta.String("type")
		if kind != "" {
			s.Value.LastDeltaType = kind
		}
		if validIndex && s.blocks[index] != "" {
			s.Value.LastBlockType = s.blocks[index]
		}
		field := ""
		switch kind {
		case "text_delta":
			field = "text"
		case "thinking_delta":
			field = "thinking"
		case "input_json_delta":
			field = "partial_json"
		case "connector_text_delta":
			field = "connector_text"
		}
		if field != "" {
			if units, err := protocol.DecodeJavaScriptJSONString(delta.Get(field)); err == nil && len(units) > 0 {
				s.Value.ContentDeltaCount++
				switch kind {
				case "text_delta", "connector_text_delta":
					s.Value.TextDeltaCount++
				case "thinking_delta":
					s.Value.ThinkingDeltaCount++
				case "input_json_delta":
					s.Value.ToolInputDeltaCount++
				}
			}
		}
	}
	s.Value.Phase = "before_content"
	if s.Value.ContentDeltaCount > 0 {
		s.Value.Phase = "mid_stream"
	}
	type plain WatchdogSnapshot
	raw, err := json.Marshal(plain(s.Value))
	if err != nil {
		return false, err
	}
	var changed protocol.JSONObject
	if err = changed.UnmarshalJSON(raw); err != nil {
		return false, err
	}
	s.Value.original.Assign(changed)
	return before == 0 && s.Value.ContentDeltaCount > 0, nil
}

type WatchdogTimeoutError struct {
	Reason    string
	TimeoutMS float64
	Snapshot  WatchdogSnapshot
}

func (e *WatchdogTimeoutError) SafeToRetry() bool {
	return e.Reason == "idle" && !e.Snapshot.ServerToolUseStarted
}
func (e *WatchdogTimeoutError) Code() string {
	if e.Reason == "max_duration" {
		return "STREAM_MAX_DURATION"
	}
	if e.Reason == "tool_input_duration" {
		return "STREAM_TOOL_INPUT_DURATION"
	}
	return "STREAM_IDLE_TIMEOUT"
}
func (e *WatchdogTimeoutError) Error() string {
	seconds := fmt.Sprintf("%.0fs", math.Floor(e.TimeoutMS/1000+.5))
	last := e.Snapshot.LastDeltaType
	if last == "" {
		last = e.Snapshot.LastEventType
	}
	if last == "" {
		last = "none"
	}
	parts := []string{"last event: " + last, fmt.Sprintf("events: %d", e.Snapshot.EventCount), fmt.Sprintf("content deltas: %d", e.Snapshot.ContentDeltaCount)}
	if e.Snapshot.LastBlockType != "" {
		parts = append(parts, "last block: "+e.Snapshot.LastBlockType)
	}
	details := strings.Join(parts, ", ")
	if e.Reason == "max_duration" {
		return "Stream max duration exceeded - no completion received after " + seconds + " (" + details + ")"
	}
	if e.Reason == "tool_input_duration" {
		return "Tool input generation exceeded " + seconds + " - aborting incomplete tool call (" + details + ")"
	}
	if e.Snapshot.Phase == "before_first_event" {
		return "Stream idle timeout - no stream events received for " + seconds
	}
	if e.Snapshot.Phase == "before_content" {
		return "Stream idle timeout - stream started but no content was received for " + seconds + " (" + details + ")"
	}
	return "Provider stream stalled after partial response - no new chunks for " + seconds + " (" + details + ")"
}
func (e *WatchdogTimeoutError) EnumerableErrorJSON() (protocol.JSONObject, error) {
	raw, err := json.Marshal(e.Snapshot)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	return protocol.NewJSONObject(field("reason", literal(e.Reason)), field("timeoutMs", json.RawMessage(fmt.Sprint(e.TimeoutMS))), field("streamSnapshot", raw), field("name", literal("StreamWatchdogTimeoutError")), field("code", literal(e.Code())), field("phase", literal(e.Snapshot.Phase))), nil
}
func (e *WatchdogTimeoutError) DiagnosticJSON() (json.RawMessage, error) {
	raw, err := json.Marshal(e.Snapshot)
	if err != nil {
		return nil, err
	}
	var snapshot protocol.JSONObject
	if err = snapshot.UnmarshalJSON(raw); err != nil {
		return nil, err
	}
	result := protocol.NewJSONObject(field("reason", literal(e.Reason)), field("code", literal(e.Code())), field("phase", literal(e.Snapshot.Phase)), field("timeoutMs", json.RawMessage(fmt.Sprint(e.TimeoutMS))), field("safeToRetry", json.RawMessage(fmt.Sprint(e.SafeToRetry()))))
	result.Assign(snapshot)
	return result.MarshalJSON()
}
