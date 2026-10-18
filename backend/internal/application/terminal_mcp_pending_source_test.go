package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTerminalMCPActualSourcePendingPublicationBeforeInitializeAndReactiveMenu(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source required")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(home, ".claude"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(home, ".claude", ".claude.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		message := obj(body)
		if message.Get("id") == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any
		switch message.String("method") {
		case "initialize":
			deadline := time.NewTimer(6 * time.Second)
			defer deadline.Stop()
			for {
				if _, err := os.Stat(filepath.Join(home, "pending-menu-mounted")); err == nil {
					break
				}
				select {
				case <-time.After(5 * time.Millisecond):
				case <-r.Context().Done():
					return
				case <-deadline.C:
					http.Error(w, "actual Source pending menu never mounted", http.StatusRequestTimeout)
					return
				}
			}
			result = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "panel-peer", "version": "1"}}
		case "tools/list":
			result = map[string]any{"tools": []any{}}
		default:
			t.Error("unexpected Source request", message.String("method"))
			result = map[string]any{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": message.Get("id"), "result": result})
	}))
	defer peer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-mcp-pending.fixture.ts")
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_MCP_PENDING_SOURCE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "COLUMNS=100", "LINES=35"}
	cmd.Stdin = bytes.NewReader(raw(map[string]string{"home": home, "url": peer.URL}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual MCP pending/AppState/Settings %v %.7000s", err, output)
	}
	var source struct {
		PendingAtInitialize            bool
		Transitions                    [][]string
		PendingOutput, ConnectedOutput string
		Completions                    []any
		Caps                           struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || !source.PendingAtInitialize || len(source.Completions) != 0 || len(source.Caps.Blocked) != 0 || !strings.Contains(source.PendingOutput, "connecting") || !strings.Contains(source.ConnectedOutput, "connected") {
		t.Fatal("actual pending publication contract", err, string(output))
	}
	pending, connected := false, false
	for _, row := range source.Transitions {
		for _, status := range row {
			if status == "pending" {
				pending = true
			}
			if status == "connected" && pending {
				connected = true
			}
		}
	}
	if !connected {
		t.Fatal("original reactive publication order", source.Transitions)
	}
}
