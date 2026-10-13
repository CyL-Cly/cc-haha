package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestInspectionTranscriptActualSourceMetadataUsageContextAndLimits(t *testing.T) {
	for _, direct := range []bool{true, false} {
		t.Run(fmt.Sprintf("direct-credential-%v", direct), func(t *testing.T) { runInspectionTranscriptActualSource(t, direct) })
	}
}
func runInspectionTranscriptActualSource(t *testing.T, direct bool) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun unavailable")
	}
	rows := []map[string]any{}
	add := func(name, content string) {
		rows = append(rows, map[string]any{"name": name, "id": fmt.Sprintf("11111111-1111-4111-8111-%012d", len(rows)+1), "content": content})
	}
	entry := func(kind, id string, content any, fields map[string]any) string {
		value := map[string]any{"type": kind, "uuid": id, "timestamp": "2026-10-07T00:00:00Z", "message": map[string]any{"role": kind, "content": content}}
		for key, raw := range fields {
			value[key] = raw
		}
		data, _ := json.Marshal(value)
		return string(data) + "\n"
	}
	usageEntry := func(id, model string, tokens any, fields map[string]any) string {
		if fields == nil {
			fields = map[string]any{}
		}
		fields["message"] = map[string]any{"id": id, "role": "assistant", "model": model, "content": []any{map[string]any{"type": "text", "text": "physical response"}}, "usage": tokens}
		return entry("assistant", id, nil, fields)
	}
	usage := map[string]any{"input_tokens": 12, "output_tokens": 8, "cache_read_input_tokens": 4, "cache_creation_input_tokens": 3}
	add("empty", "")
	add("metadata-only", `{"type":"session-meta","workDir":"$HOME","permissionMode":"plan","runtimeProviderId":null,"runtimeModelId":"deepseek-flash[1m]","effortLevel":"high"}`+"\n")
	add("one-model-usage", usageEntry("api1", "deepseek-flash", usage, nil))
	add("same-api-usage-repeat", usageEntry("api1", "deepseek-flash", usage, nil)+usageEntry("api1", "deepseek-flash", usage, nil))
	add("different-request-usage", usageEntry("api1", "deepseek-flash", usage, map[string]any{"requestId": "first"})+usageEntry("api1", "deepseek-flash", usage, map[string]any{"requestId": "second"}))
	add("fork-inherited-context-not-billable", usageEntry("api1", "deepseek-flash", usage, map[string]any{"forkedFrom": map[string]any{"sessionId": "parent", "messageUuid": "u1"}}))
	add("invalid-version-not-billable", usageEntry("api1", "deepseek-flash", usage, map[string]any{"version": "foreign"}))
	add("latest-model-without-usage", entry("assistant", "api1", nil, map[string]any{"message": map[string]any{"role": "assistant", "model": "deepseek-flash", "content": "nonzero plain context"}}))
	add("known-pricing", usageEntry("api1", "claude-sonnet-4-6", usage, nil))
	add("compact-resets-context-only", usageEntry("api1", "deepseek-flash", usage, nil)+`{"type":"system","subtype":"compact_boundary","uuid":"boundary"}`+"\n"+entry("user", "after", "continued", nil))
	add("tail-estimate-after-provider-usage", usageEntry("api1", "deepseek-flash", usage, nil)+entry("user", "after", strings.Repeat("x", 512), nil))
	attachment := func(value any) string {
		data, _ := json.Marshal(map[string]any{"type": "attachment", "attachment": value})
		return string(data) + "\n"
	}
	textFile := map[string]any{"type": "file", "filename": "$HOME/read.txt", "displayPath": "read.txt", "content": map[string]any{"type": "text", "file": map[string]any{"filePath": "$HOME/read.txt", "content": "actual retained source file 中文🙂\nsecond line", "numLines": 2, "startLine": 1, "totalLines": 2}}}
	add("retained-read-file-after-usage", usageEntry("api1", "deepseek-flash", usage, nil)+attachment(textFile))
	truncatedFile := map[string]any{}
	for key, value := range textFile {
		truncatedFile[key] = value
	}
	truncatedFile["truncated"] = true
	add("retained-truncated-file-before-usage", attachment(truncatedFile)+usageEntry("api1", "deepseek-flash", usage, nil))
	add("retained-directory-after-usage", usageEntry("api1", "deepseek-flash", usage, nil)+attachment(map[string]any{"type": "directory", "path": "$HOME/src", "content": "read.txt\nother.txt"}))
	add("retained-image-after-usage", usageEntry("api1", "deepseek-flash", usage, nil)+attachment(map[string]any{"type": "file", "filename": "$HOME/image.png", "content": map[string]any{"type": "image", "file": map[string]any{"filePath": "$HOME/image.png", "base64": "YWJj", "type": "image/png", "originalSize": 3}}}))
	add("retained-pdf-after-usage", usageEntry("api1", "deepseek-flash", usage, nil)+attachment(map[string]any{"type": "file", "filename": "$HOME/book.pdf", "content": map[string]any{"type": "pdf", "file": map[string]any{"filePath": "$HOME/book.pdf", "base64": "JVBERi0xLjQ=", "originalSize": 2048}}}))
	add("retained-extracted-pdf-parts-source-ignored", usageEntry("api1", "deepseek-flash", usage, nil)+attachment(map[string]any{"type": "file", "filename": "$HOME/book.pdf", "content": map[string]any{"type": "parts", "file": map[string]any{"filePath": "$HOME/book.pdf", "originalSize": 2048, "count": 2}}}))
	add("retained-plan-mode-full", usageEntry("api1", "deepseek-flash", usage, nil)+attachment(map[string]any{"type": "plan_mode", "reminderType": "full", "isSubAgent": false, "planFilePath": "$HOME/plan.md"}))
	add("retained-plan-mode-exit", usageEntry("api1", "deepseek-flash", usage, nil)+attachment(map[string]any{"type": "plan_mode_exit"}))
	add("mixed-models-durations", usageEntry("api1", "deepseek-flash", usage, map[string]any{"timestamp": "2026-10-07T00:00:00.000Z"})+usageEntry("api2", "claude-sonnet-4-6", usage, map[string]any{"timestamp": "2026-10-07T00:00:02.500Z"}))
	add("empty-workdir-falls-to-cwd", `{"type":"session-meta","workDir":"","cwd":"$HOME/actual","permissionMode":"plan","runtimeProviderId":"saved","runtimeModelId":"old","effortLevel":"low"}`+"\n"+`{"type":"session-meta","runtimeProviderId":null,"runtimeModelId":"next"}`+"\n")
	add("worktree-and-repository", `{"type":"session-meta","workDir":"$HOME","repository":{"repoRoot":"$HOME/repo"}}`+"\n"+`{"type":"worktree-state","worktreeSession":{"worktreePath":"$HOME/w","worktreeName":"native"}}`+"\n"+`{"type":"worktree-state","worktreeSession":null}`+"\n"+`{"type":"custom-title","customTitle":""}`+"\n")
	add("malformed-record-ignored", "{bad\n"+usageEntry("api1", "deepseek-flash", usage, nil))
	add("oversize-semantic-record", entry("assistant", "big", strings.Repeat("x", 9*1024*1024), nil))
	add("low-trust-media-usage", usageEntry("api1", "deepseek-flash", map[string]any{"input_tokens": 2000000, "output_tokens": 8}, nil)+entry("user", "image", []any{map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": "YWJj"}}}, nil))
	tooMany := ""
	for i := 0; i < 65; i++ {
		tooMany += usageEntry(fmt.Sprintf("m%d", i), fmt.Sprintf("model-%d", i), usage, nil)
	}
	add("model-count-budget", tooMany)
	add("model-name-budget", usageEntry("api1", strings.Repeat("x", 513), usage, nil))
	add("usage-key-budget", usageEntry(strings.Repeat("x", 4097), "deepseek-flash", usage, nil))
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	env := map[string]string{"ANTHROPIC_API_KEY": "fake-no-network", "ANTHROPIC_BASE_URL": "http://127.0.0.1:43210", "ANTHROPIC_MODEL": "claude-sonnet-4-6", "CLAUDE_CODE_MODEL_CONTEXT_WINDOWS": `{"deepseek-flash":1000000}`}
	if !direct {
		delete(env, "ANTHROPIC_API_KEY")
		delete(env, "ANTHROPIC_AUTH_TOKEN")
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-session-inspection.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SESSION_INSPECTION_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=test", "TZ=UTC", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"home": home, "cases": rows, "environment": env, "settings": map[string]any{}})
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Results []struct {
			Name     string
			Snapshot json.RawMessage
			Error    *struct {
				Status        int
				Code, Message string
			}
		}
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Results) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal("Invalid actual Source inspection", err, source.Capabilities.Blocked, string(output)[:min(len(output), 1200)])
	}
	o := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) {
		t.Fatal("Transcript inspection must not start a model")
		return nil, nil
	})
	o.HomeDir = home
	o.ConfigDir = filepath.Join(home, ".claude")
	o.WorkDir = home
	s, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	settings, global := protocol.NewJSONObject(), protocol.NewJSONObject()
	modelOptions := modelaccounting.NativeCalculatorOptions{Environment: env, Settings: func() (*protocol.JSONObject, error) { v := settings.Clone(); return &v, nil }, Global: func() (*protocol.JSONObject, error) { v := global.Clone(); return &v, nil }}
	for i, row := range rows {
		info := sessionInfo{ID: row["id"].(string), Path: filepath.Join(o.ConfigDir, "projects", "private", row["id"].(string)+".jsonl")}
		got, err := s.readInspectionTranscript(ctx, info, modelOptions)
		want := source.Results[i]
		if want.Error != nil {
			if actual, ok := err.(*apiError); !ok || actual.status != want.Error.Status || actual.code != want.Error.Code || actual.message != want.Error.Message {
				t.Errorf("%s Source%+v Go%v", row["name"], want.Error, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s Go error%v", row["name"], err)
			continue
		}
		var expected, actual any
		_ = json.Unmarshal(want.Snapshot, &expected)
		encoded := strings.ReplaceAll(string(recoveryJSON(got)), home, "$HOME")
		_ = json.Unmarshal([]byte(encoded), &actual)
		if !reflect.DeepEqual(expected, actual) {
			t.Errorf("%s Source%s Go%s", row["name"], want.Snapshot[:min(1700, len(want.Snapshot))], encoded[:min(1700, len(encoded))])
		}
	}
}
