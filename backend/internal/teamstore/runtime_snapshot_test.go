package teamstore

import (
	"bytes"
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

func TestRuntimeSnapshotIdentityMembersAndDuplicateKeysMatchSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	home := t.TempDir()
	configDir := filepath.Join(home, ".claude")
	tasks, _ := taskstore.New(taskstore.Options{ConfigDir: configDir, PID: os.Getpid(), Now: time.Now})
	config, _ := NewConfig(ConfigOptions{ConfigDir: configDir, TaskStore: tasks, PID: os.Getpid()})
	base := object([]byte(`{"name":"original-name","leadSessionId":"leader","leadAgentId":"team-lead@original-name","createdAt":1000,"members":[{"agentId":"a@team","name":"one","agentType":"reader","cwd":"private","backendType":"process","tmuxPaneId":"","joinedAt":1200},{"agentId":"b@team","name":"two","backendType":"in-process","joinedAt":1300},{"agentId":"a@team","name":"last","backendType":"process","joinedAt":1400}]}`))
	type row struct {
		Team          *protocol.JSONObject `json:"team"`
		Created       json.RawMessage      `json:"createdAt"`
		Session, Path string
	}
	cases := []row{{&base, raw(1000), "leader", config.Path("team")}, {&base, raw(1001), "leader", config.Path("team")}, {&base, raw(1000), "foreign", config.Path("team")}, {nil, raw(1000), "leader", config.Path("team")}, {&base, raw("1000"), "leader", config.Path("team")}}
	empty := base.Clone()
	empty.Set("members", raw([]any{}))
	cases = append(cases, row{&empty, raw(1000), "leader", config.Path("team")})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-runtime-snapshot.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + configDir, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_SNAPSHOT_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	inputs := []map[string]any{}
	for _, c := range cases {
		inputs = append(inputs, map[string]any{"team": c.Team, "createdAt": c.Created, "sessionId": c.Session, "path": c.Path})
	}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": home, "cases": inputs}))
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
	if err = json.Unmarshal(out, &source); err != nil || len(source.Capabilities.Blocked) > 0 || len(source.Rows) != len(cases) {
		t.Fatal(err, string(out))
	}
	for i, c := range cases {
		if c.Team != nil {
			if err = config.Write("team", *c.Team); err != nil {
				t.Fatal(err)
			}
		} else {
			_ = os.Remove(config.Path("team"))
		}
		v, err := config.RuntimeSnapshot("team", c.Created, c.Session)
		var value, message any
		if err == nil {
			value = v
		} else {
			message = err.Error()
		}
		actual := raw(map[string]any{"value": value, "error": message})
		var a, b any
		_ = json.Unmarshal(actual, &a)
		_ = json.Unmarshal(source.Rows[i], &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("case%d Go%s Source%s", i, actual, source.Rows[i])
		}
	}
}
