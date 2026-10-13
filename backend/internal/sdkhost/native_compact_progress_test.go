package sdkhost

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"testing"
)

func TestCompactProgressFirstTextIsVisibleAndFurtherTextIncreases(t *testing.T) {
	r := compactProgressReporter{}
	for _, row := range []struct {
		event string
		now   float64
		want  int
	}{
		{`{"type":"message_start"}`, 1700000000000, -1},
		{`{"type":"content_block_start"}`, 1700000000050, -1},
		{`{"type":"content_block_delta","delta":{"type":"text_delta","text":"😀"}}`, 1700000000060, 2},
		{`{"type":"content_block_delta","delta":{"type":"text_delta","text":"summary"}}`, 1700000000100, -1},
		{`{"type":"content_block_delta","delta":{"type":"text_delta","text":" retained"}}`, 1700000001100, 18},
		{`{"type":"message_start"}`, 1700000002100, -1},
		{`{"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"ignored"}}`, 1700000002120, -1},
		{`{"type":"content_block_delta","delta":{"type":"text_delta","text":"new attempt"}}`, 1700000002140, 11},
	} {
		var event protocol.JSONObject
		_ = json.Unmarshal([]byte(row.event), &event)
		calls := 0
		got := r.report(event, func() float64 { calls++; return row.now })
		if row.want < 0 && got != nil || row.want >= 0 && (got == nil || *got != row.want) {
			t.Fatal(row, got)
		}
		if event.String("type") == "message_start" && calls != 0 {
			t.Fatal("message_start must not consume throttle clock")
		}
	}
}
