package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestDefaultPromptKeepsSourceCacheThenUsesCurrentModelAfterClear(t *testing.T) {
	f := worktreeMainFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"agentTeamsEnabled":false,"alwaysThinkingEnabled":false,"enabledPlugins":{"sample@mkt":false}}`)
	f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
	var mu sync.Mutex
	requests := []json.RawMessage{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, body)
		n := len(requests)
		mu.Unlock()
		emitWorktreeModel(w, n, "", "", "actual model response")
	}))
	defer peer.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, Arguments: PrintArguments{ComputerUse: &disabled, Model: "actual-before-model", Tools: []string{"Read"}, MaxTurns: 2, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if err = m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	user := func(text string) protocol.JSONObject {
		return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]string{"role": "user", "content": text})})
	}
	if err = m.RunTurn(ctx, user("first actual turn")); err != nil {
		t.Fatal(err)
	}
	if err = m.SetSessionModel(ctx, "actual-after-model"); err != nil {
		t.Fatal(err)
	}
	if err = m.RunTurn(ctx, user("second actual turn")); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	rows := append([]json.RawMessage{}, requests...)
	mu.Unlock()
	if len(rows) != 2 {
		t.Fatal(len(rows))
	}
	if !bytes.Contains(rows[0], []byte("You are powered by the model actual-before-model.")) {
		t.Fatal("initial actual model prompt missing")
	}
	// Source's env_info_simple section is cached across set_model. A clear,
	// compact or worktree transition invalidates it before the next producer.
	if !bytes.Contains(rows[1], []byte("You are powered by the model actual-before-model.")) {
		t.Fatal("set_model unexpectedly invalidated the Source section cache")
	}
	if err = m.Owner.Run(ctx, func(*cooperative.Scope) error { m.prompt.Clear(); return nil }); err != nil {
		t.Fatal(err)
	}
	if err = m.RunTurn(ctx, user("actual turn after original section cache clear")); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	third := append(json.RawMessage{}, requests[2]...)
	mu.Unlock()
	if !bytes.Contains(third, []byte("You are powered by the model actual-after-model.")) {
		t.Fatal("default producer still uses the startup model after cache clear")
	}
}
