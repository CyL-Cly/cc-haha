package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliinstallation"
)

func TestActualGoPublicInstallationAndUpdate(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	binary := filepath.Join(root, "actual-Go-install-cli")
	build := exec.CommandContext(ctx, "go", "build", "-p=4", "-tags=native_process_fixture", "-ldflags", "-X main.version=1.2.3", "-o", binary, ".")
	if data, err := build.CombinedOutput(); err != nil {
		t.Fatal(err, string(data))
	}
	content, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	name, err := cliinstallation.BinaryName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(content)
	var mu sync.Mutex
	paths := []string{}
	corrupt, nonGo := false, false
	var peer *httptest.Server
	peer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.RequestURI())
		badChecksum, foreign := corrupt, nonGo
		mu.Unlock()
		payload, digest := content, hash
		if foreign {
			payload = []byte("#!/bin/sh\nprintf 'legacy TypeScript launcher'\n")
			digest = sha256.Sum256(payload)
		}
		if badChecksum {
			digest[0] ^= 255
		}
		assets := []cliinstallation.Asset{{Name: name, URL: peer.URL + "/binary", Size: int64(len(payload))}, {Name: cliinstallation.ManifestName, URL: peer.URL + "/manifest", Size: 300}}
		switch r.URL.Path {
		case "/releases":
			_ = json.NewEncoder(w).Encode([]cliinstallation.Release{{Tag: "v1.2.5", Draft: true, Assets: assets}, {Tag: "v1.2.4", Prerelease: true, Assets: assets}, {Tag: "v1.2.3", Assets: assets}})
		case "/releases/tags/v1.2.3":
			_ = json.NewEncoder(w).Encode(cliinstallation.Release{Tag: "v1.2.3", Assets: assets})
		case "/manifest":
			_ = json.NewEncoder(w).Encode(cliinstallation.Manifest{Version: "1.2.3", Runtime: "go", Assets: map[string]cliinstallation.ManifestAsset{name: {SHA256: hex.EncodeToString(digest[:]), Size: int64(len(payload))}}})
		case "/binary":
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer peer.Close()
	home := filepath.Join(root, "home")
	for _, directory := range []string{home, filepath.Join(home, ".claude"), filepath.Join(home, "tmp"), filepath.Join(home, "private-bin"), filepath.Join(home, ".claude/local")} {
		if err = os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	npmCalls := filepath.Join(home, "npm-cleanup-calls")
	if err = os.WriteFile(filepath.Join(home, "private-bin/npm"), []byte("#!/bin/sh\nif [ \"$1\" = uninstall ]; then printf '%s\\n' \"$*\" >> \"$HOME/npm-cleanup-calls\"; exit 0; fi\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	legacyTarget := filepath.Join(home, ".claude/local/claude")
	if err = os.WriteFile(legacyTarget, []byte("legacy TypeScript launcher"), 0755); err != nil {
		t.Fatal(err)
	}
	rcPath := filepath.Join(home, ".bashrc")
	customRC := "# user configuration\nalias claude='~/my-custom/claude'\n"
	if err = os.WriteFile(rcPath, []byte("alias claude='"+legacyTarget+"'\n"+customRC), 0640); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(home, ".claude/.claude.json")
	if err = os.WriteFile(config, []byte(`{"futureInstall":{"retained":true},"hasCompletedOnboarding":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(home, ".claude/settings.json")
	if err = os.WriteFile(settingsPath, []byte(`{"futureSetting":{"retained":true},"autoMemoryEnabled":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	run := func(path string, args ...string) (int, string, string) {
		command := exec.CommandContext(ctx, path, args...)
		command.Dir = home
		command.Env = append(resumeErrorEnv(home, root, "http://127.0.0.1:9"), "CC_HAHA_CLI_RELEASE_API="+peer.URL, "PATH="+filepath.Join(home, ".local/bin")+string(os.PathListSeparator)+filepath.Join(home, "private-bin")+string(os.PathListSeparator)+"/usr/bin:/bin")
		code, stdout, stderr := resumeErrorRun(t, command, false)
		return code, string(stdout), string(stderr)
	}
	code, stdout, stderr := run(binary, "install", "stable")
	if code != 0 || !strings.Contains(stdout, "installation completed successfully") || stderr != "" {
		t.Fatalf("actual install %d %s %s", code, stdout, stderr)
	}
	if body, err := os.ReadFile(npmCalls); err != nil || string(body) != "uninstall -g @anthropic-ai/claude-code\nuninstall -g claude-code-local\n" {
		t.Fatal("public install did not perform Source NPM cleanup", err, string(body))
	}
	if _, err := os.Stat(legacyTarget); !os.IsNotExist(err) {
		t.Fatal("public install retained legacy local launcher", err)
	}
	if body, err := os.ReadFile(rcPath); err != nil || string(body) != customRC || !strings.Contains(stdout, "Removed claude alias from "+rcPath) {
		t.Fatal("public install alias consumer", err, string(body), stdout)
	}
	if info, err := os.Stat(rcPath); err != nil || info.Mode().Perm() != 0640 {
		t.Fatal("public install changed shell config mode", err)
	}
	installed := filepath.Join(home, ".local/bin/claude")
	if runtime.GOOS == "windows" {
		installed += ".exe"
	}
	code, stdout, stderr = run(installed, "--version")
	if code != 0 || stdout != "1.2.3 (Claude Code)\n" || stderr != "" {
		t.Fatalf("installed Go entry %d %s %s", code, stdout, stderr)
	}
	installedBytes, err := os.ReadFile(installed)
	if err != nil || !bytes.Equal(installedBytes, content) {
		t.Fatal("installed executable differs", err)
	}
	var saved map[string]json.RawMessage
	global, err := os.ReadFile(config)
	if err != nil || json.Unmarshal(global, &saved) != nil || string(saved["installMethod"]) != `"native"` || string(saved["autoUpdates"]) != "false" || saved["futureInstall"] == nil {
		t.Fatal("global migration", err, string(global))
	}
	settings, err := os.ReadFile(settingsPath)
	if err != nil || json.Unmarshal(settings, &saved) != nil || string(saved["autoUpdatesChannel"]) != `"stable"` || saved["futureSetting"] == nil {
		t.Fatal("channel settings", err, string(settings))
	}
	mu.Lock()
	paths = nil
	mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(legacyTarget), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyTarget, []byte("update must preserve this legacy fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	legacyNPMShim := filepath.Join(home, ".claude/local/node_modules/.bin/claude")
	if err := os.MkdirAll(filepath.Dir(legacyNPMShim), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyNPMShim, []byte("old npm shim retained on update"), 0755); err != nil {
		t.Fatal(err)
	}
	beforeUpdateRC := "alias claude='" + legacyTarget + "'\n" + customRC
	if err := os.WriteFile(rcPath, []byte(beforeUpdateRC), 0640); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = run(installed, "upgrade")
	if code != 0 || !strings.Contains(stdout, "up to date (1.2.3)") || stderr != "" {
		t.Fatalf("actual upgrade alias %d %s %s", code, stdout, stderr)
	}
	if body, err := os.ReadFile(npmCalls); err != nil || string(body) != "uninstall -g @anthropic-ai/claude-code\nuninstall -g claude-code-local\n" {
		t.Fatal("update incorrectly ran legacy NPM cleanup", err, string(body))
	}
	if body, err := os.ReadFile(legacyTarget); err != nil || string(body) != "update must preserve this legacy fixture" {
		t.Fatal("update removed legacy fixture", err, string(body))
	}
	if body, err := os.ReadFile(rcPath); err != nil || string(body) != beforeUpdateRC {
		t.Fatal("update removed shell alias", err, string(body))
	}
	mu.Lock()
	requests := append([]string(nil), paths...)
	mu.Unlock()
	if len(requests) != 1 || requests[0] != "/releases?per_page=100" {
		t.Fatal("already installed update downloaded or picked prerelease", requests)
	}
	code, stdout, stderr = run(installed, "doctor")
	if code != 0 || !strings.Contains(stdout, "Go native (1.2.3)") || !strings.Contains(stdout, "Leftover npm local installation at "+filepath.Join(home, ".claude/local")) || stderr != "" {
		t.Fatalf("actual installed doctor %d %s %s", code, stdout, stderr)
	}
	for _, profile := range []struct {
		name              string
		foreign, checksum bool
	}{{"checksum", false, true}, {"legacy-runtime", true, false}} {
		t.Run(profile.name, func(t *testing.T) {
			beforeGlobal, _ := os.ReadFile(config)
			beforeSettings, _ := os.ReadFile(settingsPath)
			mu.Lock()
			corrupt, nonGo = profile.checksum, profile.foreign
			mu.Unlock()
			code, stdout, stderr := run(installed, "install", "1.2.3", "--force")
			if code != 1 || strings.Contains(stdout, "completed successfully") || !strings.Contains(stderr, map[bool]string{true: "checksum", false: "Go application runtime"}[profile.checksum]) {
				t.Fatalf("rejected artifact %d %s %s", code, stdout, stderr)
			}
			after, _ := os.ReadFile(installed)
			if !bytes.Equal(after, content) {
				t.Fatal("rejected release replaced native entry")
			}
			if data, _ := os.ReadFile(config); !bytes.Equal(data, beforeGlobal) {
				t.Fatal("failed install mutated global config")
			}
			if data, _ := os.ReadFile(settingsPath); !bytes.Equal(data, beforeSettings) {
				t.Fatal("failed install mutated channel")
			}
		})
	}
	mu.Lock()
	corrupt, nonGo = false, false
	mu.Unlock()
	var mcpMu sync.Mutex
	mcpMethods := []string{}
	mcp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var request map[string]json.RawMessage
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			w.WriteHeader(400)
			return
		}
		var method string
		_ = json.Unmarshal(request["method"], &method)
		mcpMu.Lock()
		mcpMethods = append(mcpMethods, method)
		mcpMu.Unlock()
		if request["id"] == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		result := any(map[string]any{})
		if method == "initialize" {
			result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "private-native-doctor-health", "version": "1"}}
		}
		if method == "tools/list" {
			result = map[string]any{"tools": []any{map[string]any{"name": "private_health_tool", "description": "physical MCP health catalog", "inputSchema": map[string]any{"type": "object"}}}}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": result})
	}))
	defer mcp.Close()
	global, _ = os.ReadFile(config)
	var configured map[string]any
	_ = json.Unmarshal(global, &configured)
	configured["mcpServers"] = map[string]any{"physical-health": map[string]any{"type": "http", "url": mcp.URL}}
	global, _ = json.Marshal(configured)
	if err = os.WriteFile(config, global, 0600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = run(installed, "doctor")
	if code != 0 || !strings.Contains(stdout, "physical-health: connected (1 tools)") || stderr != "" {
		t.Fatalf("actual MCP doctor %d %s %s", code, stdout, stderr)
	}
	mcpMu.Lock()
	methods := append([]string(nil), mcpMethods...)
	mcpMu.Unlock()
	if strings.Join(methods, ",") != "initialize,notifications/initialized,tools/list" {
		t.Fatal("doctor did not perform original MCP handshake/catalog", methods)
	}
	if data, _ := os.ReadFile(config); !bytes.Equal(data, global) {
		t.Fatal("doctor changed configured peers")
	}
	code, stdout, stderr = run(installed, "install", "99.99.1")
	if code != 1 || !strings.Contains(stderr, "not available for installation") {
		t.Fatalf("reserved Source version %d %s %s", code, stdout, stderr)
	}
	rows, _ := filepath.Glob(filepath.Join(home, ".claude/projects/**/*.jsonl"))
	if len(rows) > 0 {
		t.Fatal("installation opened Agent chat", rows)
	}
}
