package sdkhost

import "github.com/NanmiCoder/cc-haha/backend/internal/protocol"

type compactProgressReporter struct {
	outputChars    int
	lastReportedAt float64
}

// Only text deltas consume the reporting interval. message_start resets the
// per-attempt count but must not postpone the first visible text progress.
func (r *compactProgressReporter) report(event protocol.JSONObject, now func() float64) *int {
	if event.String("type") == "message_start" {
		r.outputChars = 0
		return nil
	}
	delta, _ := event.Object("delta")
	if event.String("type") != "content_block_delta" || delta.String("type") != "text_delta" {
		return nil
	}
	units, _ := protocol.DecodeJavaScriptJSONString(delta.Get("text"))
	r.outputChars += len(units)
	time := now()
	if time-r.lastReportedAt < 1000 {
		return nil
	}
	r.lastReportedAt = time
	value := r.outputChars
	return &value
}
