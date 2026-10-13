package modelyield

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// ProducerClock is the original Date.now primitive. It is distinct from Date
// allocation for envelopes. An absent clock preserves the legacy zero profile.
type ProducerClock struct{ NowMillis func() float64 }
type producerFields struct {
	clock *ProducerClock
	start float64
	first *float64
}

func (f *producerFields) now() (float64, error) {
	if f.clock == nil {
		return 0, nil
	}
	if f.clock.NowMillis == nil {
		return 0, unsupported("original producer Date.now")
	}
	value := f.clock.NowMillis()
	if math.IsNaN(value) || math.IsInf(value, 0) || math.Trunc(value) != value || math.Abs(value) > 9007199254740991 {
		return 0, unsupported("finite integer Date.now")
	}
	return value, nil
}
func (f *producerFields) observe(raw json.RawMessage) (streamhistory.Timing, error) {
	if f.clock == nil {
		return streamhistory.Timing{}, nil
	}
	now, err := f.now()
	if err != nil {
		return streamhistory.Timing{}, err
	}
	var event protocol.JSONObject
	if err = event.UnmarshalJSON(raw); err != nil || !event.IsObject() {
		return streamhistory.Timing{}, unsupported("producer event object")
	}
	if event.String("type") == "content_block_delta" && f.first == nil {
		var delta protocol.JSONObject
		if err = delta.UnmarshalJSON(event.Get("delta")); err != nil || !delta.IsObject() {
			return streamhistory.Timing{}, unsupported("producer delta object")
		}
		field := ""
		switch delta.String("type") {
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
			if units, e := protocol.DecodeJavaScriptJSONString(delta.Get(field)); e == nil && len(units) > 0 {
				f.first = &now
			}
		}
	}
	timing := streamhistory.Timing{}
	if event.String("type") == "message_start" {
		late, e := f.now()
		if e != nil {
			return timing, e
		}
		timing.TTFTMillis = int64(late - f.start)
	}
	if event.String("type") == "message_stop" && f.first != nil {
		elapsed := now - *f.first
		if elapsed < 0 {
			elapsed = 0
		}
		timing.DecodeMillis = int64(elapsed)
	}
	return timing, nil
}

// StreamErrorPolicy retains the raw original fallback/retry/watchdog inputs.
// Only the exact inactive triple has a resolved error policy. Other snapshots
// still admit normal streams; the actual error branch reports its capability.
type StreamErrorPolicy struct {
	disabled            bool
	original            map[string]string
	native              bool
	maxTransientRetries int
	disableFallback     bool
}

// Native callers retain the same raw snapshot while resolving Source's env
// predicates. Actual retry/fallback consumers still enforce their own seams.
func PreserveNativeStreamErrorPolicyOriginalEnvironment(env map[string]string) *StreamErrorPolicy {
	p := PreserveStreamErrorPolicyOriginalEnvironment(env)
	p.native = true
	for _, name := range []string{"CLAUDE_STREAM_IDLE_TIMEOUT_MS", "CLAUDE_STREAM_FIRST_TOKEN_TIMEOUT_MS", "API_TIMEOUT_MS", "CLAUDE_STREAM_MAX_DURATION_MS", "CLAUDE_STREAM_TOOL_INPUT_MAX_DURATION_MS", "CC_HAHA_OPENAI_OAUTH_FIRST_TOKEN_TIMEOUT_MS"} {
		if value, present := env[name]; present {
			p.original[name] = value
		}
	}
	p.maxTransientRetries = 2
	if prefix := regexp.MustCompile(`^[+-]?[0-9]+`).FindString(strings.TrimFunc(env["CLAUDE_STREAM_TRANSIENT_RETRY_MAX"], streamPolicySpace)); prefix != "" {
		if number, e := strconv.ParseFloat(prefix, 64); e == nil && number >= 0 && !math.IsInf(number, 0) {
			p.maxTransientRetries = int(math.Min(number, 5))
		}
	}
	p.disableFallback = streamPolicyTruthy(env["CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK"])
	return p
}

func streamRetryOverride(value string) *float64 {
	prefix := regexp.MustCompile(`^[+-]?[0-9]+`).FindString(strings.TrimFunc(value, streamPolicySpace))
	if prefix == "" {
		return nil
	}
	if number, e := strconv.ParseFloat(prefix, 64); e == nil && number >= 0 && !math.IsInf(number, 0) {
		n := math.Min(number, 5)
		return &n
	}
	return nil
}

func streamPolicySpace(r rune) bool {
	return r >= 9 && r <= 13 || r == 32 || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
}
func streamPolicyTruthy(value string) bool {
	switch strings.ToLower(strings.TrimFunc(value, streamPolicySpace)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func PreserveStreamErrorPolicyOriginalEnvironment(env map[string]string) *StreamErrorPolicy {
	original := map[string]string{}
	for _, key := range []string{"CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK", "CLAUDE_STREAM_TRANSIENT_RETRY_MAX", "CLAUDE_ENABLE_STREAM_WATCHDOG"} {
		if value, present := env[key]; present {
			original[key] = value
		}
	}
	return &StreamErrorPolicy{original: original, disabled: len(original) == 3 && original["CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK"] == "1" && original["CLAUDE_STREAM_TRANSIENT_RETRY_MAX"] == "0" && original["CLAUDE_ENABLE_STREAM_WATCHDOG"] == "0"}
}

// OriginalEnvironment returns a defensive raw-input view, without filling
// absent keys or resolving aliases and defaults.
func (policy *StreamErrorPolicy) OriginalEnvironment() map[string]string {
	out := map[string]string{}
	if policy != nil {
		for key, value := range policy.original {
			out[key] = value
		}
	}
	return out
}

func StreamErrorPolicyFromOriginalEnvironment(env map[string]string) (*StreamErrorPolicy, error) {
	for key := range env {
		switch key {
		case "CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK", "CLAUDE_STREAM_TRANSIENT_RETRY_MAX", "CLAUDE_ENABLE_STREAM_WATCHDOG":
		default:
			return nil, unsupported("stream policy original environment " + key)
		}
	}
	if env["CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK"] != "1" || env["CLAUDE_STREAM_TRANSIENT_RETRY_MAX"] != "0" || env["CLAUDE_ENABLE_STREAM_WATCHDOG"] != "0" {
		return nil, unsupported("active or unresolved stream fallback/retry/watchdog")
	}
	return PreserveStreamErrorPolicyOriginalEnvironment(env), nil
}
