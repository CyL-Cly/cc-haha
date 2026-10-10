package clibootstrap

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestActualSDKWorktreeResponseReplayConsumesOnlyMatchingPacketAndNoNewTurn(t *testing.T) {
	f := worktreeMainFixture(t)
	var count atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		n := int(count.Add(1))
		switch n {
		case 1:
			emitWorktreeModel(w, n, "Agent", `{"prompt":"Read hook-owned.txt","description":"replay SDK isolated reader","subagent_type":"general-purpose","isolation":"worktree"}`, "")
		case 2:
			emitWorktreeModel(w, n, "Read", `{"file_path":"hook-owned.txt"}`, "")
		case 3:
			emitWorktreeModel(w, n, "", "", "actual child complete")
		case 4:
			emitWorktreeModel(w, n, "", "", "actual parent complete")
		default:
			t.Errorf("orphan/replay started new model turn %d", n)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer model.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	output := &sdkWorktreePeerWriter{input: writer, home: f.home, oneTurn: true, duplicateReply: true}
	var diagnostics bytes.Buffer
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", PermissionMode: "default", MaxTurns: 100400, Sources: DefaultState(), Replay: true}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: reader, Output: output, Error: &diagnostics})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	go func() {
		_, _ = writer.Write([]byte(`{"type":"control_request","request_id":"init-replay629","request":{"subtype":"initialize","hooks":{"WorktreeCreate":[{"hookCallbackIds":["create-replay629"]}]}}}` + "\n" + `{"type":"user","message":{"role":"user","content":"Create one isolated reader"}}` + "\n"))
	}()
	if _, e = m.Run(ctx); e != nil {
		t.Fatal(e, diagnostics.String())
	}
	output.mu.Lock()
	defer output.mu.Unlock()
	replayed := 0
	for _, event := range output.frames {
		if event.String("type") != "control_response" {
			continue
		}
		response, _ := event.Object("response")
		switch response.String("request_id") {
		case "init-replay629":
		case "orphan629":
			t.Fatal("orphan response replayed")
		default:
			replayed++
		}
	}
	if replayed != 1 || count.Load() != 4 || output.final != 1 {
		t.Fatal("actual Source replay cardinality", replayed, count.Load(), output.final)
	}
}
