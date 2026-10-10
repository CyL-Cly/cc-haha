package teamplan

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
)

func fixturePlan() json.RawMessage {
	return []byte(`{"schemaVersion":1,"planId":"plan","sessionId":"session","teamName":"team","incarnationId":"incarnation","revision":1,"state":"review_pending","workDir":"/private","leaderRuntime":{"providerId":"source-provider","modelId":"actual-model","future":true},"agentCatalog":{"worker":{"systemPrompt":"source prompt","hooks":null,"mcpServers":["named"],"sourceIdentity":{"kind":"builtin","keep":true},"future":{"keep":true}}},"members":[{"id":"member","name":"研究员_1","agentType":"worker","prompt":"complete task","runtime":{"providerId":"source-provider","modelId":"actual-model"},"runtimeSource":"human"}],"tasks":[{"id":"task","subject":"exact","ownerId":"member","dependencies":[],"future":true}],"createdAt":1000,"updatedAt":1001,"future":{"keep":true}}`)
}
func TestTeamPlanSchemaGraphAndHTTPInputsMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	cases := []map[string]any{{"input": fixturePlan(), "runnable": true}}
	for _, patch := range []json.RawMessage{[]byte(`{"revision":0}`), []byte(`{"revision":1.5}`), []byte(`{"revision":9007199254740992}`), []byte(`{"schemaVersion":2}`), []byte(`{"members":[]}`), []byte(`{"tasks":[]}`), []byte(`{"state":"unknown"}`), []byte(`{"leaderRuntime":{"providerId":"","modelId":"x"}}`), []byte(`{"agentCatalog":{"worker":{"systemPrompt":"ok","maxTurns":"3"}}}`), []byte(`{"approvedSnapshot":{"revision":1,"members":[],"tasks":[],"leaderRuntime":{"providerId":"x","modelId":"x"},"approvedAt":1,"requestId":"r","future":true}}`), []byte(`{"launch":{"status":"pending","memberIds":{"member":"session"},"executionStarted":true}}`), []byte(`{"agentCatalog":{"worker":{"systemPrompt":"ok","sourceIdentity":{"kind":"file","path":"/private","sha256":"bad"}}}}`), []byte(`{"agentCatalog":{"worker":{"systemPrompt":"ok","sourceIdentity":{"kind":"session","future":true}}}}`), []byte(`{"members":[{"id":"member","name":"team-lead","agentType":"worker","prompt":"task","runtime":{"providerId":"p","modelId":"m"}}]}`), []byte(`{"tasks":[{"id":"task","subject":"s","ownerId":"member","dependencies":["task"]}]}`), []byte(`{"tasks":[{"id":"task","subject":"s","ownerId":"unknown","dependencies":[]}]}`), []byte(`{"tasks":[{"id":"task","subject":"s","ownerId":"member","dependencies":["missing"]}]}`)} {
		v := object(fixturePlan())
		v.Assign(object(patch))
		cases = append(cases, map[string]any{"input": v, "runnable": true})
	}
	action := []byte(`{"sessionId":"s","planId":"p","incarnationId":"i","expectedRevision":1,"requestId":"r","feedback":"user","unknown":true}`)
	cases = append(cases, map[string]any{"kind": "action", "input": json.RawMessage(action)})
	for _, patch := range []json.RawMessage{[]byte(`{"expectedRevision":0}`), []byte(`{"feedback":null}`), []byte(`{"requestId":""}`)} {
		v := object(action)
		v.Assign(object(patch))
		cases = append(cases, map[string]any{"kind": "action", "input": v})
	}
	patchRequest := []byte(`{"sessionId":"s","planId":"p","incarnationId":"i","expectedRevision":1,"members":[{"id":"m","runtime":{"providerId":"p","modelId":"m","unknown":true}}],"tasks":[{"id":"t","ownerId":"m"}]}`)
	cases = append(cases, map[string]any{"kind": "patch", "input": json.RawMessage(patchRequest)})
	for _, patch := range []json.RawMessage{[]byte(`{"unknown":true}`), []byte(`{"members":[{"id":"m","unknown":true}]}`), []byte(`{"tasks":[{"id":"t","ownerId":""}]}`)} {
		v := object(patchRequest)
		v.Assign(object(patch))
		cases = append(cases, map[string]any{"kind": "patch", "input": v})
	}
	home := t.TempDir()
	for _, dir := range []string{".claude", "tmp"} {
		_ = os.MkdirAll(filepath.Join(home, dir), 0700)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-plan-schema.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_PLAN_SCHEMA_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": home, "cases": cases}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var source struct {
		Rows         []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Rows) != len(cases) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(e, string(out))
	}
	for i, row := range cases {
		data := raw(row["input"])
		parse := ParseRecord
		kind, _ := row["kind"].(string)
		if kind == "action" {
			parse = ParseAction
		}
		if kind == "patch" {
			parse = ParsePatchRequest
		}
		value, e := parse(data)
		var result, graph any
		if e == nil {
			result = value
			if kind == "" {
				if e = ValidateGraph(value, row["runnable"] == true); e != nil {
					graph = e.Error()
				}
			}
		}
		actual := raw(map[string]any{"valid": result != nil, "data": result, "graph": graph})
		var a, b any
		_ = json.Unmarshal(actual, &a)
		_ = json.Unmarshal(source.Rows[i], &b)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("%d %s\n%s", i, actual, source.Rows[i])
		}
	}
}
