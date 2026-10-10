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
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSDKMCPStatusBeforeASKThenConnectedOriginalProducer(t *testing.T) {
	for _, profile := range []string{"connected", "failed", "disabled"} {
		t.Run(profile, func(t *testing.T) { runSDKMCPStatus(t, profile) })
	}
}
func runSDKMCPStatus(t *testing.T, profileName string) {
	f := worktreeMainFixture(t)
	f.n.CWD = f.home
	f.write(t, filepath.Join(f.config, "settings.json"), `{"permissions":{"allow":["mcp__sdk-p684__read_sdk"]}}`)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture, _ := filepath.Abs("../../testdata/native-sdk-mcp-status.fixture.ts")
	profile, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	source := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", profile, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	source.Dir = f.home
	source.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_SDK_MCP_STATUS_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	if profileName == "failed" {
		source.Env = append(source.Env, "CC_HAHA_SDK_MCP_FAILURE_P715=1")
	}
	if profileName == "disabled" {
		source.Env = append(source.Env, "CC_HAHA_SDK_MCP_DISABLED_P715=1")
	}
	var stderr bytes.Buffer
	source.Stderr = &stderr
	data, err := source.Output()
	if err != nil {
		t.Fatal("actual original SDK status", err, stderr.String())
	}
	var expected struct{ Before, After json.RawMessage }
	if err = json.Unmarshal(data, &expected); err != nil {
		t.Fatal(err, string(data))
	}
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	peer := &sdkMCPRootPeer{input: writer, ack: make(chan struct{}, 1), status: make(chan protocol.JSONObject, 1), failInitialize: profileName == "failed"}
	askStatus := func(want json.RawMessage) {
		t.Helper()
		if _, err := writer.Write([]byte(`{"type":"control_request","request_id":"sdk-status-P711","request":{"subtype":"mcp_status"}}` + "\n")); err != nil {
			t.Error(err)
			return
		}
		select {
		case response := <-peer.status:
			if response.String("subtype") != "success" {
				t.Error("actual SDK status control rejected", rawMain(response))
				return
			}
			value, _ := response.Object("response")
			var a, b any
			_ = json.Unmarshal(value.Get("mcpServers"), &a)
			_ = json.Unmarshal(want, &b)
			if !reflect.DeepEqual(a, b) {
				t.Error("actual original SDK status differs", string(value.Get("mcpServers")), string(want))
			}
		case <-ctx.Done():
			t.Error("actual SDK status did not settle", ctx.Err())
		}
	}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		askStatus(expected.After)
		emitWorktreeModel(w, 1, "", "", "SDK_STATUS_P711_OK")
	}))
	defer model.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
	disabled := false
	var diagnostics bytes.Buffer
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", PermissionMode: "default", MaxTurns: 100400, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: reader, Output: peer, Error: &diagnostics})
	if err != nil {
		t.Fatal(err)
	}
	if profileName == "disabled" {
		err := m.Global.SaveProject(f.n.CWD, func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
			next := current.Clone()
			next.Set("disabledMcpServers", []byte(`["sdk-p684"]`))
			return &next, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		enabled, err := m.nativeSettings.MCPExecutionEnabled(context.Background(), "sdk-p684", protocol.NewJSONObject())
		if err != nil || enabled {
			t.Fatal("private saved disable did not reach actual producer", err, enabled, m.Global.Path())
		}
	}

	done := make(chan error, 1)
	joined := false
	go func() { _, e := m.Run(ctx); done <- e }()
	defer func() {
		_ = writer.Close()
		_ = reader.Close()
		if !joined {
			<-done
		}
		_ = m.ShutdownSession(context.Background())
	}()
	go func() {
		_, _ = writer.Write([]byte(`{"type":"control_request","request_id":"sdk-root-init-P684","request":{"subtype":"initialize","sdkMcpServers":["sdk-p684"]}}` + "\n"))
	}()
	select {
	case <-peer.ack:
	case e := <-done:
		joined = true
		t.Fatal(e, diagnostics.String())
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	askStatus(expected.Before)
	go func() {
		_, _ = writer.Write([]byte(`{"type":"user","message":{"role":"user","content":"Observe the same actual SDK MCP status"}}` + "\n"))
	}()
	err = <-done
	joined = true
	if err != nil {
		t.Fatal(err, diagnostics.String())
	}
}
