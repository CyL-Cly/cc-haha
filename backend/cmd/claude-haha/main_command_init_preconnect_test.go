package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliargs"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/testdata/keychainprefetchfixture"
)

func TestActualOrdinaryPluginCommandInitHEADAgainstWholeCommanderSource(t *testing.T) {
	pairedOrdinaryPluginCommandSource(t, nil, 0, true)
}

func TestActualOrdinaryPluginCommandKnownFileAgainstWholeCommanderSource(t *testing.T) {
	for _, row := range []struct {
		name, content string
		code          int
	}{
		{"empty", `{}`, 0},
		{"entries-unknown-fields", `{"alpha":{"source":{"source":"directory","path":"$HOME/market"},"installLocation":"$HOME/market","lastUpdated":"2026-10-01T00:00:00.000Z","futureEntry":{"keep":true}}}`, 0},
		{"syntax-error", `{`, 1},
		{"schema-error", `{"bad":{"source":{"source":"not-a-source"}}}`, 1},
	} {
		t.Run(row.name, func(t *testing.T) { pairedOrdinaryPluginCommandSource(t, &row.content, row.code, false) })
	}
}

func pairedOrdinaryPluginCommandSource(t *testing.T, knownContent *string, expectedCode int, requireActualHEAD bool) {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned whole Source Commander runtime required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var sourceHeads, nativeHeads atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil || len(body) != 0 || request.Method != http.MethodHead || request.Header.Get("Authorization") != "" || request.Header.Get("X-Api-Key") != "" || request.Header.Get("User-Agent") != "Bun/1.3.14" {
			t.Error("actual init HEAD changed its independent fetch contract", request.Method, request.Header, string(body), err)
		}
		switch request.URL.Path {
		case "/safe":
			sourceHeads.Add(1)
		case "/native-safe":
			nativeHeads.Add(1)
		default:
			t.Error("init used launch or project environment before/after its real Safe boundary", request.URL)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer peer.Close()
	sourceHome := t.TempDir()
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-command-init-preconnect.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = sourceHome
	command.Env = []string{"HOME=" + sourceHome, "USERPROFILE=" + sourceHome, "CLAUDE_CONFIG_DIR=" + filepath.Join(sourceHome, ".claude"), "TMPDIR=" + sourceHome, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(sourceHome), "CC_HAHA_COMMAND_INIT_HEAD_SOURCE=1", "USER_TYPE=external", "NODE_ENV=production", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1", "ANTHROPIC_BASE_URL=" + peer.URL + "/launch", "NO_COLOR=1"}
	sourceInput := map[string]any{"home": sourceHome, "origin": peer.URL, "profile": "plugin"}
	if knownContent != nil {
		sourceInput["knownContent"] = strings.ReplaceAll(*knownContent, "$HOME", sourceHome)
	}
	input, _ := json.Marshal(sourceInput)
	command.Stdin = bytes.NewReader(input)
	var sourceDiagnostic bytes.Buffer
	command.Stderr = &sourceDiagnostic
	capturePublicGoCoverage(t, command)
	output, err := command.Output()
	if err != nil {
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) || exitError.ExitCode() != expectedCode {
			t.Fatal("whole original Commander Source command", err, sourceDiagnostic.String(), string(output))
		}
	}
	data, err := os.ReadFile(filepath.Join(sourceHome, "ordinary-command-init-source-receipt.json"))
	var source struct {
		Code               int
		ProtectedUnchanged bool
		Calls              []struct {
			URL, Method, Safe string
			Signal            bool
			Timeout           int
		}
		Caps struct{ Blocked []string }
	}
	if err != nil || json.Unmarshal(data, &source) != nil || source.Code != expectedCode || !source.ProtectedUnchanged || len(source.Caps.Blocked) != 0 || len(source.Calls) != 1 || source.Calls[0].URL != peer.URL+"/safe" || source.Calls[0].Method != http.MethodHead || !source.Calls[0].Signal || source.Calls[0].Timeout != 10000 || sourceHeads.Load() != 1 {
		t.Fatal("complete original init/command path did not establish real one-HEAD contract", err, string(data), sourceHeads.Load(), sourceDiagnostic.String())
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, cwd, temporary := filepath.Join(root, "home"), filepath.Join(root, "workspace"), filepath.Join(root, "tmp")
	for _, directory := range []string{home, cwd, temporary, filepath.Join(home, ".claude")} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	settings := []byte(`{"env":{"ANTHROPIC_BASE_URL":"` + peer.URL + `/native-safe","PROBE_COMMAND_INIT_SAFE":"user"},"futurePrivate":{"keep":true}}`)
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if err := os.WriteFile(settingsPath, settings, 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": filepath.Join(home, ".claude"), "TMPDIR": temporary, "USER_TYPE": "external", "NODE_ENV": "production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "DISABLE_TELEMETRY": "1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH": "1", "ANTHROPIC_BASE_URL": peer.URL + "/native-launch"}
	native, err := clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: "darwin", ManagedRoot: filepath.Join(root, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrNotExist }
	native.StartKeychainPrefetch = keychainprefetchfixture.Bind(native.Temp, native.CWD, native.Home, func(ctx context.Context, call securestorage.Command) ([]byte, error) {
		return native.RunSecurity(ctx, call)
	})
	var fetches atomic.Int32
	var fetchMu sync.Mutex
	var fetchStages []string
	native.PreconnectHTTP = func(request *http.Request, client *http.Client) (*http.Response, error) {
		fetches.Add(1)
		fetchMu.Lock()
		fetchStages = append(fetchStages, "entry ctx="+fmt.Sprint(request.Context().Err()))
		fetchMu.Unlock()
		if request == nil || client == nil || request.Method != http.MethodHead || request.URL.String() != peer.URL+"/native-safe" {
			t.Error("native original init primitive escaped exact owned loopback", request)
			return nil, os.ErrPermission
		}
		response, err := client.Do(request)
		fetchMu.Lock()
		fetchStages = append(fetchStages, "return error="+fmt.Sprint(err))
		fetchMu.Unlock()
		return response, err
	}
	if knownContent != nil {
		directory := filepath.Join(home, ".claude/plugins")
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "known_marketplaces.json"), []byte(strings.ReplaceAll(*knownContent, "$HOME", home)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	args := []string{"plugin", "marketplace", "list", "--json"}
	parsed := cliargs.Parse(args, cliargs.Config{Version: version})
	if parsed.Parsed == nil {
		t.Fatal("actual original parser", parsed)
	}
	var actual, diagnostic bytes.Buffer
	code := runNativePluginCommand(ctx, args, parsed.Parsed, native, &actual, &diagnostic)
	if code != source.Code || strings.ReplaceAll(actual.String(), home, "$HOME") != strings.ReplaceAll(string(output), sourceHome, "$HOME") || strings.ReplaceAll(diagnostic.String(), home, "$HOME") != strings.ReplaceAll(sourceDiagnostic.String(), sourceHome, "$HOME") {
		t.Fatal("ordinary original command handler changed", code, actual.String(), diagnostic.String(), string(output), sourceDiagnostic.String())
	}
	if fetches.Load() != int32(len(source.Calls)) || (requireActualHEAD && nativeHeads.Load() != sourceHeads.Load()) {
		t.Fatal("real ordinary command preAction omitted actual Source init HEAD", "nativeFetches", fetches.Load(), "nativeHTTP", nativeHeads.Load(), "SourceFetches", len(source.Calls), "SourceHTTP", sourceHeads.Load(), "stages", fetchStages)
	}
	if data, err := os.ReadFile(settingsPath); err != nil || !bytes.Equal(data, settings) {
		t.Fatal("ordinary command init changed protected user settings", err, string(data))
	}
}
