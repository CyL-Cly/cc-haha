package clibootstrap

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestActualNativeVertexReadCountRemapsCloudEndpointAndFiltersBetas(t *testing.T) {
	var calls atomic.Int32
	content := strings.Repeat("vertex private history", 1400)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/v1/projects/private-project/locations/private-region/publishers/anthropic/models/count-tokens:rawPredict" {
			t.Error("Vertex count remained an Anthropic path", r.URL.Path)
		}
		if strings.Contains(r.Header.Get("anthropic-beta"), "unallowed-count-beta") || strings.Contains(r.Header.Get("anthropic-beta"), "context-1m-") {
			t.Error("Vertex count sent disallowed betas")
		}
		var payload struct {
			Model, AnthropicVersion string
			Messages                []struct{ Content string }
		}
		var raw map[string]json.RawMessage
		_ = json.Unmarshal(body, &payload)
		_ = json.Unmarshal(body, &raw)
		var version string
		_ = json.Unmarshal(raw["anthropic_version"], &version)
		if payload.Model != "fixture-model" || version != "vertex-2023-10-16" || len(payload.Messages) != 1 || payload.Messages[0].Content != content {
			t.Error("Vertex count lost actual original Read message/model/version")
		}
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, `{"input_tokens":37}`)
	}))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		for key, value := range map[string]string{"CLAUDE_CODE_USE_VERTEX": "1", "CLAUDE_CODE_SKIP_VERTEX_AUTH": "1", "ANTHROPIC_VERTEX_BASE_URL": peer.URL + "/v1", "ANTHROPIC_VERTEX_PROJECT_ID": "private-project", "CLOUD_ML_REGION": "private-region", "ANTHROPIC_BETAS": "unallowed-count-beta,context-1m-2025-08-07"} {
			n.Environment[key] = value
		}
	}, func(o *MainPrintOptions) { o.Arguments.Tools = []string{"Read"} })
	path := filepath.Join(m.options.Native.CWD, "vertex-long-history.txt")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		_, err := m.nativeTools.Call(ctx, "Read", webObject(`{"file_path":"vertex-long-history.txt"}`), scope, false)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("Vertex Read quietly estimated without counting", calls.Load())
	}
	if err := m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
}
