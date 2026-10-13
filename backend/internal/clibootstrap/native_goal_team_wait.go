package clibootstrap

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// The lead waits only for actual team work. Paused/stopped/idle teammates do not
// suppress the goal's normal continuation, and ordinary agents never qualify.
func (m *MainPrint) nativeTeamWorkInProgress(scope *cooperative.Scope) (bool, error) {
	if !scope.BelongsTo(m.Owner) {
		return false, cooperative.ErrRestricted
	}
	if m.nativeInProcessTeammateIdentity() != nil || m.teamWorker != nil || m.teamState == nil || m.teamState.snapshot.String("leadAgentId") == "" || m.teamLeader == nil {
		return false, nil
	}
	tasks, err := m.Controller.BackgroundTasksOnOwner(scope)
	if err != nil {
		return false, err
	}
	for _, task := range tasks {
		if task.String("type") == "in_process_teammate" && task.String("status") == "running" && string(task.Get("isIdle")) != "true" {
			return true, nil
		}
	}
	file := m.teamLeader.config.Read(m.nativeLeaderTeamName())
	if file == nil {
		return false, nil
	}
	var members []protocol.JSONObject
	if err := json.Unmarshal(file.Get("members"), &members); err != nil {
		return false, err
	}
	for _, member := range members {
		if member.String("name") != "team-lead" && member.String("backendType") == "process" && string(member.Get("terminated")) != "true" && (string(member.Get("isActive")) == "true" || member.Get("autoRetry") != nil) {
			return true, nil
		}
	}
	return false, nil
}
