package clibootstrap

import (
	"context"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNativeMCPOrdinaryWorkflowPhysicalScriptJournalAndRealAgentActualSource(t *testing.T) {
	home, env := ordinaryFixture(t)
	var mu sync.Mutex
	requests := 0
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		mu.Lock()
		requests++
		mu.Unlock()
		emitPlanFixture(w, 1, "", "ACTUAL_ORDINARY_WORKFLOW_AGENT")
	}))
	defer peer.Close()
	cert := filepath.Join(home, "fixture-cert.pem")
	_ = os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw}), 0600)
	env["ANTHROPIC_BASE_URL"] = peer.URL
	env["NODE_EXTRA_CA_CERTS"] = cert
	script := `export const meta={name:'ordinary-workflow',description:'actual ordinary producer',phases:[{title:'inspect'}]};phase('inspect');log('actual physical workflow');return await agent('Return actual answer')`
	source := startOrdinarySource(t, home, home, env)
	want := ordinaryData(t, source.call(t, "Workflow", map[string]any{"script": script}))
	source.close(t)
	mu.Lock()
	sourceRequests := requests
	mu.Unlock()
	t.Logf("actual Source Workflow requests=%d task=%s", sourceRequests, want.String("taskId"))
	sourceJournal, e := os.ReadFile(filepath.Join(want.String("transcriptDir"), "journal.jsonl"))
	if e != nil {
		t.Fatal("Source actual journal", e)
	}
	t.Logf("actual Source journal %s", sourceJournal)
	sourceScript, e := os.ReadFile(want.String("scriptPath"))
	if e != nil || string(sourceScript) != script {
		t.Fatal("Source physical script", e)
	}
	host, _ := ordinaryGoContext(t, home, home, env)
	got, e := host.Call(context.Background(), "Workflow", rawMain(map[string]any{"script": script}))
	if e != nil {
		t.Fatal(e)
	}
	data := ordinaryData(t, got)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if e = host.Close(ctx); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	goRequests := requests - sourceRequests
	mu.Unlock()
	journal, e := os.ReadFile(filepath.Join(data.String("transcriptDir"), "journal.jsonl"))
	if e != nil {
		t.Fatal("Go physical journal missing", e)
	}
	if sourceRequests != 1 || goRequests != 1 || !strings.Contains(string(journal), "ACTUAL_ORDINARY_WORKFLOW_AGENT") || !strings.Contains(string(sourceJournal), "ACTUAL_ORDINARY_WORKFLOW_AGENT") {
		t.Fatalf("actual consumer mismatch requests Source=%d Go=%d journal=%s", sourceRequests, goRequests, journal)
	}
	b, e := os.ReadFile(data.String("scriptPath"))
	if e != nil || string(b) != script {
		t.Fatal("Go physical script", e)
	}
	for _, field := range []string{"status", "taskType", "workflowName", "summary"} {
		if string(data.Get(field)) != string(want.Get(field)) {
			t.Errorf("actual Source field %s Go=%s Source=%s", field, data.Get(field), want.Get(field))
		}
	}
	// No task registry is available to an ordinary MCP request even though the
	// physical job and agent have executed. Restart must not adopt this old job.
	restarted, _ := ordinaryGoContext(t, home, home, env)
	stopped, e := restarted.Call(context.Background(), "TaskStop", rawMain(map[string]any{"task_id": data.String("taskId")}))
	if e != nil || string(stopped.Get("isError")) != "true" {
		t.Fatal("restart adopted old job", e)
	}
}
