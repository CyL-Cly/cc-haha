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

func TestActualSourceSwarmAPIFieldsFirstCacheAndUnchangedLocalParser(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0700); err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../testdata/native-default-task-prompts.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, bun, "--no-env-file", "--feature=TRANSCRIPT_CLASSIFIER", fixture)
	child.Dir = home
	child.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "NODE_ENV=production", "CC_HAHA_DEFAULT_TASK_PROMPTS_FIXTURE=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1", "PATH=/usr/bin:/bin"}
	child.Stdin = bytes.NewReader(array(map[string]any{"home": home, "rows": []any{}}))
	output, err := child.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	var source struct {
		SwarmSchemas     [][]json.RawMessage
		LocalAgentSchema json.RawMessage
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.SwarmSchemas) != 5 {
		t.Fatal(err, string(output))
	}
	defs := []Definition{}
	for _, name := range []string{"Agent", "ExitPlanMode"} {
		def, err := NativeBuiltinDefinition(name)
		if err != nil {
			t.Fatal(err)
		}
		defs = append(defs, def)
	}
	session := NewSession()
	for i, enabled := range []bool{false, true, true, false, false} {
		if i == 0 || i == 2 || i == 4 {
			session.Clear()
		}
		rendered, err := session.RenderBatch(defs, Options{Host: Host{AgentDefinitions: []AgentPromptDefinition{}, TeamEnabled: enabled}, Model: "deepseek-flash"})
		if err != nil {
			t.Fatal(err)
		}
		if len(source.SwarmSchemas[i]) != len(rendered) {
			t.Fatal("Source schema count", i)
		}
		for j, tool := range rendered {
			if !jsonSourcesEqual(tool.Get("input_schema"), source.SwarmSchemas[i][j]) {
				t.Errorf("actual Source cached API schema step%d %s\nSource%s\nGo%s", i, defs[j].Name, source.SwarmSchemas[i][j], tool.Get("input_schema"))
			}
		}
	}
	local, err := OriginalInputJSONSchema(defs[0])
	if err != nil || !jsonSourcesEqual(local, source.LocalAgentSchema) {
		t.Fatal("API filtering altered original local definition", err, string(local), string(source.LocalAgentSchema))
	}
}
