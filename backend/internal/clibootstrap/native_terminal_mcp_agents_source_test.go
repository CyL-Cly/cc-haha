package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestTerminalAgentMCPActualWholeSettingsBrowserPreauthCancelAndDeferredConnection(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned whole original Source required")
	}
	x := newTerminalMCPAuthFixture(t, false)
	home := filepath.Join(x.f.home, "source-agent-ui")
	for _, path := range []string{home, filepath.Join(home, ".claude"), filepath.Join(home, "tmp")} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	browser := filepath.Join(home, "private-browser")
	if err := os.WriteFile(browser, []byte("#!/bin/sh\nprintf '%s' \"$1\" > \"$AUTH_BROWSER_RECEIPT\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-mcp-agent-ui.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + x.f.home, "BUN_OPTIONS=--no-env-file", "CC_HAHA_AGENT_MCP_UI_SOURCE=1", "BROWSER=" + browser, "AUTH_BROWSER_RECEIPT=" + filepath.Join(home, "browser-url"), "MCP_OAUTH_CALLBACK_PORT=" + strconv.Itoa(x.port), "NODE_ENV=production", "USER=owned-source-user", "PATH=" + home, "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "COLUMNS=150", "LINES=42"}
	agents := []any{map[string]any{"agentType": "auth-worker", "whenToUse": "Private original agent", "mcpServers": []any{map[string]any{"agentauth": map[string]string{"type": "http", "url": x.peer.URL + "/mcp"}}}}}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"home": home, "url": x.peer.URL + "/mcp", "port": x.port, "agents": agents}))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var address []byte
	for len(address) == 0 {
		address, _ = os.ReadFile(filepath.Join(home, "callback-ready"))
		if len(address) > 0 {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("original agent UI failed before browser callback: %v\n%s\n%s", err, stdout.Bytes(), stderr.Bytes())
		case <-ctx.Done():
			t.Fatal("original agent UI callback deadline", ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	query, err := url.Parse(string(address))
	if err != nil {
		t.Fatal(err)
	}
	redirect, err := url.Parse(query.Query().Get("redirect_uri"))
	if err != nil || redirect.Hostname() != "localhost" || redirect.Port() != strconv.Itoa(x.port) {
		t.Fatal("unowned callback URL", redirect, err)
	}
	callback := redirect.String() + "?code=private-auth-code&state=" + url.QueryEscape(query.Query().Get("state"))
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, callback, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatal("actual browser redirect rejected", response.Status)
	}
	if err = <-done; err != nil {
		t.Fatalf("actual original agent UI %v\n%s\n%s", err, stdout.Bytes(), stderr.Bytes())
	}
	var source struct {
		Frames []struct {
			Stage       string
			Output      string
			Completions []string
			Clients     []string
		}
		ListenerClosed bool
		Blocked        []string
	}
	if err = json.Unmarshal(stdout.Bytes(), &source); err != nil {
		t.Fatal(err, stdout.String(), stderr.String())
	}
	if len(source.Frames) != 4 || source.Frames[3].Stage != "complete" || len(source.Frames[3].Completions) != 1 || source.Frames[3].Completions[0] != "Authentication successful for agentauth. The server will connect when the agent runs." || len(source.Frames[3].Clients) != 0 || len(source.Blocked) != 0 || !source.ListenerClosed || x.tokens.Load() != 1 || x.connects.Load() != 0 {
		t.Fatal(stdout.String(), x.tokens.Load(), x.connects.Load())
	}
	t.Log(stdout.String())
}
