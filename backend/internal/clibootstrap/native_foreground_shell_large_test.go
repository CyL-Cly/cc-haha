package clibootstrap

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestActualInteractiveBashPersistsLargeOutputAndModelUsesOriginalReference(t *testing.T) {
	var rounds atomic.Int32
	var saved string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := rounds.Add(1)
		if n == 1 {
			emitNativeTaskTool(w, "actual-large-bash", "Bash", map[string]any{"command": "head -c 40000 /dev/zero | tr '\\000' x; printf ACTUAL_LARGE_TAIL_ND21", "description": "Write actual large output"})
			return
		}
		var request struct{ Messages []protocol.JSONObject }
		if e := json.Unmarshal(body, &request); e != nil {
			t.Error(e)
		}
		for _, message := range request.Messages {
			for _, block := range taskBlocks(message.Get("content")) {
				if block.String("type") != "tool_result" {
					continue
				}
				content := block.String("content")
				if !strings.Contains(content, "<persisted-output>") {
					t.Errorf("model got truncated output without original reference %.1200s", content)
					continue
				}
				const marker = "Full output saved to: "
				_, tail, found := strings.Cut(content, marker)
				if found {
					saved = strings.Split(tail, "\n")[0]
				}
				if strings.Contains(content, "ACTUAL_LARGE_TAIL_ND21") {
					t.Error("tail unexpectedly in first preview")
				}
			}
		}
		emitPlanFixture(w, int(n), "", "ACTUAL_LARGE_REFERENCE_USED_ND21")
	}))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, nil, func(o *MainPrintOptions) { o.ForegroundShellProgress = true })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if e := m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "Run actual large Bash output"})})); e != nil {
		t.Fatal(e)
	}
	if rounds.Load() != 2 || saved == "" {
		t.Fatal(rounds.Load(), saved)
	}
	contents, e := os.ReadFile(saved)
	if e != nil || len(contents) != 40000+len("ACTUAL_LARGE_TAIL_ND21") || !strings.HasSuffix(string(contents), "ACTUAL_LARGE_TAIL_ND21") {
		t.Fatal("full output not physically persisted", len(contents), e)
	}
	var expected string
	if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		dir, e := m.Controller.ToolResultsDirectoryOnOwner(scope)
		expected = dir
		return e
	}); e != nil {
		t.Fatal(e)
	}
	if filepath.Dir(saved) != expected {
		t.Fatalf("wrong Source session tool-results %s/%s", saved, expected)
	}
	id := strings.TrimSuffix(filepath.Base(saved), ".txt")
	var temp string
	if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		var e error
		temp, e = m.nativeTaskOutputDirectory(scope)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	st, e := os.Stat(filepath.Join(temp, id+".output"))
	if e != nil {
		t.Fatal(e)
	}
	dest, e := os.Stat(saved)
	if e != nil || !os.SameFile(st, dest) {
		t.Fatal("Source hardlink-first persistence not used", e)
	}
}
