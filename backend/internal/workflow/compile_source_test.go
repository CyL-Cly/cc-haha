package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflowcommands"
)

func TestActualSourceWorkflowPreparationPlainLanguageAndMetadata(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	scripts := []string{"export const meta={name:'plain',description:'A test',phases:[{title:'first',detail:'one',model:'fixture-model'}]};\nreturn 1", "export const meta={name:'agents',description:'A test'};\nreturn await agent('Read the actual file',{model:'fixture-model'})", "export const meta={name:'pipeline',description:'A test'};\nreturn await pipeline([1,2],async x=>x+1,async x=>x*2)", "export const meta={name:'fanout',description:'A test'};\nreturn await parallel([async()=>1,async()=>2])", "export const meta={name:'date',description:'A test'};\nreturn Date.now()", "export const meta={name:'random',description:'A test'};\nreturn Math.random()", "export const meta={name:'date-args',description:'A test'};\nreturn new Date('2020-01-01').toISOString()", "export const meta={name:'bracket',description:'A test'};\nreturn Date['now']()", "export const meta={name:'unicode',description:'𐐀 and 中文'};\nreturn {value:'𐐀中文'}", "const meta={name:'missing-export',description:'test'};\nreturn 1", "export const meta={name:'bad syntax',description:'test'};\nreturn 1", "export const meta={name:'typing',description:'test'};\nconst value:number=1;return value"}
	inputs := []json.RawMessage{}
	for _, script := range scripts {
		inputs = append(inputs, quoted(script))
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-workflow.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_WORKFLOW_FIXTURE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"homeDir": home, "preparations": inputs})
	cmd.Stdin = bytes.NewReader(input)
	output, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.3000s", e, output)
	}
	var source struct {
		Preparations []struct {
			OK     bool
			Error  string
			Meta   protocol.JSONObject
			Body   string
			Banned bool
		}
		Capabilities struct{ Blocked []string }
	}
	if e := json.Unmarshal(output, &source); e != nil || len(source.Preparations) != len(inputs) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(e, len(source.Preparations), source.Capabilities.Blocked)
	}
	owner := cooperative.New()
	defer owner.Close()
	e = owner.Run(ctx, func(scope *cooperative.Scope) error {
		metadata, e := workflowcommands.NewMetadata(scope, owner)
		if e != nil {
			return e
		}
		for i, script := range inputs {
			units, e := protocol.DecodeJavaScriptJSONString(script)
			if e != nil {
				return e
			}
			parsed, e := metadata.Parse(scope, units)
			if e != nil {
				return e
			}
			expected := source.Preparations[i]
			if !expected.OK {
				if parsed.Error != expected.Error {
					return fmt.Errorf("Source preparation error%d Go=%q Source=%q", i, parsed.Error, expected.Error)
				}
				continue
			}
			if parsed.ErrorUnits != nil {
				return fmt.Errorf("native metadata rejected Source valid%d: %s", i, parsed.Error)
			}
			if _, e := CompileBody(parsed.ScriptBodyUnits); e != nil {
				return e
			}
			banned, e := metadata.UsesBanned(scope, parsed.ScriptBodyUnits)
			if e != nil {
				return e
			}
			canonical := func(value protocol.JSONObject) any {
				data, _ := value.MarshalJSON()
				var out any
				_ = json.Unmarshal(data, &out)
				return out
			}
			if parsed.ScriptBody != expected.Body || banned != expected.Banned || !reflect.DeepEqual(canonical(parsed.Meta), canonical(expected.Meta)) {
				return fmt.Errorf("Source metadata/body/warning changed%d", i)
			}
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}
