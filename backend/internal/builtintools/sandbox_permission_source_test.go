package builtintools

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceSandboxAutoAllowFullDenyAskCompoundAndProvenance(t *testing.T) {
	rows := []map[string]any{}
	for _, command := range []string{"echo text", "echo text && rm -rf owned", "rm owned; echo text", "FOO=x timeout 10 rm owned", "LD_PRELOAD=x rm owned", "PATH=else rm owned", "xargs rm owned", "nice -n 3 rm owned", "echo text > out", "echo text 2> err", `echo 'two words' > out`, "cat <<'EOF' > out\n${}\nEOF", "echo $(rm owned)", "echo 'unterminated", "echo '' > out", "echo '; rm'"} {
		for _, deny := range [][]string{{}, {"Bash(rm:*)"}, {"Bash(*rm*)", "Read"}, {"Bash(echo text)", "Bash(rm:*)"}, {"Bash(echo:*)"}} {
			for _, ask := range [][]string{{}, {"Bash(*echo*)"}, {"Bash(rm owned)"}, {"Bash(*rm*)"}} {
				context := map[string]any{"mode": "default", "alwaysDenyRules": map[string]any{"userSettings": deny, "session": []string{"Bash(rm:*)"}}, "alwaysAskRules": map[string]any{"projectSettings": ask, "session": []string{"Bash(own:*)"}}, "alwaysAllowRules": map[string]any{"cliArg": []string{"Bash(*)"}}}
				rows = append(rows, map[string]any{"input": map[string]any{"command": command, "description": "original", "unknown": map[string]any{"keep": true}}, "context": context})
			}
		}
	}
	data, _ := json.Marshal(map[string]any{"rows": rows})
	fixture, _ := filepath.Abs("testdata/sandbox-auto-permission.fixture.ts")
	root, _ := filepath.Abs("../../..")
	home := t.TempDir()
	cmd := exec.Command(os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", "--preload", filepath.Join(root, "preload.ts"), fixture)
	cmd.Env = []string{"HOME=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=test", "DISABLE_TELEMETRY=1"}
	cmd.Stdin = bytes.NewReader(data)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	var source []json.RawMessage
	if e = json.Unmarshal(out, &source); e != nil {
		t.Fatal(e, string(out))
	}
	for i, row := range rows {
		input, _ := json.Marshal(row["input"])
		context, _ := json.Marshal(row["context"])
		var in, ctx protocol.JSONObject
		in.UnmarshalJSON(input)
		ctx.UnmarshalJSON(context)
		decision, e := sandboxAutoPermissionForContext(in, ctx, "darwin")
		if e != nil {
			t.Fatal(i, e)
		}
		projected := protocol.NewJSONObject(field("behavior", decision.Behavior))
		if decision.Message != nil {
			projected.Set("message", decision.Message)
		}
		if decision.DecisionReason != nil {
			projected.Set("decisionReason", decision.DecisionReason)
		}
		if decision.UpdatedInput != nil {
			projected.Set("updatedInput", raw(*decision.UpdatedInput))
		}
		var expected, actual any
		json.Unmarshal(source[i], &expected)
		json.Unmarshal(raw(projected), &actual)
		if !reflect.DeepEqual(actual, expected) {
			t.Fatalf("Source%d %+v\nSource%s\nGo%s", i, row, source[i], raw(projected))
		}
	}
}
