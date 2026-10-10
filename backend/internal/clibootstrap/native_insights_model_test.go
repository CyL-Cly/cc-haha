package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
)

func TestActualInsightsAuxiliaryHTTPUsesCanonicalOwnerAndAccountingWithoutCanonicalMessages(t *testing.T) {
	f := worktreeMainFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"agentTeamsEnabled":false,"alwaysThinkingEnabled":false,"enabledPlugins":{"sample@mkt":false}}`)
	f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := int(calls.Add(1))
		if n == 1 {
			var request protocol.JSONObject
			if err := request.UnmarshalJSON(body); err != nil {
				t.Error(err)
			}
			var messages []protocol.JSONObject
			_ = json.Unmarshal(request.Get("messages"), &messages)
			if request.String("model") != "actual-insights-opus" || string(request.Get("max_tokens")) != "500" || len(messages) != 1 || !bytes.Contains(body, []byte("PRIVATE_INSIGHTS_AUX_921")) || bytes.Contains(body, []byte("cache_control")) || bytes.Contains(body, []byte(`"name":"Read"`)) {
				t.Errorf("original single-user/no-tools/no-cache Insights request missing: %s", body)
			}
			emitWorktreeModel(w, n, "", "", "PRIVATE_INSIGHTS_RESULT_921")
			return
		}
		if n != 2 || bytes.Contains(body, []byte("PRIVATE_INSIGHTS_AUX_921")) || bytes.Contains(body, []byte("PRIVATE_INSIGHTS_RESULT_921")) {
			t.Errorf("auxiliary leaked into canonical next ASK: %s", body)
		}
		emitWorktreeModel(w, n, "", "", "actual normal main reply")
	}))
	defer peer.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "Actual main Harness", Tools: []string{"Read"}, MaxTurns: 2, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if err = m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		text, e := m.Host.QueryInsights(ctx, scope, quoteMain("PRIVATE_INSIGHTS_AUX_921"), "actual-insights-opus", 500)
		if e != nil {
			return e
		}
		if string(text) != `"PRIVATE_INSIGHTS_RESULT_921"` {
			return fmt.Errorf("last actual assistant text %s", text)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	usage, err := m.ReadSessionUsage(ctx)
	if err != nil || string(usage.Get("totalInputTokens")) != "1" {
		t.Fatal(usage, err)
	}
	user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]string{"role": "user", "content": "normal next turn"})})
	if err = m.RunTurn(ctx, user); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal(calls.Load())
	}
	if err = m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(f.config, "projects", "*", "*.jsonl"))
	if len(files) != 1 {
		t.Fatal(files)
	}
	disk, err := os.ReadFile(files[0])
	if err != nil || bytes.Contains(disk, []byte("PRIVATE_INSIGHTS_")) {
		t.Fatal("auxiliary persistence", err, string(disk))
	}
}

func TestActualInsightsBatchStartsParallelHTTPAndKeepsInputOrderOnSameOwner(t *testing.T) {
	f := worktreeMainFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"agentTeamsEnabled":false,"alwaysThinkingEnabled":false,"enabledPlugins":{"sample@mkt":false}}`)
	var calls atomic.Int32
	allStarted := make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if calls.Add(1) == 3 {
			close(allStarted)
		}
		select {
		case <-allStarted:
		case <-r.Context().Done():
			return
		}
		var request protocol.JSONObject
		_ = request.UnmarshalJSON(body)
		var rows []protocol.JSONObject
		_ = json.Unmarshal(request.Get("messages"), &rows)
		var content []protocol.JSONObject
		_ = json.Unmarshal(rows[0].Get("content"), &content)
		emitWorktreeModel(w, 1, "", "", content[0].String("text"))
	}))
	defer peer.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", MaxTurns: 2, Thinking: requestcontext.Thinking{Type: "disabled"}, Tools: []string{"Read"}, Sources: DefaultState()}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err = m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		requests := []sdkhost.InsightsRequest{}
		for _, text := range []string{"first", "second", "third"} {
			requests = append(requests, sdkhost.InsightsRequest{Prompt: quoteMain(text), Model: "parallel-insights", MaxTokens: 500})
		}
		results, e := m.Host.QueryInsightsBatch(ctx, scope, requests)
		if e != nil {
			return e
		}
		for i, result := range results {
			if result.Err != nil {
				return result.Err
			}
			if string(result.Text) != string(requests[i].Prompt) {
				return fmt.Errorf("batch index%d: %s", i, result.Text)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatal(calls.Load())
	}
	usage, err := m.ReadSessionUsage(ctx)
	if err != nil || string(usage.Get("totalInputTokens")) != "3" {
		t.Fatal(usage, err)
	}
}
