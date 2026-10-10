package teamstore

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
)

func TestActualTeamCreateDeleteNameReservationGenerationAndRegisteredIdlePolicyMatchSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	home := t.TempDir()
	rows := []protocol.JSONObject{object([]byte(`{"name":"collision","sessionId":"first","description":"original","agentType":"researcher"}`)), object([]byte(`{"name":"collision","sessionId":"second"}`)), object([]byte(`{"name":"already","sessionId":"third","existing":"current-team"}`)), object([]byte(`{"name":"finished","sessionId":"fourth","delete":true}`)), object([]byte(`{"name":"idle-alive","sessionId":"fifth","delete":true,"active":true,"terminated":false}`)), object([]byte(`{"name":"terminated","sessionId":"sixth","delete":true,"active":true,"terminated":true}`))}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-manager.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_MANAGER_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "ANTHROPIC_API_KEY=fake-private", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": home, "cases": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Rows         []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.Rows) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(out))
	}
	configDir := filepath.Join(t.TempDir(), ".claude")
	tasks, _ := taskstore.New(taskstore.Options{ConfigDir: configDir, PID: os.Getpid(), Now: func() time.Time { return time.UnixMilli(1000) }})
	config, _ := NewConfig(ConfigOptions{ConfigDir: configDir, TaskStore: tasks, PID: os.Getpid()})
	cleanup, _ := NewLifecycle(LifecycleOptions{Config: config, Tasks: tasks, RuntimeResources: func(context.Context, string, []protocol.JSONObject, bool) error { return nil }})
	for i, row := range rows {
		manager, _ := NewManager(ManagerOptions{Config: config, Tasks: tasks, Lifecycle: cleanup, Now: func() time.Time { return time.UnixMilli(1000) }, WordSlug: func() (string, error) { return "collision-generated", nil }})
		created, failure := manager.Create(context.Background(), CreationRequest{TeamName: row.String("name"), Description: row.String("description"), HasDescription: row.Get("description") != nil, AgentType: row.String("agentType"), LeaderModel: "fixture-model", CWD: home, SessionID: row.String("sessionId"), ExistingTeam: row.String("existing")})
		var createdValue, deletedValue, message any
		name := row.String("name")
		if failure == nil {
			name = created.TeamName
			createdValue = map[string]string{"team_name": name, "lead_agent_id": created.LeadAgentID}
			if string(row.Get("delete")) == "true" {
				_, err = tasks.Create(SanitizeName(name), object([]byte(`{"subject":"Actual visible task","description":"retained","status":"pending","blocks":[],"blockedBy":[]}`)))
				if err != nil {
					t.Fatal(err)
				}
				if string(row.Get("active")) == "true" {
					team := config.Read(name)
					members := []protocol.JSONObject{}
					_ = json.Unmarshal(team.Get("members"), &members)
					members = append(members, protocol.NewJSONObject(protocol.JSONField{Name: "agentId", Value: raw("worker@" + name)}, protocol.JSONField{Name: "name", Value: raw("worker")}, protocol.JSONField{Name: "agentType", Value: raw("reader")}, protocol.JSONField{Name: "joinedAt", Value: raw(1000)}, protocol.JSONField{Name: "model", Value: raw("fixture-model")}, protocol.JSONField{Name: "cwd", Value: raw(home)}, protocol.JSONField{Name: "tmuxPaneId", Value: raw("")}, protocol.JSONField{Name: "subscriptions", Value: raw([]string{})}, protocol.JSONField{Name: "backendType", Value: raw("process")}, protocol.JSONField{Name: "terminated", Value: row.Get("terminated")}))
					team.Set("members", raw(members))
					if err = config.Write(name, *team); err != nil {
						t.Fatal(err)
					}
				}
				var deleted protocol.JSONObject
				deleted, failure = manager.Delete(context.Background(), name)
				if failure == nil {
					deletedValue = deleted
				}
			}
		}
		if failure != nil {
			message = failure.Error()
		}
		var physical any
		if data, err := os.ReadFile(config.Path(name)); err == nil {
			physical = string(data)
		}
		var registered any
		if token := manager.Registered(name); token != nil {
			registered = map[string]any{"generation": token.Generation, "identity": token.Identity}
		}
		actual := raw(map[string]any{"created": createdValue, "deleted": deletedValue, "error": message, "physical": physical, "registered": registered})
		var a, b any
		_ = json.Unmarshal(actual, &a)
		_ = json.Unmarshal(source.Rows[i], &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("case%d Go%s\nSource%s", i, actual, source.Rows[i])
		}
	}
}
