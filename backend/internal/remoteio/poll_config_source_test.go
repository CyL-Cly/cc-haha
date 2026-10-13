package remoteio

import (
	"encoding/json"
	"testing"
	"time"
)

func TestActualSourceBridgePollOverridesWholeObjectValidationAndDisable(t *testing.T) {
	valid := map[string]any{"poll_interval_ms_not_at_capacity": 2000, "poll_interval_ms_at_capacity": 600000, "session_keepalive_interval_v2_ms": 25}
	zero := map[string]any{"poll_interval_ms_not_at_capacity": 2000, "poll_interval_ms_at_capacity": 600000, "session_keepalive_interval_v2_ms": 0}
	for _, row := range []struct {
		name, user  string
		env, config any
	}{
		{"external-config-ignored", "external", nil, valid}, {"external-env-ignored", "external", valid, nil}, {"ant-config", "ant", nil, valid}, {"ant-env-wins", "ant", zero, valid}, {"ant-invalid-env-falls-to-default-not-config", "ant", map[string]any{"session_keepalive_interval_v2_ms": 1}, valid}, {"ant-invalid-liveness", "ant", map[string]any{"poll_interval_ms_not_at_capacity": 100, "poll_interval_ms_at_capacity": 0, "multisession_poll_interval_ms_at_capacity": 0, "session_keepalive_interval_v2_ms": 0}, nil}, {"ant-heartbeat-liveness", "ant", map[string]any{"poll_interval_ms_not_at_capacity": 100, "poll_interval_ms_at_capacity": 0, "multisession_poll_interval_ms_at_capacity": 0, "non_exclusive_heartbeat_interval_ms": 1, "session_keepalive_interval_v2_ms": 2}, nil}, {"ant-fraction-invalid", "ant", map[string]any{"poll_interval_ms_not_at_capacity": 100, "poll_interval_ms_at_capacity": 100, "session_keepalive_interval_v2_ms": 2.5}, nil},
		{"ant-native-timer-overflow", "ant", map[string]any{"poll_interval_ms_not_at_capacity": 100, "poll_interval_ms_at_capacity": 100, "session_keepalive_interval_v2_ms": 2147483648}, nil},
	} {
		t.Run(row.name, func(t *testing.T) {
			env := map[string]string{"USER_TYPE": row.user}
			global := object([]byte(`{}`))
			if row.env != nil {
				env["CLAUDE_INTERNAL_FC_OVERRIDES"] = string(raw(map[string]any{"tengu_bridge_poll_interval_config": row.env}))
			}
			if row.config != nil {
				global = object(raw(map[string]any{"growthBookOverrides": map[string]any{"tengu_bridge_poll_interval_config": row.config}}))
			}
			want := runSourceFixture(t, "native-remoteio-poll.fixture.ts", map[string]any{"env": env, "global": global})
			var milliseconds float64
			if err := json.Unmarshal(want.Get("effectiveInterval"), &milliseconds); err != nil {
				t.Fatal(err)
			}
			if got := BridgeKeepAliveInterval(env, global); got != time.Duration(milliseconds)*time.Millisecond {
				t.Fatalf("Source interval %v Go %v", milliseconds, got)
			}
		})
	}
}
