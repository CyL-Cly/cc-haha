package workflow

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
)

func TestActualSourceWorkflowTaskRowsAndTerminalNotification(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	object := func(raw string) protocol.JSONObject { return jsonObject([]byte(raw)) }
	params := object(`{"taskId":"w-actual","script":"return 1","scriptPath":"/private/supplied.js","args":{"nested":[1,"中文"]},"summary":"Original <&> summary","workflowName":"native","workflowRunId":"wf_abc123","title":"Actual title","ownerAgentId":"parent-owner","toolUseId":"tool<&>","phases":[{"title":"one"}],"defaultModel":"fixture-model"}`)
	first := []protocol.JSONObject{object(`{"type":"workflow_phase","index":1,"title":"one","kind":"meta"}`), object(`{"type":"workflow_agent","index":1,"label":"first","state":"progress","tokens":3,"toolCalls":1,"future":{"keep":true}}`), object(`{"type":"workflow_log","message":"first log"}`), object(`{"type":"workflow_agent","index":2,"label":"second","state":"done","tokens":2,"toolCalls":1}`)}
	second := []protocol.JSONObject{object(`{"type":"workflow_agent","index":1,"label":"first","state":"done","tokens":8,"toolCalls":3}`), object(`{"type":"workflow_phase","index":1,"title":"updated","kind":"script"}`)}
	large := []protocol.JSONObject{}
	for i := 0; i < 1100; i++ {
		large = append(large, object(`{"type":"workflow_log","message":"bounded log"}`))
	}
	batches := [][]protocol.JSONObject{first, {}, second, large}
	notices := []protocol.JSONObject{object(`{"summary":"Original <&> summary","status":"completed","result":{"ordered":true,"x":[1,2]},"failures":["caught failure"],"agentCount":2,"totalTokens":10,"totalToolCalls":4,"durationMs":123,"toolUseId":"tool<&>","transcriptDir":"/private/transcripts","scriptPath":"/private/supplied.js","workflowRunId":"wf_abc123","args":{"nested":[1,"中文"]}}`), object(`{"summary":"Original <&> summary","status":"failed","error":"actual failure","agentCount":0,"totalTokens":0,"totalToolCalls":0,"durationMs":12,"transcriptDir":"/private/transcripts","scriptPath":"/private/supplied.js","workflowRunId":"wf_abc123","args":null}`), object(`{"summary":"Original <&> summary","status":"killed","result":"verbatim result","agentCount":1,"totalTokens":3,"totalToolCalls":1,"durationMs":9}`)}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-workflow.fixture.ts")
	input, _ := json.Marshal(map[string]any{"homeDir": home, "tasks": []map[string]any{{"params": params, "batches": batches, "notifications": notices}}})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CLAUDE_CODE_TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_WORKFLOW_FIXTURE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if e := cmd.Run(); e != nil {
		t.Fatalf("Source task %v %.1600s %.1600s", e, stdout.String(), stderr.String())
	}
	var source struct {
		Tasks []struct {
			Snapshots          []json.RawMessage
			OutputPath, Resume string
			Notifications      []string
		}
		Capabilities struct{ Blocked []string }
	}
	if e := json.Unmarshal(stdout.Bytes(), &source); e != nil || len(source.Tasks) != 1 || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(e, len(source.Tasks), source.Capabilities.Blocked, stderr.String())
	}
	canonical := func(raw []byte) any {
		var value any
		if e := json.Unmarshal(raw, &value); e != nil {
			t.Fatal(e)
		}
		return value
	}
	expected := source.Tasks[0]
	record := TaskRecord(params, 1700000000000, expected.OutputPath)
	records := []protocol.JSONObject{record.Clone()}
	for _, batch := range batches {
		var e error
		record, e = ApplyProgressBatch(record, batch)
		if e != nil {
			t.Fatal(e)
		}
		records = append(records, record.Clone())
	}
	for i, record := range records {
		raw, _ := record.MarshalJSON()
		if !reflect.DeepEqual(canonical(raw), canonical(expected.Snapshots[i])) {
			t.Fatalf("Source task snapshot%d mismatch Go=%.2000s\nSource=%.2000s", i, raw, expected.Snapshots[i])
		}
	}
	if actual := ResumePrompt(record); actual != expected.Resume {
		t.Fatal("Source resume prompt changed", actual, expected.Resume)
	}
	for i, notice := range notices {
		notice.Set("taskId", params.Get("taskId"))
		actual, e := Notification(notice, expected.OutputPath)
		if e != nil || actual != expected.Notifications[i] {
			t.Fatalf("Source workflow notification%d changed %v\nGo=%s\nSource=%s", i, e, actual, expected.Notifications[i])
		}
	}
}
