package remoteio

import (
	"encoding/json"
	"math"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// BridgeKeepAliveInterval follows the frozen pollConfig schema and the actual
// fork's empty GrowthBook client key. Only original ant overrides precede the
// default; malformed or partially specified configs discard the whole object.
func BridgeKeepAliveInterval(environment map[string]string, global protocol.JSONObject) time.Duration {
	const fallback = 120 * time.Second
	if environment["USER_TYPE"] != "ant" {
		return fallback
	}
	var value json.RawMessage
	var overrides protocol.JSONObject
	if overrides.UnmarshalJSON([]byte(environment["CLAUDE_INTERNAL_FC_OVERRIDES"])) == nil && overrides.IsObject() {
		value = overrides.Get("tengu_bridge_poll_interval_config")
	}
	if value == nil {
		overrides = object(global.Get("growthBookOverrides"))
		value = overrides.Get("tengu_bridge_poll_interval_config")
	}
	if value == nil {
		return fallback
	}
	config := object(value)
	if !config.IsObject() {
		return fallback
	}
	integer := func(key string, def float64, required bool) (float64, bool) {
		v := config.Get(key)
		if v == nil {
			return def, !required
		}
		var n float64
		if json.Unmarshal(v, &n) != nil || len(v) == 0 || v[0] == '"' || v[0] == 'n' || v[0] == 't' || v[0] == 'f' || math.IsNaN(n) || math.IsInf(n, 0) || math.Trunc(n) != n {
			return 0, false
		}
		return n, true
	}
	notCap, a := integer("poll_interval_ms_not_at_capacity", 0, true)
	atCap, b := integer("poll_interval_ms_at_capacity", 0, true)
	heartbeat, c := integer("non_exclusive_heartbeat_interval_ms", 0, false)
	multiNot, d := integer("multisession_poll_interval_ms_not_at_capacity", 2000, false)
	partial, e := integer("multisession_poll_interval_ms_partial_capacity", 2000, false)
	multiAt, f := integer("multisession_poll_interval_ms_at_capacity", 600000, false)
	reclaim, g := integer("reclaim_older_than_ms", 5000, false)
	keepalive, h := integer("session_keepalive_interval_v2_ms", 120000, false)
	zeroOrFloor := func(value float64) bool { return value == 0 || value >= 100 }
	if !(a && b && c && d && e && f && g && h) || notCap < 100 || !zeroOrFloor(atCap) || heartbeat < 0 || multiNot < 100 || partial < 100 || !zeroOrFloor(multiAt) || reclaim < 1 || keepalive < 0 || heartbeat == 0 && (atCap == 0 || multiAt == 0) {
		return fallback
	}
	// Timers in both original runtimes clamp an overflowing signed 32-bit
	// interval to one millisecond, rather than overflow a Go duration.
	if keepalive > 2147483647 {
		return time.Millisecond
	}
	return time.Duration(keepalive) * time.Millisecond
}
