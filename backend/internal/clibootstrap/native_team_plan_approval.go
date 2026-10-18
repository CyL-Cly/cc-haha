package clibootstrap

import (
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
)

func (m *MainPrint) nativePlanTeammateIdentity(scope *cooperative.Scope) (bool, bool, error) {
	if !scope.BelongsTo(m.Owner) {
		return false, false, cooperative.ErrRestricted
	}
	identity, err := m.Controller.TeammateIdentityOnOwner(scope)
	if err != nil {
		return false, false, err
	}
	return identity.AgentID != "", identity.PlanModeRequired, nil
}
func (m *MainPrint) nativeRequestTeamPlanApproval(scope *cooperative.Scope, plan json.RawMessage, path string) (json.RawMessage, error) {
	identity, err := m.Controller.TeammateIdentityOnOwner(scope)
	if err != nil {
		return nil, err
	}
	if identity.AgentID == "" || !identity.PlanModeRequired {
		return nil, cooperative.ErrRestricted
	}
	units, err := protocol.DecodeJavaScriptJSONString(plan)
	if err != nil || len(units) == 0 {
		return nil, fmt.Errorf("No plan file found at %s. Please write your plan to this file before calling ExitPlanMode.", path)
	}
	if m.teamLeader == nil {
		return nil, cooperative.ErrRestricted
	}
	// The immutable leader and worker role both use the original physical store.
	if m.teamLeader.mailbox == nil {
		mailbox, e := teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: m.options.Native.ConfigHomeDirectory(), TaskStore: m.taskStore})
		err = e
		m.teamLeader.mailbox = mailbox
		if err != nil {
			return nil, err
		}
	}
	requestID := fmt.Sprintf("plan_approval-%d@%s", int64(m.options.Native.NowMillis()), identity.AgentID)
	stamp := m.options.Native.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	body := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("plan_approval_request")}, protocol.JSONField{Name: "from", Value: quoteMain(identity.AgentName)}, protocol.JSONField{Name: "timestamp", Value: quoteMain(stamp)}, protocol.JSONField{Name: "planFilePath", Value: quoteMain(path)}, protocol.JSONField{Name: "planContent", Value: plan}, protocol.JSONField{Name: "requestId", Value: quoteMain(requestID)})
	mailbox := m.teamLeader.mailbox
	work, err := nativeSkillWork(m, scope, func() (struct{}, error) {
		envelope := protocol.NewJSONObject(protocol.JSONField{Name: "from", Value: quoteMain(identity.AgentName)}, protocol.JSONField{Name: "text", Value: quoteMain(string(rawMain(body)))}, protocol.JSONField{Name: "timestamp", Value: quoteMain(stamp)})
		return struct{}{}, mailbox.Write("team-lead", identity.TeamName, envelope)
	})
	if err != nil {
		return nil, err
	}
	if _, err = work.Await(scope); err != nil {
		return nil, err
	}
	if d := m.options.DetachedAgent; d != nil && d.teammate != nil && d.preset != nil {
		parent := d.preset.parent
		_, err = nativeAgentOwnerCall(m, scope, parent, func(current *cooperative.Scope) (struct{}, error) {
			tasks, err := parent.Controller.BackgroundTasksOnOwner(current)
			if err != nil {
				return struct{}{}, err
			}
			for _, task := range tasks {
				role, _ := task.Object("identity")
				if task.String("type") == "in_process_teammate" && role.String("agentName") == identity.AgentName {
					return struct{}{}, parent.Controller.UpdateBackgroundTaskOnOwner(current, task.String("id"), func(data protocol.JSONObject) (protocol.JSONObject, error) {
						data.Set("awaitingPlanApproval", rawMain(true))
						return data, nil
					})
				}
			}
			return struct{}{}, nil
		})
		if err != nil {
			return nil, err
		}
	}
	result := protocol.NewJSONObject(protocol.JSONField{Name: "plan", Value: plan}, protocol.JSONField{Name: "isAgent", Value: rawMain(true)}, protocol.JSONField{Name: "filePath", Value: quoteMain(path)}, protocol.JSONField{Name: "awaitingLeaderApproval", Value: rawMain(true)}, protocol.JSONField{Name: "requestId", Value: quoteMain(requestID)})
	return rawMain(result), nil
}
