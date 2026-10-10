package sessioncontroller

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestInProcessTeammateIdentityAndSidechainMetadataAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	c, state, owner := boundController(t)
	c.active = true
	branch, err := NewOwnedGitRecordBranch(state, actualOwnedGitService(t, state, owner))
	if err != nil {
		t.Fatal(err)
	}
	c.options.RecordBranchProfile, c.options.BeginOwnedRecordBranch = OwnedGitRecordProfile, branch
	rows, actual := []map[string]any{}, []map[string]any{}
	for i, name := range []string{"worker", "other"} {
		writer := []string{"a123456", "a234567"}[i]
		identity := InProcessTeammateIdentity{TeamName: "MyTeam_", AgentName: name, AgentID: name + "@MyTeam_", ParentSessionID: c.SessionID(), Color: []string{"blue", "red"}[i], PlanModeRequired: i == 0}
		rows = append(rows, map[string]any{"writerId": writer, "identity": map[string]any{"teamName": identity.TeamName, "agentName": name, "agentId": identity.AgentID, "parentSessionId": identity.ParentSessionID, "color": identity.Color, "planModeRequired": identity.PlanModeRequired}})
		if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			var err error
			c.options.WriterOptions.AgentID = writer
			c.options.InProcessTeammateContext, err = NewInProcessTeammateContext(scope, owner, writer, identity)
			if err != nil {
				return err
			}
			got, err := c.TeammateIdentityOnOwner(scope)
			if err != nil {
				return err
			}
			list, err := c.TaskListIDOnOwner(scope, writer)
			if err != nil {
				return err
			}
			options, err := c.recordOptions(scope)
			if err != nil {
				return err
			}
			actual = append(actual, map[string]any{"identity": got, "list": list, "records": []map[string]any{{"isSidechain": true, "agentId": writer, "teamName": options.TeamName, "agentName": options.AgentName, "sessionId": c.SessionID()}}})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-inprocess-teammate-context.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_INPROCESS_TEAM_CONTEXT_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"homeDir": home, "sessionId": c.SessionID(), "rows": rows})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String(), string(output))
	}
	var report struct {
		Rows         json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &report); err != nil || len(report.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(output), stderr.String())
	}
	want, _ := json.Marshal(actual)
	var a, b any
	_ = json.Unmarshal(want, &a)
	_ = json.Unmarshal(report.Rows, &b)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("Go %s\nSource %s", want, report.Rows)
	}
}
