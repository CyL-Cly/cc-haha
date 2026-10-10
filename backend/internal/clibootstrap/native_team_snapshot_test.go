package clibootstrap

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
)

func TestNativeTeamSnapshotReadsPhysicalIdentityAndChangesLeaderTaskList(t *testing.T) {
	m := backgroundAgentMain(t, "http://127.0.0.1:1")
	ctx := context.Background()
	config, err := teamstore.NewConfig(teamstore.ConfigOptions{ConfigDir: m.options.Native.Environment["CLAUDE_CONFIG_DIR"], TaskStore: m.taskStore, PID: os.Getpid()})
	if err != nil {
		t.Fatal(err)
	}
	var team protocol.JSONObject
	_ = team.UnmarshalJSON([]byte(`{"name":"ActualTeam_","createdAt":1000,"members":[],"leadSessionId":""}`))
	team.Set("leadSessionId", quoteMain(m.Controller.SessionID()))
	if err = config.Write("ActualTeam_", team); err != nil {
		t.Fatal(err)
	}
	if err = m.SetTeamRuntimeSnapshot(ctx, "ActualTeam_", json.RawMessage(`1001`)); err == nil {
		t.Fatal("stale snapshot accepted")
	}
	if m.teamState != nil {
		t.Fatal("failed snapshot changed actual state")
	}
	if err = m.SetTeamRuntimeSnapshot(ctx, "ActualTeam_", json.RawMessage(`1000`)); err != nil {
		t.Fatal(err)
	}
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		list, err := m.Controller.TaskListIDOnOwner(scope, "")
		if err != nil {
			return err
		}
		if list != "actualteam-" || m.teamState.snapshot.String("teamName") != "ActualTeam_" {
			t.Errorf("actual identity/list lost %s", list)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
