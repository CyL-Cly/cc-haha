package clibootstrap

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNativeMCPOrdinaryActualSubscriberModelIdentitySource(t *testing.T) {
	home, env := ordinaryFixture(t)
	delete(env, "ANTHROPIC_API_KEY")
	env["CC_HAHA_MCP_FAKE_SECURE_JSON"] = string(rawMain(map[string]any{"claudeAiOauth": map[string]any{"accessToken": "fixture-subscriber", "refreshToken": "fixture-refresh", "expiresAt": time.Now().Add(time.Hour).UnixMilli(), "scopes": []string{"user:inference", "user:profile"}, "subscriptionType": "max", "rateLimitTier": "default_claude_max_5x"}}))
	var mu sync.Mutex
	models := []string{}
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-subscriber" {
			t.Error("actual owned subscriber token was not consumed by transport")
		}
		var body protocol.JSONObject
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		models = append(models, body.String("model"))
		mu.Unlock()
		emitPlanFixture(w, 1, "", "ACTUAL_SUBSCRIBER_IDENTITY")
	}))
	defer peer.Close()
	cert := filepath.Join(home, "cert.pem")
	_ = os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw}), 0600)
	env["ANTHROPIC_BASE_URL"], env["NODE_EXTRA_CA_CERTS"] = peer.URL, cert
	source := startOrdinarySource(t, home, home, env)
	want := source.call(t, "WebSearch", map[string]any{"query": "actual subscriber identity"})
	source.close(t)
	host, _ := ordinaryGoContext(t, home, home, env)
	got, e := host.Call(context.Background(), "WebSearch", rawMain(map[string]any{"query": "actual subscriber identity"}))
	if e != nil {
		t.Fatal(e)
	}
	if e = host.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	script := `export const meta={name:'subscriber',description:'actual cached identity worker'};return await agent('Return the actual subscriber worker answer')`
	sourceWorker := startOrdinarySource(t, home, home, env)
	launched := ordinaryData(t, sourceWorker.call(t, "Workflow", map[string]any{"script": script}))
	sourceWorker.close(t)
	worker, _ := ordinaryGoContext(t, home, home, env)
	result, e := worker.Call(context.Background(), "Workflow", rawMain(map[string]any{"script": script}))
	if e != nil {
		t.Fatal(e)
	}
	goLaunched := ordinaryData(t, result)
	if e = worker.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	for _, dir := range []string{launched.String("transcriptDir"), goLaunched.String("transcriptDir")} {
		raw, e := os.ReadFile(filepath.Join(dir, "journal.jsonl"))
		if e != nil || !strings.Contains(string(raw), "ACTUAL_SUBSCRIBER_IDENTITY") {
			t.Fatal("actual subscriber worker journal", e, string(raw))
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(models) != 4 || models[0] != models[1] || models[2] != models[3] {
		t.Fatalf("actual cached subscriber main model Source/Go: %v; Source=%s Go=%s", models, rawMain(want), rawMain(got))
	}
	if string(want.Get("isError")) == "true" || string(got.Get("isError")) == "true" {
		t.Fatalf("actual subscriber transport Source=%s Go=%s", rawMain(want), rawMain(got))
	}
}
