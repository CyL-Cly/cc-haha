package application

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/charmbracelet/x/term"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestActualPublicCLILSPNineQueriesWriteDiagnosticsNextHumanAndPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("actual Unix PTY; ConPTY requires actual Windows host")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	binary, _ := os.Executable()
	home := t.TempDir()
	if err = os.WriteFile(filepath.Join(home, "owned-lsp-pty"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/terminal_lsp_pty.py", binary, home)
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=" + os.Getenv("PATH"), "TERM=xterm-256color", "CC_HAHA_LSP_PTY_HELPER=1"}
	capturePublicGoCoverage(t, cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal("actual public LSP CLI", err, string(out))
	}
	t.Log(string(out))
}
func TestNativeTerminalLSPPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_LSP_PTY_HELPER") != "1" {
		t.Skip("owned public LSP PTY helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "owned-lsp-pty")); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	var rawHover, rawDiagnostics, allNine atomic.Bool
	var file string
	operations := []string{"goToDefinition", "findReferences", "hover", "documentSymbol", "workspaceSymbol", "goToImplementation", "prepareCallHierarchy", "incomingCalls", "outgoingCalls"}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, _ := io.ReadAll(r.Body)
		n := int(requests.Add(1))
		if n == 1 {
			hasLSP := false
			for _, tool := range array(obj(body).Get("tools")) {
				hasLSP = hasLSP || tool.String("name") == "LSP"
			}
			if !hasLSP {
				t.Error("actual interactive model did not receive LSP schema", string(body))
			}
		}
		if n > 1 && n <= 12 {
			found := false
			for _, m := range array(obj(body).Get("messages")) {
				for _, block := range array(m.Get("content")) {
					if block.String("type") == "tool_result" && block.String("tool_use_id") == fmt.Sprintf("call-lsp-public-%d", n-1) {
						found = true
						if booleanField(block, "is_error") {
							t.Error("actual LSP/Write returned tool error", n, string(raw(block)))
						}
					}
				}
			}
			if !found {
				t.Error("actual preceding tool result absent", n)
			}
		}
		if bytes.Contains(body, []byte("PUBLIC_LSP_HOVER_RAW")) && bytes.Contains(body, []byte(`\ud800`)) {
			rawHover.Store(true)
		}
		if bytes.Contains(body, []byte("<new-diagnostics>")) && bytes.Contains(body, []byte("PUBLIC_LSP_DIAGNOSTIC_RAW")) && bytes.Contains(body, []byte(`\udfff`)) {
			rawDiagnostics.Store(true)
		}
		tool := "LSP"
		var input any
		operationIndex := -1
		switch {
		case n == 1:
			operationIndex = 0
		case n == 2:
			tool = "Read"
			input = map[string]any{"file_path": file}
		case n == 3:
			tool = "Write"
			input = map[string]any{"file_path": file, "content": "PUBLIC_LSP_ACTUAL_WRITTEN_FILE\n"}
		case n >= 4 && n <= 11:
			operationIndex = n - 3
		case n == 12:
			allNine.Store(true)
			response(w, "lsp-public-done", "", nil, "PUBLIC_LSP_NINE_OPERATIONS_DONE_731")
			return
		case n == 13:
			if !bytes.Contains(body, []byte("PUBLIC_LSP_NEXT_HUMAN_731")) {
				t.Error("next human absent")
			}
			response(w, "lsp-public-next", "", nil, "PUBLIC_LSP_NEXT_HUMAN_DONE_731")
			return
		default:
			t.Error("unexpected physical model request", n)
			http.Error(w, "unexpected", 400)
			return
		}
		if operationIndex >= 0 {
			input = map[string]any{"operation": operations[operationIndex], "filePath": file, "line": 1, "character": 1}
		}
		response(w, fmt.Sprintf("lsp-public-%d", n), tool, input, "")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Arguments.Tools = []string{"Read,Write,Bash,LSP"}
	o.Arguments.MaxTurns = 20
	o.Stderr = os.Stderr
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		env["ENABLE_LSP_TOOL"] = "1"
		env["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] = "1"
		env["USER_TYPE"] = "external"
	}
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	plugin := prepareTerminalPluginFixture(t, o)
	if err := os.WriteFile(filepath.Join(plugin, ".claude-plugin", "plugin.json"), []byte(`{"name":"sample","version":"1.0.0"}`), 0600); err != nil {
		t.Fatal(err)
	}
	file = filepath.Join(o.WorkDir, "actual-lsp.txt")
	if err := os.WriteFile(file, []byte("PRIVATE_ORIGINAL_LSP_FILE\n"), 0600); err != nil {
		t.Fatal(err)
	}
	server := filepath.Join(o.Native.Home, "bin", "private-lsp-peer")
	if err := os.MkdirAll(filepath.Dir(server), 0700); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile("../../testdata/lsp-file-notify-peer.py")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(server, script, 0700); err != nil {
		t.Fatal(err)
	}
	framesPath := filepath.Join(home, "lsp-stdio-frames.jsonl")
	uri := "file://" + filepath.ToSlash(file)
	rangeValue := `{"start":{"line":0,"character":0},"end":{"line":0,"character":4}}`
	loc := `{"uri":` + string(raw(uri)) + `,"range":` + rangeValue + `}`
	item := `{"name":"publicFunction","kind":12,"uri":` + string(raw(uri)) + `,"range":` + rangeValue + `,"selectionRange":` + rangeValue + `}`
	results := `{"textDocument/definition":` + loc + `,"textDocument/references":[` + loc + `],"textDocument/hover":{"contents":"PUBLIC_LSP_HOVER_RAW\ud800"},"textDocument/documentSymbol":[{"name":"publicFunction","kind":12,"range":` + rangeValue + `}],"workspace/symbol":[{"name":"publicFunction","kind":12,"location":` + loc + `}],"textDocument/implementation":` + loc + `,"textDocument/prepareCallHierarchy":[` + item + `],"callHierarchy/incomingCalls":[{"from":` + item + `,"fromRanges":[` + rangeValue + `]}],"callHierarchy/outgoingCalls":[{"to":` + item + `,"fromRanges":[` + rangeValue + `]}]}`
	diagnostics := `[{"range":` + rangeValue + `,"severity":1,"source":"private-real-peer","code":731,"message":"PUBLIC_LSP_DIAGNOSTIC_RAW\udfff"}]`
	declaration := map[string]any{"fixture": map[string]any{"command": server, "args": []string{framesPath, home}, "workspaceFolder": o.WorkDir, "extensionToLanguage": map[string]string{".txt": "plaintext"}, "env": map[string]string{"CC_HAHA_LSP_RESULTS": results, "CC_HAHA_LSP_DIAGNOSTIC": diagnostics}}}
	if err = os.WriteFile(filepath.Join(plugin, ".lsp.json"), raw(declaration), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"enabledPlugins":{"sample@mkt":true},"futureUser":{"preserve":731}}`), 0600); err != nil {
		t.Fatal(err)
	}
	installed, err := pluginregistry.NewInstalledRegistry(pluginregistry.InstalledOptions{PluginsDir: filepath.Join(config, "plugins"), Store: storage.New(storage.Options{})})
	if err != nil {
		t.Fatal(err)
	}
	if err = installed.Add(context.Background(), "sample@mkt", "user", plugin, obj([]byte(`{"name":"sample","version":"1.0.0"}`)), nil); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(config, ".claude.json"), raw(map[string]any{"hasCompletedOnboarding": true, "projects": map[string]any{o.WorkDir: map[string]any{"hasTrustDialogAccepted": true}}}), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	environment := []string{}
	for k, v := range o.Environment {
		environment = append(environment, k+"="+v)
	}
	environment = append(environment, "TERM=xterm-256color")
	code, err := RunCommand(ctx, Command{Arguments: o.Arguments}, o, os.Stdin, os.Stdout, environment)
	if err != nil || code != 0 {
		t.Fatal(code, err)
	}
	if _, err = nativeTUIPresentationTerminalProbe(os.Stdin.Fd()); err != nil {
		t.Fatal(err)
	}
	after, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	fileData, _ := os.ReadFile(file)
	frameData, _ := os.ReadFile(framesPath)
	methods := map[string]int{}
	peerPID := 0
	for _, line := range strings.Split(string(frameData), "\n") {
		if line != "" {
			row := obj([]byte(line))
			methods[row.String("method")]++
			if row.String("method") == "initialize" {
				_ = json.Unmarshal(row.Get("pid"), &peerPID)
			}
		}
	}
	allFrames := true
	for _, method := range []string{"textDocument/definition", "textDocument/references", "textDocument/hover", "textDocument/documentSymbol", "workspace/symbol", "textDocument/implementation", "textDocument/prepareCallHierarchy", "callHierarchy/incomingCalls", "callHierarchy/outgoingCalls", "textDocument/didChange", "textDocument/didSave", "shutdown"} {
		allFrames = allFrames && methods[method] > 0
	}
	receipt := map[string]any{"terminalRestored": reflect.DeepEqual(before, after), "requests": requests.Load(), "rawHover": rawHover.Load(), "rawDiagnosticReachedModel": rawDiagnostics.Load(), "actualNineQueries": allNine.Load() && allFrames, "actualWrite": string(fileData) == "PUBLIC_LSP_ACTUAL_WRITTEN_FILE\n", "oneInitialize": methods["initialize"] == 1, "exitCode": code, "methods": methods, "actualPeerReaped": peerPID > 0 && publicPTYProcessSignalZero(peerPID) == syscall.ESRCH}
	if err = os.WriteFile(filepath.Join(home, "lsp-pty-receipt.json"), raw(receipt), 0600); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err = os.Stat(filepath.Join(home, "lsp-pty-release")); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("physical parent did not acknowledge terminal")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if requests.Load() != 13 || !allFrames || !rawHover.Load() || !rawDiagnostics.Load() || !reflect.DeepEqual(before, after) {
		t.Fatal(string(raw(receipt)), string(frameData))
	}
}
func booleanField(o protocol.JSONObject, key string) bool { return string(o.Get(key)) == "true" }
