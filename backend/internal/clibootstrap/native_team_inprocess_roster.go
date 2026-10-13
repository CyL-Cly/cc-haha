package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
	"strings"
)

type nativeInProcessReservation struct {
	Name, ID, Color string
	Added           bool
}

// Name selection and roster reservation are one real file transaction before
// any child can run. The short color allocation is relayed to the same Owner.
func (m *MainPrint) reserveNativeInProcessRoster(ctx context.Context, scope *cooperative.Scope, team string, input protocol.JSONObject, model string, resume *nativeInProcessResume) (nativeInProcessReservation, error) {
	work, err := nativeSkillWork(m, scope, func() (nativeInProcessReservation, error) {
		var reserved nativeInProcessReservation
		_, err := m.teamLeader.config.Mutate(team, func(file protocol.JSONObject) (protocol.JSONObject, error) {
			if !file.IsObject() {
				return file, errors.New("Team does not exist")
			}
			var members []protocol.JSONObject
			if err := json.Unmarshal(file.Get("members"), &members); err != nil {
				return file, err
			}
			if resume != nil {
				identity := resume.Template.Identity
				reserved = nativeInProcessReservation{Name: identity.AgentName, ID: identity.AgentID, Color: identity.Color}
				model = resume.Template.Model
				for i := range members {
					if members[i].String("agentId") == identity.AgentID {
						members[i].Set("joinedAt", rawMain(m.options.Native.NowMillis()))
						file.Set("members", rawMain(members))
						return file, nil
					}
				}
			} else {
				var err error
				reserved, err = inProcessParentCall(ctx, m, func(next *cooperative.Scope) (nativeInProcessReservation, error) {
					name := strings.ReplaceAll(teamspawn.UniqueName(input.String("name"), members), "@", "-")
					id := name + "@" + team
					color, err := m.nativeTeammateColor(next, id)
					return nativeInProcessReservation{Name: name, ID: id, Color: color}, err
				})
				if err != nil {
					return file, err
				}
			}
			plan := input.String("mode") == "plan"
			if resume != nil {
				plan = resume.Template.Identity.PlanModeRequired
			}
			member := nativeInProcessObject(rawMain(map[string]any{"agentId": reserved.ID, "name": reserved.Name, "model": model, "prompt": input.String("prompt"), "color": reserved.Color, "planModeRequired": plan, "joinedAt": m.options.Native.NowMillis(), "tmuxPaneId": "in-process", "cwd": m.workingDirectory(), "subscriptions": []string{}, "backendType": "in-process"}))
			if value := input.Get("subagent_type"); value != nil {
				member.Set("agentType", value)
			}
			file.Set("members", rawMain(append(members, member)))
			reserved.Added = true
			return file, nil
		})
		return reserved, err
	})
	if err != nil {
		return nativeInProcessReservation{}, err
	}
	return work.Await(scope)
}
