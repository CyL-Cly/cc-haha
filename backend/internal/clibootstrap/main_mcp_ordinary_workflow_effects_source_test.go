package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func ordinaryAwaitJournal(t *testing.T, path string) {
	t.Helper()
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, _ := os.ReadFile(path)
		if bytes.Contains(data, []byte(`"type":"result"`)) {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("actual owned journal producer did not finish", path, string(data))
		case <-ticker.C:
		}
	}
}
func TestNativeMCPOrdinaryWorkflowToolEffectsFreshCacheAndResumeActualSource(t *testing.T) {
	home, env := ordinaryFixture(t)
	cached := filepath.Join(home, "cached.txt")
	written := filepath.Join(home, "worker.txt")
	original := []byte("cache original\n")
	_ = os.WriteFile(cached, original, 0600)
	type step struct {
		name  string
		input map[string]any
	}
	steps := []step{{"Edit", map[string]any{"file_path": cached, "old_string": "original", "new_string": "edited"}}, {"Read", map[string]any{"file_path": cached}}, {"Edit", map[string]any{"file_path": cached, "old_string": "original", "new_string": "edited"}}, {"Write", map[string]any{"file_path": written, "content": "actual worker file\n"}}, {"Read", map[string]any{"file_path": written}}, {"TodoWrite", map[string]any{"todos": []any{map[string]any{"content": "worker", "status": "pending", "activeForm": "worker"}}}}, {"EnterPlanMode", map[string]any{}}, {"ExitPlanMode", map[string]any{}}, {"TaskStop", map[string]any{"task_id": "fixture-missing"}}}
	var mu sync.Mutex
	bodies := []protocol.JSONObject{}
	round := 0
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body protocol.JSONObject
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		bodies = append(bodies, body.Clone())
		round++
		i := round
		mu.Unlock()
		if i <= len(steps) {
			emitNativeTaskTool(w, fmt.Sprintf("ordinary-worker-tool-%d", i), steps[i-1].name, steps[i-1].input)
		} else {
			emitPlanFixture(w, i, "", "ACTUAL_MCP_WORKER_EFFECTS")
		}
	}))
	defer peer.Close()
	cert := filepath.Join(home, "cert.pem")
	_ = os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw}), 0600)
	env["ANTHROPIC_BASE_URL"], env["NODE_EXTRA_CA_CERTS"] = peer.URL, cert
	script := `export const meta={name:'ordinary-effects',description:'actual worker effects'};return await agent('Verify the worker starts with a fresh cache, write and read the private file, then report',{label:'ordinary worker'})`
	source := startOrdinarySource(t, home, home, env)
	source.call(t, "Read", map[string]any{"file_path": cached})
	launched := ordinaryData(t, source.call(t, "Workflow", map[string]any{"script": script}))
	ordinaryAwaitJournal(t, filepath.Join(launched.String("transcriptDir"), "journal.jsonl"))
	source.call(t, "Workflow", map[string]any{"scriptPath": launched.String("scriptPath"), "resumeFromRunId": launched.String("runId")})
	source.close(t)
	mu.Lock()
	sourceCount := round
	sourceBodies := append([]protocol.JSONObject{}, bodies...)
	round = 0
	bodies = nil
	mu.Unlock()

	if data, e := os.ReadFile(cached); e != nil || string(data) != "cache edited\n" {
		t.Fatal("Source fresh-cache Read→Edit effect", e, string(data))
	}
	sourceWritten, e := os.ReadFile(written)
	if e != nil {
		t.Fatal("Source Write side effect", e)
	}
	_ = os.WriteFile(cached, original, 0600)
	_ = os.Remove(written)
	host, _ := ordinaryGoContext(t, home, home, env)
	if _, e = host.Call(context.Background(), "Read", rawMain(map[string]any{"file_path": cached})); e != nil {
		t.Fatal(e)
	}
	result, e := host.Call(context.Background(), "Workflow", rawMain(map[string]any{"script": script}))
	if e != nil {
		t.Fatal(e)
	}
	goLaunched := ordinaryData(t, result)
	ordinaryAwaitJournal(t, filepath.Join(goLaunched.String("transcriptDir"), "journal.jsonl"))
	if _, e = host.Call(context.Background(), "Workflow", rawMain(map[string]any{"scriptPath": goLaunched.String("scriptPath"), "resumeFromRunId": goLaunched.String("runId")})); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if e = host.Close(ctx); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	goCount := round
	goBodies := append([]protocol.JSONObject{}, bodies...)
	mu.Unlock()
	if sourceCount != len(steps)+1 || goCount != sourceCount {
		t.Fatalf("real resume request counts Source=%d Go=%d", sourceCount, goCount)
	}
	if data, e := os.ReadFile(cached); e != nil || string(data) != "cache edited\n" {
		t.Fatal("Go fresh-cache Read→Edit effect", e, string(data))
	}
	if data, e := os.ReadFile(written); e != nil || !bytes.Equal(data, sourceWritten) {
		t.Fatal("Go physical Write", e, string(data))
	}
	results := func(body protocol.JSONObject) []any {
		var messages []protocol.JSONObject
		_ = json.Unmarshal(body.Get("messages"), &messages)
		out := []any{}
		for _, message := range messages {
			var blocks []protocol.JSONObject
			_ = json.Unmarshal(message.Get("content"), &blocks)
			for _, block := range blocks {
				if block.String("type") == "tool_result" {
					var value map[string]any
					_ = json.Unmarshal(rawMain(block), &value)
					delete(value, "tool_use_id")
					text, _ := value["content"].(string)
					var parsed map[string]any
					if strings.HasPrefix(text, "{") && json.Unmarshal([]byte(text), &parsed) == nil {
						delete(parsed, "durationMs")
						delete(parsed, "durationSeconds")
						value["content"] = parsed
					}
					out = append(out, value)
				}
			}
		}
		return out
	}
	for i := 1; i < len(goBodies); i++ {
		a, b := results(goBodies[i]), results(sourceBodies[i])
		if !reflect.DeepEqual(a, b) {
			t.Errorf("actual worker tool results at request%d Go=%v Source=%v", i+1, a, b)
		}
	}
}
