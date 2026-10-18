//go:build !windows

package clibootstrap

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
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/fsnotify/fsnotify"
)

type sdkInstructionEnvironmentOutput struct {
	settings chan protocol.JSONObject
	results  chan protocol.JSONObject
}

func (output *sdkInstructionEnvironmentOutput) Write(data []byte) (int, error) {
	var frame protocol.JSONObject
	if err := frame.UnmarshalJSON(bytes.TrimSpace(data)); err != nil {
		return 0, err
	}
	if frame.String("type") == "control_response" {
		response, _ := frame.Object("response")
		if response.String("request_id") == "sdk-directory-settings" {
			output.settings <- response.Clone()
		}
	}
	if frame.String("type") == "result" {
		output.results <- frame.Clone()
	}
	return len(data), nil
}

func TestActualNativeSDKInstructionDirectoryUpdateWhileUserReadPendingMatchesOriginalSettingsAndRequests(t *testing.T) {
	runActualNativeSDKInstructionDirectoryUpdate(t, false)
}

func TestActualNativeSDKDefaultPromptStartsMemoryWhileUserReadPendingAgainstOriginalParallelCaller(t *testing.T) {
	runActualNativeSDKInstructionDirectoryUpdate(t, true)
}

func runActualNativeSDKInstructionDirectoryUpdate(t *testing.T, requireParallelMemory bool) {
	if os.Getenv("CC_HAHA_SOURCE_BUN") == "" {
		t.Skip("pinned whole Source Bun required")
	}
	f := newNativePluginFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var mu sync.Mutex
	var requests [][]byte
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil || request.Method != http.MethodPost || request.URL.Path != "/v1/messages" {
			t.Errorf("actual SDK model request %s %s: %v", request.Method, request.URL, err)
		}
		mu.Lock()
		requests = append(requests, append([]byte(nil), body...))
		mu.Unlock()
		emitWorktreeModel(w, 1, "", "", "SDK_INSTRUCTIONS_OK")
	}))
	defer model.Close()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-user-instructions-sdk-environment.fixture.ts")
	sandbox, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	source := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", "--feature=TRANSCRIPT_CLASSIFIER", fixture)
	source.Dir = f.home
	source.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_USER_INSTRUCTIONS_SDK_ENVIRONMENT=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "USER_TYPE=external", "TZ=UTC", "ANTHROPIC_API_KEY=offline-fixture-key", "ANTHROPIC_BASE_URL=" + model.URL, "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_MAX_RETRIES=0", "CLAUDE_STREAM_TRANSIENT_RETRY_MAX=0", "USE_BUILTIN_RIPGREP=false", "PATH=" + filepath.Join(f.home, "bin")}
	source.Stdin = bytes.NewReader(rawMain(map[string]any{"home": f.home, "origin": model.URL, "verifySettings": true}))
	var sourceErrors bytes.Buffer
	source.Stderr = &sourceErrors
	if stdout, err := source.Output(); err != nil || sourceErrors.Len() != 0 {
		t.Fatalf("actual default concurrent Source: %v stderr=%s stdout=%s", err, sourceErrors.String(), stdout)
	}
	var expected struct {
		Code                                           int
		Project, Initial, NextRequested, AutoDirectory string
		HeldRead, HeldMkdir, ProtectedFilesUnchanged   bool
		SettingsResponse                               protocol.JSONObject
		Caps                                           struct{ Blocked []string }
	}
	data, err := os.ReadFile(filepath.Join(f.home, "sdk-user-instructions-environment-result.json"))
	mu.Lock()
	sourceRequests := requests
	requests = nil
	mu.Unlock()
	if err != nil || json.Unmarshal(data, &expected) != nil || expected.Code != 0 || !expected.HeldRead || !expected.HeldMkdir || !expected.ProtectedFilesUnchanged || len(expected.Caps.Blocked) != 0 || expected.SettingsResponse.String("subtype") != "success" || len(sourceRequests) != 2 {
		t.Fatalf("actual Source settings/request receipt: %v %s requests=%d", err, data, len(sourceRequests))
	}
	var watcher *fsnotify.Watcher
	if requireParallelMemory {
		memoryPath := filepath.Clean(expected.AutoDirectory)
		relative, err := filepath.Rel(expected.Initial, memoryPath)
		if err != nil || expected.AutoDirectory == "" || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || !strings.HasSuffix(memoryPath, string(filepath.Separator)+"memory") {
			t.Fatal("Source memory directory is outside this private configuration", expected.AutoDirectory, err)
		}
		// The original Source just created this empty private directory. Remove
		// only its leaf, so the actual Go mkdir can be observed at the same path
		// without warming the Go memory memo or replacing a filesystem method.
		if err := os.Remove(memoryPath); err != nil {
			t.Fatal("Source memory directory was not an empty private leaf", err)
		}
		watcher, err = fsnotify.NewWatcher()
		if err != nil {
			t.Fatal(err)
		}
		defer watcher.Close()
		if err := watcher.Add(filepath.Dir(memoryPath)); err != nil {
			t.Fatal(err)
		}
	}
	userFile := filepath.Join(expected.Initial, "CLAUDE.md")
	userBody, err := os.ReadFile(userFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(userFile); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(userFile, 0600); err != nil {
		t.Fatal(err)
	}
	f.n.CWD = expected.Project
	f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
	f.n.Environment["ANTHROPIC_API_KEY"] = "offline-fixture-key"
	f.n.Environment["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
	f.n.Environment["PATH"] = filepath.Join(f.home, "bin")
	for _, key := range []string{"CLAUDE_CODE_SIMPLE", "CLAUDE_CODE_DISABLE_AUTO_MEMORY", "CLAUDE_CODE_DISABLE_CLAUDE_MDS"} {
		delete(f.n.Environment, key)
	}
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	output := &sdkInstructionEnvironmentOutput{settings: make(chan protocol.JSONObject, 1), results: make(chan protocol.JSONObject, 2)}
	disabled := false
	var diagnostics bytes.Buffer
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "claude-sonnet-4-5-20250929", PermissionMode: "default", Thinking: requestcontext.Thinking{Type: "disabled"}, MaxTurns: 1, Tools: []string{""}, Sources: State{AllowedSettingSources: []string{"userSettings"}, EnabledSettingSources: []string{"userSettings"}}}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: reader, Output: output, Error: &diagnostics})
	if err != nil {
		_ = os.Remove(userFile)
		_ = os.WriteFile(userFile, userBody, 0600)
		t.Fatal(err)
	}
	if requireParallelMemory {
		if _, err := os.Stat(expected.AutoDirectory); !os.IsNotExist(err) {
			_ = m.ShutdownSession(context.Background())
			_ = os.Remove(userFile)
			_ = os.WriteFile(userFile, userBody, 0600)
			t.Fatal("Go constructor already created the default prompt directory", err)
		}
	}
	type openedFIFO struct {
		file *os.File
		err  error
	}
	opened := make(chan openedFIFO, 1)
	var fifoWriter *os.File
	openedConsumed := false
	done := make(chan error, 1)
	joined := false
	defer func() {
		// Opening this cleanup-only reader releases our test writer even if a
		// setup failure prevented the actual native reader from reaching FIFO.
		if !openedConsumed {
			rescue, _ := os.OpenFile(userFile, os.O_RDONLY|syscall.O_NONBLOCK, 0600)
			result := <-opened
			openedConsumed = true
			fifoWriter = result.file
			if rescue != nil {
				defer rescue.Close()
			}
		}
		if fifoWriter != nil {
			_, _ = fifoWriter.Write(userBody)
			_ = fifoWriter.Close()
		}
		_ = writer.Close()
		_ = reader.Close()
		cancel()
		if !joined {
			<-done
		}
		_ = m.ShutdownSession(context.Background())
		_ = os.Remove(userFile)
		_ = os.WriteFile(userFile, userBody, 0600)
	}()
	go func() { _, err := m.Run(ctx); done <- err }()
	go func() {
		file, err := os.OpenFile(userFile, os.O_WRONLY, 0600)
		opened <- openedFIFO{file, err}
	}()
	sendLine := func(line []byte) {
		t.Helper()
		written := make(chan error, 1)
		go func() { _, err := writer.Write(append(line, '\n')); written <- err }()
		select {
		case err := <-written:
			if err != nil {
				t.Fatal("actual SDK input write", err)
			}
		case err := <-done:
			joined = true
			t.Fatal("native run ended before actual SDK input was consumed", err, diagnostics.String())
		case <-ctx.Done():
			t.Fatal("actual SDK input was not consumed", ctx.Err())
		}
	}
	sendLine([]byte(`{"type":"user","message":{"role":"user","content":"SDK_INSTRUCTIONS_FIRST_HUMAN"}}`))
	select {
	case result := <-opened:
		openedConsumed = true
		fifoWriter = result.file
		if result.err != nil {
			t.Fatal("actual private FIFO writer", result.err)
		}
	case err := <-done:
		joined = true
		t.Fatal("native run ended before original User read", err, diagnostics.String())
	case <-ctx.Done():
		t.Fatal("actual original User reader did not reach physical FIFO", ctx.Err())
	}
	if requireParallelMemory {
		for {
			info, err := os.Stat(expected.AutoDirectory)
			if err == nil && info.IsDir() {
				break
			}
			if err != nil && !os.IsNotExist(err) {
				t.Fatal("actual memory directory observation", err)
			}
			select {
			case _, open := <-watcher.Events:
				if !open {
					t.Fatal("private memory filesystem observer closed")
				}
			case err := <-watcher.Errors:
				t.Fatal("private memory filesystem observer failed", err)
			case err := <-done:
				joined = true
				t.Fatal("native run ended before the concurrent default memory producer", err, diagnostics.String())
			case <-ctx.Done():
				t.Fatal("original default memory mkdir did not run while the actual User read remained held", ctx.Err())
			}
		}
	}
	for _, frame := range []any{
		map[string]any{"type": "update_environment_variables", "variables": map[string]string{"CLAUDE_CONFIG_DIR": expected.NextRequested}},
		map[string]any{"type": "control_request", "request_id": "sdk-directory-settings", "request": map[string]string{"subtype": "get_settings"}},
	} {
		line, _ := json.Marshal(frame)
		sendLine(line)
	}
	var settings protocol.JSONObject
	select {
	case settings = <-output.settings:
	case err := <-done:
		joined = true
		t.Fatal("native run ended before actual environment/settings response", err, diagnostics.String())
	case <-ctx.Done():
		t.Fatal("actual SDK settings response absent", ctx.Err())
	}
	// Release only the genuine read through its real kernel FIFO. The settings
	// response separately records the current ordinary Startup guard; it is
	// not evidence that the serial default-prompt producer ran concurrently.
	if _, err := fifoWriter.Write(userBody); err != nil {
		t.Fatal(err)
	}
	_ = fifoWriter.Close()
	if settings.String("subtype") != expected.SettingsResponse.String("subtype") {
		t.Fatalf("actual SDK configuration-directory admission differs: native=%s Source=%s", rawMain(settings), rawMain(expected.SettingsResponse))
	}
	var actualReply, expectedReply any
	if err := json.Unmarshal(rawMain(settings), &actualReply); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(rawMain(expected.SettingsResponse), &expectedReply); err != nil || !reflect.DeepEqual(actualReply, expectedReply) {
		t.Fatalf("actual SDK refreshed effective/sources/applied differ: native=%s Source=%s error=%v", rawMain(settings), rawMain(expected.SettingsResponse), err)
	}
	for _, human := range []string{"", "SDK_INSTRUCTIONS_SECOND_HUMAN"} {
		if human != "" {
			sendLine([]byte(`{"type":"user","message":{"role":"user","content":"SDK_INSTRUCTIONS_SECOND_HUMAN"}}`))
		}
		select {
		case <-output.results:
		case err := <-done:
			joined = true
			t.Fatal("actual updated-directory request did not complete", err, diagnostics.String())
		case <-ctx.Done():
			t.Fatal("actual updated-directory model result absent", ctx.Err())
		}
	}
	_ = writer.Close()
	select {
	case err := <-done:
		joined = true
		if err != nil {
			t.Fatal(err, diagnostics.String())
		}
	case <-ctx.Done():
		t.Fatal("actual SDK run did not close after two completed requests", ctx.Err())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != len(sourceRequests) {
		t.Fatalf("actual native model requests=%d Source=%d", len(requests), len(sourceRequests))
	}
	for _, request := range requests {
		if !bytes.Contains(request, []byte("ACTUAL_SDK_USER_INITIAL")) || !bytes.Contains(request, []byte("ACTUAL_SDK_RULE_NEXT")) || strings.Contains(string(request), "ACTUAL_SDK_USER_NEXT") {
			t.Fatal("actual admitted request differs from captured original user/rules snapshot", string(request))
		}
		if requireParallelMemory && !bytes.Contains(request, []byte(expected.AutoDirectory)) {
			t.Fatal("parallel default memory prompt did not preserve its original captured directory", string(request))
		}
	}
}
