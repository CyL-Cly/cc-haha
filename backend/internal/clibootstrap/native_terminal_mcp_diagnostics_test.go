package clibootstrap

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestNativeTerminalMCPDiagnosticsReadsActualFilesWithoutConnectingOrWriting(t *testing.T) {
	f := newNativePluginFixture(t)
	var requests atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "diagnostic must not connect", http.StatusBadGateway)
	}))
	defer peer.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
	seedNativeReleaseWarmFixture(t, f, "fixture")
	f.write(t, filepath.Join(f.home, ".mcp.json"), `{"mcpServers":{"warning-peer":{"type":"http","url":"`+peer.URL+`/${CC_HAHA_WARNING_MISSING}"}}}`)
	f.write(t, filepath.Join(f.cwd, ".mcp.json"), `{broken`)
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", Tools: []string{}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Platform: runtime.GOOS, Version: "fixture", Interactive: true, Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	before := map[string]string{}
	for _, path := range []string{filepath.Join(f.home, ".mcp.json"), filepath.Join(f.cwd, ".mcp.json"), filepath.Join(f.config, "settings.json")} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		before[path] = string(data)
	}
	rows, err := m.TerminalMCPParsingDiagnostics(context.Background())
	if err != nil || len(rows) != 4 {
		t.Fatal(err, rows)
	}
	if rows[1].String("scope") != "project" || rows[1].String("path") != filepath.Join(f.n.CWD, ".mcp.json") || string(rows[1].Get("errors")) == "[]" || string(rows[1].Get("warnings")) == "[]" {
		t.Fatal("actual MainPrint diagnostics omitted malformed and missing env config", string(rawMain(rows)))
	}
	if m.mcpRuntime == nil {
		t.Fatal("actual MCP runtime missing")
	}
	if requests.Load() != 0 {
		t.Fatal("diagnostics connected to the actual MCP/model endpoint", requests.Load())
	}
	for path, want := range before {
		got, err := os.ReadFile(path)
		if err != nil || !reflect.DeepEqual(string(got), want) {
			t.Fatal("read-only diagnostics changed source file", path, err)
		}
	}
	m.options.Interactive = false
	if _, err := m.TerminalMCPParsingDiagnostics(context.Background()); err == nil {
		t.Fatal("headless profile borrowed terminal diagnostic consumer")
	}
	m.options.Interactive = true
	m.Close()
	if _, err := m.TerminalMCPParsingDiagnostics(context.Background()); err != cooperative.ErrClosed {
		t.Fatal("closed dialog process remained readable", err)
	}
	var missing *MainPrint
	if _, err := missing.TerminalMCPParsingDiagnostics(context.Background()); err != cooperative.ErrRestricted {
		t.Fatal("nil producer was accepted", err)
	}
}
