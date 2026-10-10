package clibootstrap

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualAgentBorrowsParentHelperCacheAndChildCloseKeepsItAlive(t *testing.T) {
	var calls atomic.Int32
	var count string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer shared-private-helper" {
			t.Error("Agent did not use original parent process helper")
		}
		if n := calls.Add(1); n == 1 {
			emitNativeTaskTool(w, "helper-agent", "Agent", map[string]any{"subagent_type": "general-purpose", "prompt": "Return CHILD_HELPER_OK without tools", "description": "Actual parent credential cache"})
		} else {
			emitPlanFixture(w, 1, "", "SHARED_HELPER_OK")
		}
	}))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		delete(n.Environment, "ANTHROPIC_API_KEY")
		delete(n.Environment, "ANTHROPIC_AUTH_TOKEN")
		delete(n.Environment, "CLAUDE_CODE_SIMPLE")
		count = filepath.Join(n.CWD, "shared-helper-count")
		command := "printf called\\n >> " + settingsShellQuote(count) + "; printf shared-private-helper"
		settings := map[string]any{"apiKeyHelper": command, "alwaysThinkingEnabled": false, "agentTeamsEnabled": false, "enabledPlugins": map[string]bool{"sample@mkt": false}}
		if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), rawMain(settings), 0600); err != nil {
			t.Fatal(err)
		}
	}, func(o *MainPrintOptions) { o.Arguments.Tools = []string{"Agent,Read"} })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	user := func(s string) protocol.JSONObject {
		return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": s})})
	}
	if err := m.RunTurn(ctx, user("Run actual child Agent with shared helper")); err != nil {
		t.Fatal(err)
	}
	if err := m.RunTurn(ctx, user("Parent cache remains after actual child closes")); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(count); err != nil || string(raw) != "calledn" {
		t.Fatal("helper executed separately in worker or parent cache closed", string(raw), err)
	}
	if calls.Load() != 4 {
		t.Fatal(fmt.Sprintf("actual Agent same Harness calls=%d", calls.Load()))
	}
}
