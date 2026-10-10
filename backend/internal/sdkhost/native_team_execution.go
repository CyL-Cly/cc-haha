package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

// NativeTeamExecution identifies the installed producer graph on its actual
// Controller and Owner. Environment flags cannot manufacture a teammate role.
type NativeTeamExecution struct {
	owner      *cooperative.Owner
	controller *sessioncontroller.Controller
	tools      *builtintools.Service
	worker     *NativeTeamWorker
	review     bool
	ordinary   bool
	inProcess  *sessioncontroller.InProcessTeammateIdentity
}

// NewNativeInProcessTeamExecution admits the actual sidechain teammate role.
// Its task identity is independent of the writer ID and cannot mint MAIN or
// leader permission. Ordinary CLI flags still cannot manufacture this role.
func NewNativeInProcessTeamExecution(scope *cooperative.Scope, owner *cooperative.Owner, controller *sessioncontroller.Controller, tools *builtintools.Service, writerID string) (*NativeTeamExecution, error) {
	if !scope.BelongsTo(owner) || controller == nil || tools == nil || !tools.Owns(owner) || !tools.HasTeamBindings() || writerID == "" {
		return nil, cooperative.ErrRestricted
	}
	identity, err := controller.TeammateIdentityOnOwner(scope)
	if err != nil || identity.ParentSessionID == "" || identity.ParentSessionID != controller.SessionID() || identity.AgentID == writerID {
		return nil, cooperative.ErrRestricted
	}
	if _, err = controller.InProcessWriterOnOwner(scope, writerID); err != nil {
		return nil, err
	}
	return &NativeTeamExecution{owner: owner, controller: controller, tools: tools, inProcess: &identity}, nil
}

func NewNativeTeamExecution(scope *cooperative.Scope, owner *cooperative.Owner, controller *sessioncontroller.Controller, tools *builtintools.Service, worker *NativeTeamWorker, environment map[string]string) (*NativeTeamExecution, error) {
	if !scope.BelongsTo(owner) || controller == nil {
		return nil, cooperative.ErrRestricted
	}
	if tools == nil || !tools.HasTeamBindings() {
		return nil, nil
	}
	if !tools.Owns(owner) {
		return nil, cooperative.ErrRestricted
	}
	review := environment["CC_HAHA_TEAM_REVIEW_REQUIRED"] == "1"
	if worker == nil && !review && !tools.HasTeamExecutionBindings() {
		return nil, nil
	}
	_, _, id, err := controller.TeamWorkerIdentity(scope)
	if err != nil {
		return nil, err
	}
	if worker != nil {
		if worker.owner != owner || worker.controller != controller || worker.prepare == nil {
			return nil, cooperative.ErrRestricted
		}
		if id != worker.agentID || id == "" {
			return nil, cooperative.ErrRestricted
		}
	} else {
		identity, err := controller.TeammateIdentityOnOwner(scope)
		if err != nil || id != "" || identity.AgentID != "" {
			return nil, cooperative.ErrRestricted
		}
	}
	return &NativeTeamExecution{owner: owner, controller: controller, tools: tools, worker: worker, review: review, ordinary: worker == nil && !review}, nil
}

func nativeTeamExecutionBound(scope *cooperative.Scope, o Options) bool {
	if o.Print == nil || !o.Print.NativeProcess || o.Print.NativeTeamExecution == nil {
		return false
	}
	t := o.Print.NativeTeamExecution
	if !scope.BelongsTo(t.owner) || t.owner != o.Owner || t.controller != o.Controller || t.tools != o.Print.Tools || t.tools == nil || !t.tools.Owns(o.Owner) || !t.tools.HasTeamBindings() || t.worker != o.Print.TeamWorker {
		return false
	}
	_, _, id, err := o.Controller.TeamWorkerIdentity(scope)
	if err != nil {
		return false
	}
	if t.worker == nil {
		if t.inProcess != nil {
			identity, err := o.Controller.InProcessWriterOnOwner(scope, o.Print.AgentID)
			return err == nil && identity == *t.inProcess && !t.review
		}
		if t.review {
			return o.OriginalEnv["CC_HAHA_TEAM_REVIEW_REQUIRED"] == "1"
		}
		identity, err := o.Controller.TeammateIdentityOnOwner(scope)
		return t.ordinary && err == nil && id == "" && identity.AgentID == "" &&
			t.tools.HasTeamExecutionBindings() && o.Print.NativeLeaderInbox != nil &&
			o.Print.PlanAttachments != nil && o.RunState != nil &&
			o.RunState.Registry.Owns(o.Owner) && o.RunState.Worker.Owns(o.Owner) &&
			o.RunState.GetTasks != nil && o.RunState.IsShuttingDown != nil
	}
	return err == nil && id != "" && id == t.worker.agentID && id == o.Print.AgentID && t.worker.owner == o.Owner && t.worker.controller == o.Controller
}
