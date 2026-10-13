package tui

import (
	"strings"
	"testing"
	"time"
)

func TestPluginHintOriginalChoicesPriorityTypingPauseAndMountedTimeout(t *testing.T) {
	for _, choice := range []struct{ key, response string }{{"enter", "yes"}, {"esc", "no"}, {"3", "disable"}} {
		state := NewState(nil, 80, 24)
		state.PluginHint = &pluginHintState{hint: PluginHint{Name: "owned", Marketplace: "agent-skills", SourceCommand: "vendor"}}
		if lines := strings.Join(state.PluginHint.lines(80), "\n"); !strings.Contains(lines, "Plugin Recommendation") || !strings.Contains(lines, "vendor command suggests installing a plugin.") {
			t.Fatal(lines)
		}
		actions := state.HandleKey(Key{Name: choice.key}, time.Now())
		if len(actions) != 1 || actions[0].kind != actionPluginHint || actions[0].name != choice.response {
			t.Fatal(choice, actions)
		}
	}
	now := time.Unix(17, 0)
	state := NewState(nil, 80, 24)
	state.PluginHint = &pluginHintState{}
	state.Permissions = []PermissionRequest{{ID: "higher-priority"}}
	state.pluginHintTick(now)
	if !state.PluginHint.expires.IsZero() || state.pluginHintVisible() {
		t.Fatal("timer started behind higher dialog")
	}
	state.Permissions = nil
	state.ActiveTurn = "real-agent-may-run"
	state.pluginHintTick(now)
	if !state.pluginHintVisible() {
		t.Fatal("Source dialog is not gated on loading")
	}
	state.Editor.Set("draft")
	state.pluginHintInputChanged(now)
	state.pluginHintTick(now)
	if state.pluginHintVisible() {
		t.Fatal("typing priority")
	}
	state.pluginHintTick(now.Add(1500 * time.Millisecond))
	if !state.pluginHintVisible() {
		t.Fatal("Source 1500 ms typing suppression did not expire")
	}
	if actions := state.pluginHintTick(now.Add(30 * time.Second)); len(actions) != 0 {
		t.Fatal("used obsolete unmounted timer")
	}
	actions := state.pluginHintTick(now.Add(31500 * time.Millisecond))
	if len(actions) != 1 || actions[0].name != "no" || state.PluginHint != nil {
		t.Fatal("Source mounted 30-second dismiss", actions)
	}
}
