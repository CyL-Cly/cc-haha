package analytics

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func sourceFixture(t *testing.T, name string, input any) []byte {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		var e error
		bun, e = exec.LookPath("bun")
		if e != nil {
			t.Skip("Source Bun unavailable")
		}
	}
	root, _ := filepath.Abs("../../..")
	allocation := t.TempDir()
	home := filepath.Join(allocation, "home")
	config := filepath.Join(home, ".claude")
	tmp := filepath.Join(home, "tmp")
	for _, p := range []string{home, config, tmp} {
		if e := os.MkdirAll(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	raw, _ := json.Marshal(input)
	cmd := exec.Command(bun, "--no-env-file", "run", filepath.Join(root, "scripts/quality-gate/go-parity", name))
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + tmp, "PATH=" + os.Getenv("PATH"), "BUN_OPTIONS=--no-env-file", "NODE_ENV=test"}
	cmd.Stdin = bytes.NewReader(raw)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, e := cmd.Output()
	if e != nil {
		t.Fatalf("Source %v: %s", e, stderr.String())
	}
	return output
}

func TestActualSourcePrivacyAndFileOperationMetadata(t *testing.T) {
	type profile struct {
		Env  map[string]string    `json:"env"`
		File *protocol.JSONObject `json:"file,omitempty"`
	}
	profiles := []profile{{Env: map[string]string{}}}
	for _, key := range []string{"NODE_ENV", "DISABLE_TELEMETRY", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_AZURE_OPENAI"} {
		for _, value := range []string{"", "0", "false", "true", "YES", "test", "\ufeffTRUE\ufeff", "\u0085TRUE\u0085"} {
			profiles = append(profiles, profile{Env: map[string]string{key: value}})
		}
	}
	for _, path := range []string{`"/private/project/a.ts"`, `"/private/project/中文🦊.md"`, `"/private/project/\ud800.md"`} {
		for _, content := range []json.RawMessage{nil, json.RawMessage(`""`), json.RawMessage(`"中文🦊\ud800"`), scalar(strings.Repeat("a", 100*1024)), scalar(strings.Repeat("a", 100*1024+1)), scalar(strings.Repeat("🦊", 50*1024))} {
			file := protocol.NewJSONObject(field("operation", "read"), field("tool", "FileReadTool"), protocol.JSONField{Name: "filePath", Value: json.RawMessage(path)}, protocol.JSONField{Name: "content", Value: content})
			profiles = append(profiles, profile{Env: map[string]string{}, File: &file})
		}
	}
	root, _ := filepath.Abs("../../..")
	raw := sourceFixture(t, "native-file-analytics-pure.fixture.ts", map[string]any{"root": root, "cases": profiles})
	var expected []json.RawMessage
	if e := json.Unmarshal(raw, &expected); e != nil {
		t.Fatal(e, string(raw))
	}
	for i, p := range profiles {
		events := []Event{}
		if p.File != nil {
			path, e := protocol.DecodeJavaScriptJSONString(p.File.Get("filePath"))
			if e != nil {
				t.Fatal(e)
			}
			input := FileOperation{Operation: p.File.String("operation"), Tool: p.File.String("tool"), Path: path}
			if p.File.Get("content") != nil {
				content, e := protocol.DecodeJavaScriptJSONString(p.File.Get("content"))
				if e != nil {
					t.Fatal(e)
				}
				input.Content = &content
			}
			event := FileOperationEvent(input)
			events = append(events, event)
		}
		got, _ := json.Marshal(map[string]any{"level": PrivacyLevel(p.Env), "disabled": Disabled(p.Env), "events": events})
		var a, b any
		_ = json.Unmarshal(got, &a)
		_ = json.Unmarshal(expected[i], &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("profile%d Go%s Source%s", i, got, expected[i])
		}
	}
}

func TestActualSourceFirstPartyRecordAndPrivilegedMetadata(t *testing.T) {
	type profile struct {
		Event     Event               `json:"event"`
		ID        string              `json:"id"`
		UserID    string              `json:"userID"`
		Timestamp int64               `json:"timestamp"`
		Core      protocol.JSONObject `json:"core"`
		User      protocol.JSONObject `json:"user"`
	}
	cases := []profile{}
	env := protocol.NewJSONObject(field("platform", "darwin"), field("platformRaw", "darwin"), field("arch", "arm64"), field("nodeVersion", "fixture-runtime"), field("terminal", nil), field("packageManagers", "bun,npm"), field("runtimes", "fixture-runtime"), field("isRunningWithBun", true), field("isCi", false), field("isClaubbit", false), field("isClaudeCodeRemote", false), field("isLocalAgentMode", false), field("isConductor", false), field("isGithubAction", false), field("isClaudeCodeAction", false), field("isClaudeAiAuth", false), field("version", "fixture-version"), field("buildTime", "fixture-build"), field("deploymentEnvironment", "fixture"))
	rawEnv, _ := env.MarshalJSON()
	core := protocol.NewJSONObject(field("model", "fixture-model"), field("sessionId", "private-session"), field("userType", "external"), field("isInteractive", "false"), field("clientType", "cli"), protocol.JSONField{Name: "envContext", Value: rawEnv})
	for _, metadata := range []protocol.JSONObject{protocol.NewJSONObject(), protocol.NewJSONObject(field("operation", "write"), field("filePathHash", "hash")), protocol.NewJSONObject(field("_PROTO_skill_name", "fixture-skill"), field("_PROTO_plugin_name", "fixture-plugin"), field("_PROTO_marketplace_name", "fixture-market"), field("_PROTO_future", 17), field("ordinary", true)), protocol.NewJSONObject(field("unicode", "中文🦊"), protocol.JSONField{Name: "lone", Value: json.RawMessage(`"\ud800"`)})} {
		cases = append(cases, profile{Event: Event{Name: "tengu_file_operation", Metadata: metadata}, ID: "private-event", UserID: "private-device", Timestamp: 1700000000123, Core: core.Clone(), User: protocol.NewJSONObject()})
	}
	rich := core.Clone()
	rich.Set("betas", scalar("beta-one"))
	rich.Set("rh", scalar("repo-hash"))
	rich.Set("kairosActive", scalar(true))
	rich.Set("agentId", scalar("private-agent"))
	rich.Set("parentSessionId", scalar("private-parent"))
	rich.Set("agentType", scalar("subagent"))
	rich.Set("processMetrics", json.RawMessage(`{"uptime":1,"future":9007199254740993,"heapUsed":7}`))
	rich.Set("isInteractive", scalar("true"))
	rich.Set("sweBenchRunId", scalar("fixture-run"))
	cases = append(cases, profile{Event: Event{Name: "tengu_session_file_read", Metadata: protocol.NewJSONObject(field("totalLines", 7))}, ID: "private-rich", UserID: "private-device", Timestamp: 1700000000123, Core: rich, User: protocol.NewJSONObject(field("accountUuid", "private-account"), field("organizationUuid", "private-org"), field("email", "private@example.invalid"), field("githubActionsMetadata", map[string]string{"actorId": "1", "repositoryId": "2", "repositoryOwnerId": "3"}))})
	expected := sourceFixture(t, "native-file-analytics-record.fixture.ts", map[string]any{"cases": cases})
	var want []json.RawMessage
	if e := json.Unmarshal(expected, &want); e != nil {
		t.Fatal(e, string(expected))
	}
	for i, row := range cases {
		got, e := Record(RecordInput{Event: row.Event, ID: row.ID, UserID: row.UserID, Timestamp: time.UnixMilli(row.Timestamp), Core: row.Core, User: row.User})
		if e != nil {
			t.Fatal(e)
		}
		raw, _ := got.MarshalJSON()
		var a, b any
		_ = json.Unmarshal(raw, &a)
		_ = json.Unmarshal(want[i], &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("record%d Go%s Source%s", i, raw, want[i])
		}
	}
}
