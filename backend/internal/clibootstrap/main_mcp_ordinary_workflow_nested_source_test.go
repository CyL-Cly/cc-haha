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
	"sync/atomic"
	"testing"
)

func TestNativeMCPOrdinaryNestedWorkflowAndNamedAgentRejectionActualSource(t *testing.T) {
	home, env := ordinaryFixture(t)
	var calls atomic.Int32
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		calls.Add(1)
		emitPlanFixture(w, 1, "", "ACTUAL_NESTED_WORKER")
	}))
	defer peer.Close()
	cert := filepath.Join(home, "cert.pem")
	_ = os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw}), 0600)
	env["ANTHROPIC_BASE_URL"], env["NODE_EXTRA_CA_CERTS"] = peer.URL, cert
	nested := filepath.Join(home, "nested.js")
	_ = os.WriteFile(nested, []byte(`export const meta={name:'nested-child',description:'actual nested worker'};return await agent('Run the actual nested worker')`), 0600)
	script := `export const meta={name:'nested-parent',description:'actual nested ordinary producer',phases:[{title:'inspect'}]};phase('inspect');try {await agent('Must resolve actual empty named definitions',{agentType:'fixture-missing'})}catch(error){log(error.message)};return await workflow({scriptPath:'nested.js'},args)`
	source := startOrdinarySource(t, home, home, env)
	want := ordinaryData(t, source.call(t, "Workflow", map[string]any{"script": script, "args": map[string]any{"owned": true}}))
	source.close(t)
	sourceCount := calls.Load()
	journal, e := os.ReadFile(filepath.Join(want.String("transcriptDir"), "journal.jsonl"))
	if e != nil || !strings.Contains(string(journal), "ACTUAL_NESTED_WORKER") || sourceCount != 1 {
		t.Fatal("actual Source nested consumer", e, sourceCount, string(journal))
	}
	host, _ := ordinaryGoContext(t, home, home, env)
	got, e := host.Call(context.Background(), "Workflow", rawMain(map[string]any{"script": script, "args": map[string]any{"owned": true}}))
	if e != nil {
		t.Fatal(e)
	}
	launched := ordinaryData(t, got)
	if e = host.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	journal, e = os.ReadFile(filepath.Join(launched.String("transcriptDir"), "journal.jsonl"))
	if e != nil || !strings.Contains(string(journal), "ACTUAL_NESTED_WORKER") || calls.Load()-sourceCount != 1 {
		t.Fatal("Go actual nested consumer", e, calls.Load(), string(journal))
	}
}
