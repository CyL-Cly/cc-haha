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
	"os/exec"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type sdkRunMCPOrderOutput struct {
	peer    io.Writer
	observe func(protocol.JSONObject)
}

func (output sdkRunMCPOrderOutput) Write(data []byte) (int, error) {
	var frame protocol.JSONObject
	if err := frame.UnmarshalJSON(bytes.TrimSpace(data)); err != nil {
		return 0, err
	}
	output.observe(frame)
	return output.peer.Write(data)
}

func TestActualNativeSDKRunWaitsForMCPBeforeDequeuingOriginalCommand(t *testing.T) {
	f := worktreeMainFixture(t)
	f.n.CWD = f.home
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var requests atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil || !bytes.Contains(body, []byte("SDK_ORDER_HUMAN")) {
			t.Errorf("actual accepted SDK prompt: %v %s", err, body)
		}
		requests.Add(1)
		emitWorktreeModel(w, 1, "", "", "SDK_ORDER_OK")
	}))
	defer model.Close()
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-sdk-run-mcp-order.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	profile, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	source := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", profile, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", "--feature=TRANSCRIPT_CLASSIFIER", fixture)
	source.Dir = f.home
	source.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_SDK_RUN_MCP_ORDER=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "TZ=UTC", "ANTHROPIC_API_KEY=offline-fixture-key", "ANTHROPIC_BASE_URL=" + model.URL, "CLAUDE_CODE_SIMPLE=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1", "CLAUDE_CODE_DISABLE_FAST_MODE=1", "CLAUDE_CODE_MAX_RETRIES=0", "CLAUDE_STREAM_TRANSIENT_RETRY_MAX=0", "USE_BUILTIN_RIPGREP=false", "PATH=" + filepath.Join(f.home, "bin")}
	source.Stdin = bytes.NewReader(rawMain(map[string]string{"origin": model.URL}))
	var sourceErrors bytes.Buffer
	source.Stderr = &sourceErrors
	if stdout, err := source.Output(); err != nil || sourceErrors.Len() != 0 {
		t.Fatalf("actual original runHeadless: %v stderr=%s stdout=%s", err, sourceErrors.String(), stdout)
	}
	var expected struct {
		Observations []struct {
			Dequeues, Starts int
		} `json:"observations"`
		Capabilities struct{ Blocked []string } `json:"capabilities"`
	}
	data, err := os.ReadFile(filepath.Join(f.home, "sdk-run-mcp-order-result.json"))
	if err != nil || json.Unmarshal(data, &expected) != nil || len(expected.Observations) != 1 || len(expected.Capabilities.Blocked) != 0 || requests.Swap(0) != 1 {
		t.Fatalf("actual Source consumer receipt: %v %s", err, data)
	}
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	peer := sdkControlStartPeer(t, ctx, f.home, f.config, writer)
	peer.closeOnResult = true
	var dequeues, starts atomic.Int32
	observed := make(chan [2]int, 8)
	output := sdkRunMCPOrderOutput{peer: peer, observe: func(frame protocol.JSONObject) {
		request, _ := frame.Object("request")
		message, _ := request.Object("message")
		if request.String("subtype") == "mcp_message" && message.String("method") == "initialize" {
			observed <- [2]int{int(dequeues.Load()), int(starts.Load())}
		}
	}}
	f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
	disabled := false
	var diagnostics bytes.Buffer
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", PermissionMode: "default", MaxTurns: 1, Tools: []string{""}, SystemPrompt: "original SDK ordering system", HasCustomSystemPrompt: true, SessionPersistenceDisabled: true, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: reader, Output: output, Error: &diagnostics, OnControllerPhase: func(phase string) {
		switch phase {
		case "dequeue":
			dequeues.Add(1)
		case "lifecycle:started":
			starts.Add(1)
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	done := make(chan error, 1)
	go func() { _, failure := m.Run(ctx); done <- failure }()
	go func() {
		_, _ = fmt.Fprintln(writer, `{"type":"control_request","request_id":"order-init","request":{"subtype":"initialize","sdkMcpServers":["order-peer"]}}`)
		_, _ = fmt.Fprintln(writer, `{"type":"user","uuid":"order-user","message":{"role":"user","content":"SDK_ORDER_HUMAN"}}`)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("actual native SDK run", err, diagnostics.String())
		}
	case <-ctx.Done():
		t.Fatal("actual native SDK run did not complete", ctx.Err(), diagnostics.String())
	}
	want := [2]int{expected.Observations[0].Dequeues, expected.Observations[0].Starts}
	select {
	case actual := <-observed:
		if !reflect.DeepEqual(actual, want) {
			t.Fatalf("SDK initialize entered after command acceptance: native dequeues/started=%v, actual original=%v", actual, want)
		}
	default:
		t.Fatal("actual native SDK MCP handshake absent")
	}
	if len(observed) != 0 || peer.countSnapshot()["order-peer"].Initialize != 1 {
		t.Fatal("actual SDK run started a second handshake", len(observed), peer.countSnapshot())
	}
	if requests.Load() != 1 || dequeues.Load() != 1 || starts.Load() != 1 || diagnostics.Len() != 0 {
		t.Fatal("actual one-prompt command/model lifecycle", requests.Load(), dequeues.Load(), starts.Load(), diagnostics.String())
	}
}
