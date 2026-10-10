package sdkhost

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
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

func TestNativeHookIfUsesActualSourceSchemasCanonicalToolAndPatternConsumers(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	home := t.TempDir()
	temp := filepath.Join(home, "tmp")
	if err := os.MkdirAll(temp, 0700); err != nil {
		t.Fatal(err)
	}
	type row struct {
		Event      string              `json:"event"`
		Name       string              `json:"name"`
		Input      protocol.JSONObject `json:"input"`
		Conditions []string            `json:"conditions"`
	}
	rows := []row{}
	for _, name := range []string{"Read", "Write", "Edit", "Glob", "Grep", "Agent", "Bash"} {
		for _, text := range []string{"actual/file.txt", "plain.txt", "x\nfile.txt", "unicode\u00a0/文件.txt", "literal*value"} {
			input := protocol.NewJSONObject(protocol.JSONField{Name: "file_path", Value: quote(text)}, protocol.JSONField{Name: "content", Value: quote("actual")}, protocol.JSONField{Name: "old_string", Value: quote("old")}, protocol.JSONField{Name: "new_string", Value: quote("new")})
			if name == "Read" {
				input.Delete("content")
				input.Delete("old_string")
				input.Delete("new_string")
			}
			if name == "Write" {
				input.Delete("old_string")
				input.Delete("new_string")
			}
			if name == "Edit" {
				input.Delete("content")
			}
			if name == "Glob" || name == "Grep" {
				input = protocol.NewJSONObject(protocol.JSONField{Name: "pattern", Value: quote(text)})
			}
			if name == "Bash" {
				input = protocol.NewJSONObject(protocol.JSONField{Name: "command", Value: quote(text)})
			}
			if name == "Agent" {
				input = protocol.NewJSONObject(protocol.JSONField{Name: "description", Value: quote("actual")}, protocol.JSONField{Name: "prompt", Value: quote("actual")})
			}
			conditions := []string{name, name + "(*)", name + "()", name + "(*file*)", name + "(plain.txt)", name + "(actual/*)", "Task", "Task(*)", "Task(actual)", "Wrong(*)"}
			rows = append(rows, row{"PreToolUse", name, input, conditions})
		}
		rows = append(rows, row{"PostToolUse", name, protocol.NewJSONObject(), []string{name, name + "(something)", "Wrong"}})
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-hook-command.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_NATIVE_HOOK_COMMAND_FIXTURE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"homeDir": home, "ifMatchers": rows})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Rows         [][]bool
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.Rows) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(out))
	}
	for i, r := range rows {
		definition, err := toolschema.NativeBuiltinDefinition(r.Name)
		if err != nil {
			t.Fatal(err)
		}
		tool := queryloop.RegisteredTool{Name: r.Name, ParseInput: func(input protocol.JSONObject) (protocol.JSONObject, error) {
			return toolschema.ParseInput(definition, input)
		}}
		matcher := nativeHookIfMatcher(tool, &r.Input)
		actual := []bool{}
		for _, condition := range r.Conditions {
			value, err := matcher(quote(condition))
			if err != nil {
				t.Fatal(err)
			}
			actual = append(actual, value)
		}
		if !reflect.DeepEqual(actual, source.Rows[i]) {
			t.Errorf("row%d %s Go%v Source%v", i, r.Name, actual, source.Rows[i])
		}
	}
}
