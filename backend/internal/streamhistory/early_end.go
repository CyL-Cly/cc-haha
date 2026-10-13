package streamhistory

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type StreamEndEvidence struct {
	EventCount          int     `json:"eventCount"`
	LastEventType       *string `json:"lastEventType"`
	StopReason          *string `json:"stopReason"`
	MessageStopReceived bool    `json:"messageStopReceived"`
	OpenBlockCount      int     `json:"openBlockCount"`
	ElapsedMS           float64 `json:"elapsedMs"`
}

type StreamEndedEarlyError struct {
	Reason   string
	Evidence StreamEndEvidence
	Retries  int
}

func (e *StreamEndedEarlyError) Error() string {
	if e.Reason == "no_events" {
		return "Stream ended without receiving any events"
	}
	return "Provider stream ended before completing the response"
}
func (e *StreamEndedEarlyError) EnumerableErrorJSON() (protocol.JSONObject, error) {
	raw, err := json.Marshal(e.Evidence)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	return protocol.NewJSONObject(field("reason", literal(e.Reason)), field("evidence", raw), field("name", literal("StreamEndedEarlyError"))), nil
}

func (e *StreamEndedEarlyError) DiagnosticJSON() (json.RawMessage, error) {
	result := protocol.NewJSONObject(field("reason", literal(e.Reason)), field("retries", json.RawMessage(fmt.Sprint(e.Retries))))
	raw, err := json.Marshal(e.Evidence)
	if err != nil {
		return nil, err
	}
	var evidence protocol.JSONObject
	if err = evidence.UnmarshalJSON(raw); err != nil {
		return nil, err
	}
	result.Assign(evidence)
	return result.MarshalJSON()
}

// Count open blocks before preserving partial text. A terminal stop_reason
// tolerates a dropped message_stop only if no later content event followed it.
func (c *Controller) EndOfStream(elapsedMS float64) *StreamEndedEarlyError {
	open := 0
	for index := range c.blocks {
		if !c.completed[index] {
			open++
		}
	}
	c.preservePartialText()
	if c.isLimit() {
		return nil
	}
	reason := ""
	if c.start == nil || len(c.created) == 0 && (nullish(c.stopReason) || canonical(c.stopReason) == `"tool_use"`) {
		reason = "no_events"
	} else if !c.messageStop && !c.stopTerminal || nullish(c.stopReason) || open > 0 {
		reason = "incomplete"
	}
	if reason == "" {
		return nil
	}
	var stop, last *string
	if !nullish(c.stopReason) {
		var value string
		if json.Unmarshal(c.stopReason, &value) == nil {
			stop = &value
		}
	}
	if c.lastEvent != "" {
		value := c.lastEvent
		last = &value
	}
	return &StreamEndedEarlyError{Reason: reason, Evidence: StreamEndEvidence{EventCount: c.eventCount, LastEventType: last, StopReason: stop, MessageStopReceived: c.messageStop, OpenBlockCount: open, ElapsedMS: elapsedMS}}
}

func (e *StreamEndedEarlyError) Format() string {
	details := []string{}
	if e.Retries > 0 {
		unit := "times"
		if e.Retries == 1 {
			unit = "time"
		}
		details = append(details, fmt.Sprintf("retried %d %s", e.Retries, unit))
	}
	unit := "events"
	if e.Evidence.EventCount == 1 {
		unit = "event"
	}
	details = append(details, fmt.Sprintf("%d %s", e.Evidence.EventCount, unit))
	if e.Evidence.LastEventType != nil && *e.Evidence.LastEventType != "" {
		details = append(details, "last "+*e.Evidence.LastEventType)
	}
	stop := "none"
	if e.Evidence.StopReason != nil {
		stop = *e.Evidence.StopReason
	}
	details = append(details, "stop_reason "+stop)
	if !e.Evidence.MessageStopReceived {
		details = append(details, "message_stop missing")
	}
	if e.Evidence.OpenBlockCount > 0 {
		unit = "blocks"
		if e.Evidence.OpenBlockCount == 1 {
			unit = "block"
		}
		details = append(details, fmt.Sprintf("%d %s open", e.Evidence.OpenBlockCount, unit))
	}
	details = append(details, fmt.Sprintf("%.0fs", math.Floor(e.Evidence.ElapsedMS/1000+.5)))
	return e.Error() + ". The upstream model provider closed the stream before the reply finished; this is not a context-limit error. (" + strings.Join(details, " · ") + ")"
}
