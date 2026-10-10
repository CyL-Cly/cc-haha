package toolschema

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestActualTaskDescriptionsUseSourceTeamGatesAndCachedFirstRender(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	rows := []map[string]any{
		{"env": map[string]string{"USER_TYPE": "external"}},
		{"env": map[string]string{"USER_TYPE": "external", "CC_HAHA_AGENT_TEAMS_ENABLED": "1"}},
		{"env": map[string]string{"USER_TYPE": "ant", "CC_HAHA_AGENT_TEAMS_ENABLED": "0"}},
		{"env": map[string]string{"USER_TYPE": "ant"}},
		{"env": map[string]string{"USER_TYPE": "external", "CC_HAHA_AGENT_TEAMS_ENABLED": "0", "CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS": "1"}},
		{"env": map[string]string{"USER_TYPE": "external", "CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS": "yes"}},
		{"env": map[string]string{"USER_TYPE": "external", "CC_HAHA_AGENT_TEAMS_DEFAULT": "true"}},
		{"env": map[string]string{"USER_TYPE": "external"}, "argv": []string{"--agent-teams"}},
		{"env": map[string]string{"USER_TYPE": "external", "CC_HAHA_AGENT_TEAMS_ENABLED": "1"}, "cachedFeatures": map[string]any{"tengu_amber_flint": false}},
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../testdata/native-default-task-prompts.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, bun, "--no-env-file", "--feature=TRANSCRIPT_CLASSIFIER", fixture)
	child.Dir = home
	child.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "NODE_ENV=production", "CC_HAHA_DEFAULT_TASK_PROMPTS_FIXTURE=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1", "PATH=/usr/bin:/bin"}
	child.Stdin = bytes.NewReader(array(map[string]any{"home": home, "rows": rows}))
	var stderr bytes.Buffer
	child.Stderr = &stderr
	data, err := child.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Rows []struct {
			Enabled      bool
			Create, List string
		}
		Cache [][]string
	}
	if err = json.Unmarshal(data, &source); err != nil || len(source.Rows) != len(rows) {
		t.Fatal(err, string(data))
	}
	for i, row := range source.Rows {
		for _, name := range []string{"TaskCreate", "TaskList"} {
			def, _ := NativeBuiltinDefinition(name)
			session := NewSession()
			schemas, err := session.RenderBatch([]Definition{def}, Options{Host: Host{TeamEnabled: row.Enabled}})
			if err != nil {
				t.Fatal(err)
			}
			expected := row.Create
			if name == "TaskList" {
				expected = row.List
			}
			if schemas[0].String("description") != expected {
				t.Errorf("actual Source row%d %s team%v differs", i, name, row.Enabled)
			}
		}
	}
	defs := []Definition{}
	for _, name := range []string{"TaskCreate", "TaskList"} {
		def, _ := NativeBuiltinDefinition(name)
		defs = append(defs, def)
	}
	session := NewSession()
	for index, enabled := range []bool{true, false, false} {
		if index == 2 {
			session.Clear()
		}
		rendered, err := session.RenderBatch(defs, Options{Host: Host{TeamEnabled: enabled}})
		if err != nil {
			t.Fatal(err)
		}
		for i, tool := range rendered {
			if tool.String("description") != source.Cache[index][i] {
				t.Fatalf("actual Source cached prompt transition %d %s differs", index, defs[i].Name)
			}
		}
	}
}
