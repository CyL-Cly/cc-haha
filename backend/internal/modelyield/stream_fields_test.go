package modelyield

import (
	"encoding/json"
	"math"
	"testing"
)

func TestProducerClockContentAndOriginalPolicy(t *testing.T) {
	now := float64(100)
	f := producerFields{clock: &ProducerClock{NowMillis: func() float64 { return now }}, start: 80}
	timing, e := f.observe(json.RawMessage(`{"type":"message_start"}`))
	if e != nil || timing.TTFTMillis != 20 {
		t.Fatalf("original TTFT %+v %v", timing, e)
	}
	for _, source := range []string{`{"type":"content_block_delta","delta":{"type":"text_delta","text":""}}`, `{"type":"content_block_delta","delta":{"type":"signature_delta","signature":"real signature"}}`, `{"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":null}}`} {
		if _, e = f.observe([]byte(source)); e != nil {
			t.Fatal(e)
		}
	}
	if f.first != nil {
		t.Fatal("noncontent transition started decode span")
	}
	for _, kind := range []string{"text_delta", "thinking_delta", "input_json_delta", "connector_text_delta"} {
		fields := map[string]string{"text_delta": "text", "thinking_delta": "thinking", "input_json_delta": "partial_json", "connector_text_delta": "connector_text"}
		f.first = nil
		now = 101
		source := `{"type":"content_block_delta","delta":{"type":"` + kind + `","` + fields[kind] + `":"\ud800"}}`
		if _, e = f.observe([]byte(source)); e != nil {
			t.Fatal(e)
		}
		now = 130
		timing, e = f.observe([]byte(`{"type":"message_stop"}`))
		if e != nil || timing.DecodeMillis != 29 {
			t.Fatalf("%s span %+v %v", kind, timing, e)
		}
		now = 99
		timing, e = f.observe([]byte(`{"type":"message_stop"}`))
		if e != nil || timing.DecodeMillis != 0 {
			t.Fatalf("negative span %+v %v", timing, e)
		}
	}
	f.first = nil
	now = 150
	timing, e = f.observe([]byte(`{"type":"message_stop"}`))
	if e != nil || timing.DecodeMillis != 0 {
		t.Fatal("unopened span")
	}
	f.clock = nil
	if timing, e = f.observe([]byte(`not parsed in legacy no-clock profile`)); e != nil || timing.TTFTMillis != 0 {
		t.Fatal(e)
	}
	f.clock = &ProducerClock{}
	if _, e = f.observe([]byte(`{}`)); e == nil {
		t.Fatal("missing clock admitted")
	}
	f.clock = &ProducerClock{NowMillis: func() float64 { return math.NaN() }}
	if _, e = f.observe([]byte(`{}`)); e == nil {
		t.Fatal("nonfinite clock admitted")
	}
	f.clock = &ProducerClock{NowMillis: func() float64 { return 1.25 }}
	if _, e = f.observe([]byte(`{}`)); e == nil {
		t.Fatal("fractional Date.now admitted")
	}
	f.clock = &ProducerClock{NowMillis: func() float64 { return 1 }}
	if _, e = f.observe([]byte(`[]`)); e == nil {
		t.Fatal("nonobject event admitted")
	}
	if _, e = f.observe([]byte(`{"type":"content_block_delta","delta":[]}`)); e == nil {
		t.Fatal("nonobject delta admitted")
	}
	for _, env := range []map[string]string{nil, {"CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK": "0"}, {"unknown": "1"}, {"CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK": "1", "CLAUDE_STREAM_TRANSIENT_RETRY_MAX": "1", "CLAUDE_ENABLE_STREAM_WATCHDOG": "0"}} {
		if _, e := StreamErrorPolicyFromOriginalEnvironment(env); e == nil {
			t.Fatal("unresolved/active policy admitted")
		}
	}
	if originalPolicyForTest() == nil {
		t.Fatal("explicit original no-retry profile refused")
	}
}
