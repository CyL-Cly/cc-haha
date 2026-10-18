package sdkhost

import (
	"encoding/json"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

func TestNativeTeammateInputObserverRetainsInteractiveRootAndRecordBoundaries(t *testing.T) {
	record := func(t *testing.T, value string) protocol.JSONObject {
		t.Helper()
		var result protocol.JSONObject
		if err := json.Unmarshal([]byte(value), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	valid := `{"type":"user","message":{"role":"user","content":"<teammate-message teammate_id=\"worker\">\nactual accepted input\n</teammate-message>"}}`
	for _, item := range []struct {
		name  string
		alter func(*Host, *sessioncontroller.ProcessedInput)
	}{
		{"non-native", func(h *Host, _ *sessioncontroller.ProcessedInput) { h.options.Print.NativeProcess = false }},
		{"non-interactive", func(h *Host, _ *sessioncontroller.ProcessedInput) { h.options.Print.NativeInteractive = false }},
		{"agent", func(h *Host, _ *sessioncontroller.ProcessedInput) { h.options.Print.AgentID = "actual-agent-role" }},
		{"team-worker", func(h *Host, _ *sessioncontroller.ProcessedInput) { h.options.Print.TeamWorker = &NativeTeamWorker{} }},
		{"missing-print", func(h *Host, _ *sessioncontroller.ProcessedInput) { h.options.Print = nil }},
		{"ordinary-user", func(_ *Host, p *sessioncontroller.ProcessedInput) {
			p.Messages = []protocol.JSONObject{record(t, `{"type":"user","message":{"content":"ordinary user input"}}`)}
		}},
		{"meta-user", func(_ *Host, p *sessioncontroller.ProcessedInput) {
			p.Messages[0].Set("isMeta", json.RawMessage(`true`))
		}},
		{"assistant", func(_ *Host, p *sessioncontroller.ProcessedInput) {
			p.Messages[0].Set("type", json.RawMessage(`"assistant"`))
		}},
		{"content-blocks", func(_ *Host, p *sessioncontroller.ProcessedInput) {
			p.Messages[0].Set("message", json.RawMessage(`{"content":[{"type":"text","text":"<teammate-message"}]}`))
		}},
		{"no-query-keeps-original-branch", func(_ *Host, p *sessioncontroller.ProcessedInput) { p.NoQuery = true }},
		{"local-jsx-keeps-original-branch", func(_ *Host, p *sessioncontroller.ProcessedInput) { p.TerminalLocalJSX = true }},
	} {
		t.Run(item.name, func(t *testing.T) {
			h := &Host{options: Options{Print: &PrintOptions{NativeProcess: true, NativeInteractive: true}}}
			prompt := &sessioncontroller.ProcessedInput{Messages: []protocol.JSONObject{record(t, valid)}}
			before, _ := json.Marshal(prompt.Messages)
			if !h.observesNativeTeammateInput(prompt) {
				t.Fatal("canonical user record class rejected")
			}
			after, _ := json.Marshal(prompt.Messages)
			if string(before) != string(after) {
				t.Fatal("observer predicate mutated accepted record")
			}
			item.alter(h, prompt)
			if h.observesNativeTeammateInput(prompt) {
				t.Fatal("new observer crossed its existing input/actor boundary")
			}
		})
	}
	var h *Host
	if h.observesNativeTeammateInput(nil) || (&Host{}).observesNativeTeammateInput(nil) {
		t.Fatal("missing input admitted")
	}
}
