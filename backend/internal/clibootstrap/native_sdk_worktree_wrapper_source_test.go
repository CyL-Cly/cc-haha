package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type SDKWrapperPeer struct {
	sdkWorktreePeerWriter
	initialized chan struct{}
}

func (p *SDKWrapperPeer) Write(data []byte) (int, error) {
	n, e := p.sdkWorktreePeerWriter.Write(data)
	var event protocol.JSONObject
	_ = event.UnmarshalJSON(bytes.TrimSpace(data))
	if event.String("type") == "control_response" {
		response, _ := event.Object("response")
		if response.String("request_id") == "init-wrapper629" {
			close(p.initialized)
		}
	}
	return n, e
}
func TestActualSourceWorktreeSDKWrappersDoNotInheritParentAbortBeforePeerReply(t *testing.T) {
	f := newNativePluginFixture(t)
	home, e := filepath.EvalSymlinks(f.home)
	if e != nil {
		t.Fatal(e)
	}
	f.n.Home = home
	f.n.CWD = home
	f.n.Environment["HOME"] = home
	f.n.Environment["CLAUDE_CONFIG_DIR"] = filepath.Join(home, ".claude")
	f.n.Environment["ANTHROPIC_API_KEY"] = "fake629"
	f.write(t, filepath.Join(home, ".claude", "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	output := &SDKWrapperPeer{sdkWorktreePeerWriter: sdkWorktreePeerWriter{input: writer, home: home}, initialized: make(chan struct{})}
	var diagnostics bytes.Buffer
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", PermissionMode: "default", MaxTurns: 100400, Sources: DefaultState(), SessionID: "62900000-0000-4000-8000-000000000629"}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: reader, Output: output, Error: &diagnostics})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, e := m.Run(ctx); done <- e }()
	go func() {
		_, _ = writer.Write([]byte(`{"type":"control_request","request_id":"init-wrapper629","request":{"subtype":"initialize","hooks":{"WorktreeCreate":[{"hookCallbackIds":["wrapper629"]}],"WorktreeRemove":[{"hookCallbackIds":["remove-wrapper629"]}]}}}` + "\n"))
	}()
	select {
	case <-output.initialized:
	case <-ctx.Done():
		t.Fatal("real SDK initialize did not settle")
	}
	aborted, abort := context.WithCancel(context.Background())
	abort()
	var path string
	receipt, e := m.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(scope *cooperative.Scope) error {
		var e error
		path, e = m.hook.ExecuteWorktreeCreateHook(aborted, "agent-a1234567", m.Controller.SessionID(), queryhooks.Context{Scope: scope})
		if e != nil {
			return e
		}
		ran, e := m.hook.ExecuteWorktreeRemoveHook(aborted, path, m.Controller.SessionID(), queryhooks.Context{Scope: scope})
		if !ran && e == nil {
			t.Error("original remove wrapper skipped for parent abort")
		}
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	if e = receipt.Wait(); e != nil {
		t.Fatal("original wrappers", e, diagnostics.String())
	}
	_ = writer.Close()
	select {
	case e = <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal("SDK wrapper Close stranded owner")
	}
	rows := []map[string]any{{"name": "create-wrapper-preabort", "id": "wrapper629", "event": "WorktreeCreate", "wrapper": true, "preAbort": true, "response": map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "WorktreeCreate", "worktreePath": path}}}, {"name": "remove-wrapper-preabort", "id": "remove-wrapper629", "event": "WorktreeRemove", "wrapper": true, "preAbort": true, "response": map[string]any{}}}
	fixture, _ := filepath.Abs("../../testdata/native-sdk-worktree-callback.fixture.ts")
	sandbox, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SDK_WORKTREE_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"sid": m.Controller.SessionID(), "path": path, "rows": rows}))
	var sourceDiagnostic bytes.Buffer
	cmd.Stderr = &sourceDiagnostic
	data, e := cmd.Output()
	if e != nil {
		t.Fatal("actual Source worktree wrappers", e, sourceDiagnostic.String())
	}
	var source struct {
		Rows []struct {
			Request protocol.JSONObject
			Result  json.RawMessage
		}
	}
	if e = json.Unmarshal(data, &source); e != nil || len(source.Rows) != 2 {
		t.Fatal(e, string(data))
	}
	output.mu.Lock()
	defer output.mu.Unlock()
	frames := []protocol.JSONObject{}
	for _, event := range output.frames {
		request, _ := event.Object("request")
		if request.String("subtype") == "hook_callback" {
			frames = append(frames, event)
		}
		if event.String("type") == "control_cancel_request" {
			t.Fatal("wrapper inherited parent abort")
		}
	}
	if len(frames) != 2 {
		t.Fatal("actual wrapper callbacks", len(frames))
	}
	for i, event := range frames {
		got, _ := event.Object("request")
		want, _ := source.Rows[i].Request.Object("request")
		got.Delete("tool_use_id")
		want.Delete("tool_use_id")
		var a, b any
		_ = json.Unmarshal(rawMain(got), &a)
		_ = json.Unmarshal(rawMain(want), &b)
		if !reflect.DeepEqual(a, b) {
			t.Fatal("original wrapper input", string(rawMain(got)), string(rawMain(want)))
		}
	}
	if string(source.Rows[1].Result) != "true" || !strings.Contains(string(source.Rows[0].Result), path) {
		t.Fatal("actual Source wrapper output", string(data))
	}
	if _, e = os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("remove callback did not remove physical source-returned path", e)
	}
}
