package builtintools

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

func TestActualSourceAutoMemoryForkToolAuthority(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	rows := []map[string]any{}
	for _, name := range []string{"Read", "Grep", "Glob", "Write", "Edit", "Agent", "PowerShell", "NotebookEdit", "TeamCreate", "SendMessage", "TaskStop", "WebFetch", "ToolSearch"} {
		for _, path := range []string{"$HOME/memory/allowed.md", "$HOME/memory/../outside.md", "$HOME/memory-other/no.md", "$HOME/memory"} {
			rows = append(rows, map[string]any{"name": name, "input": map[string]any{"file_path": path, "content": "private", "old_string": "a", "new_string": "b"}})
		}
	}
	for _, command := range []any{"ls", "find . -type f", "cat /tmp/private", "grep -n needle /tmp/private", "git status", "git log -5", "wc -l /tmp/private", "head -1 /tmp/private", "ls && pwd", "ls 2>&1", "ls > output", "find . -delete", "git -c core.pager=evil log", "git status; touch /tmp/no", "echo x", "printf x", "rm -f /tmp/no", "$(touch /tmp/no)", "cat < input", "", nil, 7} {
		rows = append(rows, map[string]any{"name": "Bash", "input": map[string]any{"command": command}})
	}
	// Actual official Flash consolidation commands from the retained v1/v2 live
	// evidence. Compare the original authority; do not relax its read-only rules
	// merely because the task eventually recovered and wrote useful memory.
	liveBytes, err := os.ReadFile("testdata/auto-memory-live-bash-commands.json")
	if err != nil {
		t.Fatal(err)
	}
	var liveRows []map[string]any
	if err = json.Unmarshal(liveBytes, &liveRows); err != nil {
		t.Fatal(err)
	}
	for _, live := range liveRows {
		rows = append(rows, map[string]any{"name": "Bash", "input": live})
	}
	s, owner, home, _ := fixture(t)
	s.options.Environment["USER_TYPE"] = "external"
	fixturePath, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-auto-dream-runtime.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixturePath)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_AUTO_DREAM_RUNTIME_FIXTURE=1", "NODE_ENV=production", "USER_TYPE=external", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"home": home, "permissionRows": rows})
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var source struct {
		Memory       string
		Results      []map[string]any
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.Results) != len(rows) || len(source.Capabilities.Blocked) != 0 {
		t.Fatal("invalid actual Source authority", err, string(out))
	}
	for i := len(rows) - len(liveRows); i < len(rows); i++ {
		if source.Results[i]["behavior"] != "deny" {
			t.Fatalf("live Bash denial is not original Source behavior: case%d %v", i, source.Results[i])
		}
	}
	var authority *cooperative.NativeAutoDreamModels
	if err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		var err error
		authority, err = cooperative.NewNativeAutoDreamModels(scope, owner)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, tools, err := s.Catalog(scope)
		if err != nil {
			return err
		}
		return authority.Start(scope, context.Background(), func(current *cooperative.Scope) error {
			for i, row := range rows {
				name := row["name"].(string)
				tool := queryloop.RegisteredTool{Name: name}
				for _, value := range tools {
					if value.Name == name {
						tool = value
						break
					}
				}
				data, _ := json.Marshal(row["input"])
				in := input(strings.ReplaceAll(string(data), "$HOME", home))
				decision, err := s.AutoMemoryPermission(context.Background(), current, strings.ReplaceAll(source.Memory, "$HOME", home), tool, &in)
				if err != nil {
					t.Errorf("case%d %s: %v", i, name, err)
					continue
				}
				got := map[string]any{"behavior": decision.Behavior}
				if decision.UpdatedInput != nil {
					var updated any
					data, _ := decision.UpdatedInput.MarshalJSON()
					_ = json.Unmarshal([]byte(strings.ReplaceAll(string(data), home, "$HOME")), &updated)
					got["updatedInput"] = updated
				}
				for key, value := range map[string]json.RawMessage{"message": decision.Message, "decisionReason": decision.DecisionReason} {
					if value != nil {
						var parsed any
						_ = json.Unmarshal([]byte(strings.ReplaceAll(string(value), home, "$HOME")), &parsed)
						got[key] = parsed
					}
				}
				if !reflect.DeepEqual(got, source.Results[i]) {
					t.Errorf("case%d %s input%v Source%v Go%v", i, name, row["input"], source.Results[i], got)
				}
			}
			return nil
		}, func(_ *cooperative.Scope, err error) {
			if err != nil {
				t.Error(err)
			}
		})
	}); err != nil {
		t.Fatal(err)
	}
}
