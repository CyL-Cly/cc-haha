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
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSDKInferenceControlOriginalSourceThenDefaultMainPrintNoModel(t *testing.T) {
	f := newNativePluginFixture(t)
	oldHome := f.home
	home, e := filepath.EvalSymlinks(f.home)
	if e != nil {
		t.Fatal(e)
	}
	f.home = home
	f.cwd = home
	f.config = filepath.Join(home, ".claude")
	f.n.Home = home
	f.n.CWD = home
	f.n.Environment["HOME"] = home
	f.n.Environment["CLAUDE_CONFIG_DIR"] = f.config
	f.n.Environment["ANTHROPIC_API_KEY"] = "fake-sdk-controls"
	f.n.Environment["CLAUDE_CODE_ENTRYPOINT"] = "sdk-cli"
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
	known, e := os.ReadFile(filepath.Join(f.config, "plugins", "known_marketplaces.json"))
	if e != nil {
		t.Fatal(e)
	}
	f.write(t, filepath.Join(f.config, "plugins", "known_marketplaces.json"), strings.ReplaceAll(string(known), oldHome, home))
	f.write(t, filepath.Join(home, ".git", "HEAD"), "ref: refs/heads/private-sdk-control\n")
	var modelCalls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls.Add(1)
		http.Error(w, "unexpected model request", 500)
	}))
	defer peer.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	controls := []map[string]any{{"subtype": "set_model", "model": "fixture-model-2"}, {"subtype": "set_max_thinking_tokens", "max_thinking_tokens": 0}, {"subtype": "set_max_thinking_tokens", "max_thinking_tokens": 1536}, {"subtype": "set_max_thinking_tokens", "max_thinking_tokens": nil}, {"subtype": "set_model", "model": "default"}}
	ctx, finish := context.WithTimeout(context.Background(), 20*time.Second)
	defer finish()
	fixture, _ := filepath.Abs("../../testdata/native-sdk-initialize-options.fixture.ts")
	preload, _ := filepath.Abs("../../../preload")
	sandbox, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	c := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", "--preload", preload, fixture)
	c.Dir = home
	c.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SDK_OPTIONS_FIXTURE=1", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_ENTRYPOINT=sdk-cli", "NODE_ENV=production", "ANTHROPIC_API_KEY=fake-sdk-controls", "ANTHROPIC_BASE_URL=" + peer.URL, "PATH=/usr/bin:/bin"}
	c.Stdin = bytes.NewReader(rawMain(map[string]any{"controls": controls, "initial": "fixture-model", "override": "fixture-model"}))
	var sourceError bytes.Buffer
	c.Stderr = &sourceError
	data, e := c.Output()
	if e != nil {
		t.Fatal("actual Source SDK inference control", e, sourceError.String())
	}
	var source struct {
		Frames       []protocol.JSONObject
		Snapshots    []protocol.JSONObject
		Breadcrumbs  []protocol.JSONObject
		Capabilities protocol.JSONObject
	}
	if e = json.Unmarshal(data, &source); e != nil || len(source.Frames) != len(controls) {
		t.Fatal(e, string(data))
	}
	var blocked []string
	_ = json.Unmarshal(source.Capabilities.Get("blocked"), &blocked)
	if len(blocked) > 0 {
		t.Fatal("Source capability escape", blocked)
	}
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	frames := make(chan protocol.JSONObject, 16)
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", PermissionMode: "default", MaxTurns: 100400, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: reader, Output: io.Discard, Error: io.Discard, OnOutput: func(data []byte) error {
		var frame protocol.JSONObject
		if err := frame.UnmarshalJSON(data); err != nil {
			return err
		}
		if frame.String("type") == "control_response" {
			frames <- frame.Clone()
		}
		return nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	runFinished := make(chan struct{})
	t.Cleanup(func() {
		_ = writer.Close()
		select {
		case <-runFinished:
		case <-time.After(3 * time.Second):
			t.Error("actual SDK input did not finish before cleanup")
		}
		_ = m.ShutdownSession(context.Background())
	})
	runDone := make(chan error, 1)
	go func() { defer close(runFinished); _, err := m.Run(ctx); runDone <- err }()
	send := func(id string, request any) protocol.JSONObject {
		t.Helper()
		packet := append(rawMain(map[string]any{"type": "control_request", "request_id": id, "request": request}), '\n')
		if _, err := writer.Write(packet); err != nil {
			t.Fatal(err)
		}
		select {
		case frame := <-frames:
			return frame
		case err := <-runDone:
			t.Fatal("actual input ended before ACK", err)
		case <-ctx.Done():
			t.Fatal("actual control ACK missing", id, ctx.Err())
		}
		return protocol.JSONObject{}
	}
	_ = send("init-control", map[string]any{"subtype": "initialize"})
	actualFrames := []protocol.JSONObject{}
	for i, request := range controls {
		response, _ := source.Frames[i].Object("response")
		actualFrames = append(actualFrames, send(response.String("request_id"), request))
		var snapshot protocol.JSONObject
		receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
			model, err := m.Host.InferenceModel(scope)
			if err != nil {
				return err
			}
			snapshot = protocol.NewJSONObject(protocol.JSONField{Name: "model", Value: quoteMain(model)})
			if m.options.Arguments.Thinking.Type != "" {
				snapshot.Set("thinkingConfig", rawMain(m.options.Arguments.Thinking))
			}
			return nil
		})
		if err == nil {
			err = receipt.Wait()
		}
		if err != nil {
			t.Fatal("same Owner lawful read", err)
		}
		var a, b any
		_ = json.Unmarshal(rawMain(snapshot), &a)
		_ = json.Unmarshal(rawMain(source.Snapshots[i]), &b)
		if !reflect.DeepEqual(a, b) {
			t.Fatal("actual Source model/thinking state", i, string(rawMain(snapshot)), string(rawMain(source.Snapshots[i])))
		}
	}
	_ = writer.Close()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatal("actual input EOF", err)
		}
	case <-ctx.Done():
		t.Fatal("actual input did not join", ctx.Err())
	}

	var a, b any
	_ = json.Unmarshal(rawMain(actualFrames), &a)
	_ = json.Unmarshal(rawMain(source.Frames), &b)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("actual Source control ACKs", string(rawMain(actualFrames)), string(rawMain(source.Frames)))
	}
	if modelCalls.Load() != 0 {
		t.Fatal("control-only SDK unexpectedly queried model", modelCalls.Load())
	}
}
