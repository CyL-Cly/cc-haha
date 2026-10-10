package sessioncontroller

import (
	"context"
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestInProcessTeammateContextKeepsOuterIdentityAndActualSidechainAuthoritySeparate(t *testing.T) {
	c, state, owner := boundController(t)
	branch, err := NewOwnedGitRecordBranch(state, actualOwnedGitService(t, state, owner))
	if err != nil {
		t.Fatal(err)
	}
	c.options.RecordBranchProfile, c.options.BeginOwnedRecordBranch = OwnedGitRecordProfile, branch
	c.active = true
	c.options.WriterOptions.AgentID = "a123456"
	c.options.ResumeOptions.Environment["CLAUDE_CODE_TASK_LIST_ID"] = "unrelated-standalone"
	identity := InProcessTeammateIdentity{TeamName: "MyTeam_", AgentName: "worker", AgentID: "worker@MyTeam_", ParentSessionID: c.SessionID(), Color: "blue", PlanModeRequired: true}
	var retained *cooperative.Scope
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		retained = scope
		var err error
		c.options.InProcessTeammateContext, err = NewInProcessTeammateContext(scope, owner, "a123456", identity)
		if err != nil {
			return err
		}
		actual, err := c.TeammateIdentityOnOwner(scope)
		if err != nil || actual != identity {
			t.Errorf("outer identity %#v %v", actual, err)
		}
		if list, err := c.TaskListIDOnOwner(scope, "a123456"); err != nil || list != "myteam-" {
			t.Errorf("actual sidechain task list %q %v", list, err)
		}
		if _, err := c.TaskListIDOnOwner(scope, identity.AgentID); !errors.Is(err, cooperative.ErrRestricted) {
			t.Errorf("outer identity incorrectly used as writer %v", err)
		}
		if team, _, _, err := c.TeamWorkerIdentity(scope); err != nil || team != "" {
			t.Errorf("sidechain became MAIN worker %q %v", team, err)
		}
		if err := c.SetLeaderTeamOnOwner(scope, "foreign"); !errors.Is(err, cooperative.ErrRestricted) {
			t.Errorf("teammate became leader %v", err)
		}
		options, err := c.recordOptions(scope)
		if err != nil || options.TeamName != nil || options.AgentName != nil {
			t.Errorf("Source sidechain acquired MAIN record metadata %#v %v", options, err)
		}
		c.options.WriterOptions.AgentID = "other-run"
		if _, err := c.TeammateIdentityOnOwner(scope); !errors.Is(err, cooperative.ErrRestricted) {
			t.Errorf("mismatched sidechain %v", err)
		}
		c.options.WriterOptions.AgentID = "a123456"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.TeammateIdentityOnOwner(retained); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("stale scope admitted", err)
	}
	if _, err := NewInProcessTeammateContext(retained, owner, "a234567", identity); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("stale constructor admitted", err)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error { _, err := c.TeammateIdentityOnOwner(scope); return err }); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("foreign identity admitted", err)
	}
}
