package clibootstrap

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (p *foregroundAgentProgress) snapshot() protocol.JSONObject {
	progress := protocol.NewJSONObject(protocol.JSONField{Name: "toolUseCount", Value: rawMain(p.tools)}, protocol.JSONField{Name: "tokenCount", Value: rawMain(p.input + p.output)})
	if len(p.recent) > 0 {
		progress.Set("lastActivity", rawMain(p.recent[len(p.recent)-1]))
	}
	progress.Set("recentActivities", rawMain(append([]protocol.JSONObject{}, p.recent...)))
	return progress
}
func (m *MainPrint) updateNativeAgentProgress(scope *cooperative.Scope, id string, p *foregroundAgentProgress) error {
	update := p.snapshot()
	return m.nativeBackgroundBindings().Update(scope, id, func(task protocol.JSONObject) (protocol.JSONObject, error) {
		if task.String("status") != "running" {
			return task, nil
		}
		previous, _ := task.Object("progress")
		if summary := previous.Get("summary"); backgroundUsageTruthy(summary) {
			update.Set("summary", append(json.RawMessage(nil), summary...))
		}
		task = task.Clone()
		task.Set("progress", rawMain(update))
		return task, nil
	})
}
