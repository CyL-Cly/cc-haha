package clibootstrap

import (
	"context"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
)

func TestNativeMCPOrdinaryActualAPIKeyHelperAndModelTransportSource(t *testing.T) {
	home, env := ordinaryFixture(t)
	delete(env, "ANTHROPIC_API_KEY")
	marker := filepath.Join(home, "helper-runs.txt")
	helper := filepath.Join(home, "helper.sh")
	_ = os.WriteFile(helper, []byte("#!/bin/sh\nprintf invoked\\n >> '"+marker+"'\nprintf fixture-helper-key\n"), 0700)
	_ = os.WriteFile(filepath.Join(home, ".claude/settings.json"), rawMain(map[string]any{"apiKeyHelper": helper}), 0600)
	var requests atomic.Int32
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		requests.Add(1)
		if r.Header.Get("X-Api-Key") != "fixture-helper-key" && r.Header.Get("Authorization") != "Bearer fixture-helper-key" {
			t.Error("actual helper credential was not consumed")
		}
		emitPlanFixture(w, 1, "", "ACTUAL_HELPER_MODEL")
	}))
	defer peer.Close()
	cert := filepath.Join(home, "cert.pem")
	_ = os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw}), 0600)
	env["ANTHROPIC_BASE_URL"], env["NODE_EXTRA_CA_CERTS"] = peer.URL, cert
	source := startOrdinarySource(t, home, home, env)
	sourceResult := source.call(t, "WebSearch", map[string]any{"query": "actual helper producer"})
	source.close(t)
	sourceRuns, e := os.ReadFile(marker)
	if e != nil {
		t.Fatal("Source physical helper missing", e)
	}
	_ = os.Remove(marker)
	host, _ := ordinaryGoContext(t, home, home, env)
	goResult, e := host.Call(context.Background(), "WebSearch", rawMain(map[string]any{"query": "actual helper producer"}))
	if e != nil {
		t.Fatal(e)
	}
	if e = host.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	goRuns, e := os.ReadFile(marker)
	if e != nil || string(goRuns) != string(sourceRuns) {
		t.Fatal("actual helper execution count differs", e, len(goRuns), len(sourceRuns))
	}
	if !reflect.DeepEqual(mcpOrdinaryResult(t, goResult), mcpOrdinaryResult(t, sourceResult)) {
		t.Fatalf("actual helper Source model result differs: Source=%s Go=%s", rawMain(sourceResult), rawMain(goResult))
	}
	if requests.Load() != 2 {
		t.Fatal("actual helper Source/Go model requests", requests.Load())
	}
}
