package attachments_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/attachments"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestRetainedSkillFileDirectoryAndAgentProjectionUsesOriginalToolMapper(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if e := os.MkdirAll(filepath.Join(home, "tmp"), 0700); e != nil {
		t.Fatal(e)
	}
	rows := []originalCase{}
	for i, path := range []string{"/fixture/a", "/fixture/a b", "/fixture/a'b", `/fixture/a'"$!`, `C:\Users\file`, "/fixture/文件"} {
		data, _ := json.Marshal(map[string]string{"type": "directory", "path": path, "content": "zeta\nalpha\n"})
		rows = append(rows, originalCase{Name: fmt.Sprintf("directory%d", i), Kind: "projection", Source: string(data)})
	}
	for i, raw := range []string{
		`{"type":"file","filename":"/fixture/human.png","content":{"type":"image","file":{"base64":"aW1hZ2UtYnl0ZXM=","type":"image/png","originalSize":11,"dimensions":{"originalWidth":10,"originalHeight":20,"displayWidth":10,"displayHeight":20}}}}`,
		`{"type":"file","filename":"/fixture/a","content":{"type":"text","file":{"filePath":"/fixture/a","content":"one\r\ntwo\n","numLines":3,"startLine":4,"totalLines":8}}}`,
		`{"type":"file","filename":"/fixture/a","content":{"type":"text","file":{"filePath":"/fixture/a","content":"","numLines":0,"startLine":1,"totalLines":0}}}`,
		`{"type":"file","filename":"/fixture/a","content":{"type":"text","file":{"filePath":"/fixture/a","content":"","numLines":0,"startLine":12,"totalLines":3}}}`,
		`{"type":"file","filename":"/fixture/a","truncated":true,"content":{"type":"text","file":{"filePath":"/fixture/a","content":"one","numLines":1,"startLine":1,"totalLines":3000}}}`,
		`{"type":"compact_file_reference","filename":"/fixture/大文件"}`,
		`{"type":"plan_file_reference","planFilePath":"/fixture/plan.md","planContent":"必须继续 \ud800"}`,
		`{"type":"task_status","taskId":"child-id","taskType":"local_agent","description":"真实任务","status":"running","deltaSummary":"progress","outputFilePath":"/fixture/out"}`,
		`{"type":"task_status","taskId":"child-id","taskType":"local_agent","description":"task","status":"running"}`,
		`{"type":"task_status","taskId":"child-id","taskType":"local_agent","description":"task","status":"killed","deltaSummary":"ignore"}`,
		`{"type":"task_status","taskId":"child-id","taskType":"local_agent","description":"task","status":"completed","deltaSummary":"result","outputFilePath":"/fixture/out"}`,
		`{"type":"task_status","taskId":"child-id","taskType":"local_agent","description":"task","status":"failed"}`,
		`{"type":"agent_mention","agentType":"custom:review"}`,
		`{"type":"agent_mention","agentType":"\ud800"}`,
		`{"type":"mcp_resource","server":"fixture","uri":"scheme:path"}`,
		`{"type":"mcp_resource","server":"fixture","uri":"scheme:path","content":{"contents":[]}}`,
		`{"type":"mcp_resource","server":"fixture","uri":"scheme:path","content":{"contents":[null,{},3]}}`,
		`{"type":"mcp_resource","server":"fixture","uri":"scheme:path","content":{"contents":[{"uri":"scheme:path","text":"first\n\ud800"},{"uri":"scheme:other","text":"second"}]}}`,
		`{"type":"mcp_resource","server":"fixture","uri":"scheme:path","content":{"contents":[{"uri":"scheme:path","blob":"AAAA","mimeType":"image/png"},{"uri":"scheme:other","blob":null}]}}`,
		`{"type":"async_hook_response","response":{}}`,
		`{"type":"async_hook_response","response":{"systemMessage":"","hookSpecificOutput":{"additionalContext":""}}}`,
		`{"type":"async_hook_response","response":{"systemMessage":"first\ud800","hookSpecificOutput":{"additionalContext":"second🐋"},"decision":"block","continue":false,"suppressOutput":true}}`,
		`{"type":"async_hook_response","response":false}`,
		`{"type":"async_hook_response","response":[]}`,
		`{"type":"async_hook_response","response":{"systemMessage":7,"hookSpecificOutput":{"additionalContext":true}}}`,
		`{"type":"async_hook_response","response":{"systemMessage":[{"type":"text","text":"array\udfff","future":true},{"type":"image","source":{"type":"base64","data":"AAA"}}],"hookSpecificOutput":{"additionalContext":[{"type":"text"},{"type":"text","text":null}]}}}`,
		`{"type":"async_hook_response","response":{"systemMessage":{"future":true}}}`,
	} {
		rows = append(rows, originalCase{Name: fmt.Sprintf("attachment%d", i), Kind: "projection", Source: raw})
	}
	observed := []observation{}
	for _, row := range rows {
		result := observation{Name: row.Name, Before: js([]byte(row.Source)), After: js([]byte(row.Source)), Trace: []map[string]string{}}
		count := 0
		factory, _ := messages.NewFactory(func() string {
			count++
			id := fmt.Sprintf("skills-id-%d", count)
			result.Trace = append(result.Trace, map[string]string{"kind": "uuid", "value": id})
			return id
		}, func() time.Time {
			result.Trace = append(result.Trace, map[string]string{"kind": "date", "value": "2023-11-14T22:13:20.000Z"})
			return time.UnixMilli(1700000000000)
		})
		projected, e := attachments.ProjectWithOptions([]byte(row.Source), factory, &attachments.PlanOptions{MapToolResult: func(name string, data, id json.RawMessage) (protocol.JSONObject, error) {
			out, e := builtintools.MapResult(name, data, id)
			var unsupported *builtintools.CapabilityError
			if errors.As(e, &unsupported) {
				return out, &attachments.CapabilityError{Boundary: unsupported.Boundary}
			}
			return out, e
		}})
		if e != nil {
			t.Fatal(e)
		}
		result.Output = sources(projected)
		observed = append(observed, result)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/retained-skills.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + root, "BUN_OPTIONS=--no-env-file", "CC_HAHA_RETAINED_SKILLS_FIXTURE=1", "NODE_ENV=production", "ANTHROPIC_API_KEY=fake-native-key", "PATH=/usr/bin:/bin"}
	inputs := []map[string]string{}
	for _, row := range rows {
		inputs = append(inputs, map[string]string{"name": row.Name, "kind": row.Kind, "source": row.Source})
	}
	data, _ := json.Marshal(map[string]any{"home": home, "rows": inputs})
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, e := cmd.Output()
	if e != nil {
		t.Fatalf("Source %v %.3000s", e, stderr.String())
	}
	var source struct {
		Observations []observation
		SourceOnly   struct{ Capabilities struct{ Blocked []string } }
	}
	if e = json.Unmarshal(output, &source); e != nil || len(source.Observations) != len(rows) || len(source.SourceOnly.Capabilities.Blocked) > 0 {
		t.Fatal(e, len(source.Observations), source.SourceOnly.Capabilities.Blocked)
	}
	for i := range observed {
		if !reflect.DeepEqual(observed[i], source.Observations[i]) {
			a, _ := json.Marshal(observed[i])
			b, _ := json.Marshal(source.Observations[i])
			t.Errorf("%s Go=%s Source=%s", rows[i].Name, a, b)
		}
	}
}
