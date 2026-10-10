package clibootstrap

import (
	"context"
	"encoding/pem"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestNativeMCPOrdinaryWorkflowCloseDeadlineAndOwnerContinuationActualSource(t *testing.T) {
	home, env := ordinaryFixture(t)
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		started <- struct{}{}
		<-release
		emitPlanFixture(w, 1, "", "ACTUAL_JOINED_WORKFLOW")
	}))
	defer peer.Close()
	cert := filepath.Join(home, "cert.pem")
	_ = os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw}), 0600)
	env["ANTHROPIC_BASE_URL"], env["NODE_EXTRA_CA_CERTS"] = peer.URL, cert
	script := `export const meta={name:'actual-join',description:'actual owner lifetime'};return await agent('Wait for the controlled native transport')`
	source := startOrdinarySource(t, home, home, env)
	launched := ordinaryData(t, source.call(t, "Workflow", map[string]any{"script": script}))
	select {
	case <-started:
	case <-time.After(8 * time.Second):
		t.Fatal("Source actual request absent")
	}
	stopped := source.call(t, "TaskStop", map[string]any{"task_id": launched.String("taskId")})
	if string(stopped.Get("isError")) != "true" {
		t.Fatal("Source fresh state unexpectedly adopted job")
	}
	_ = source.in.Close()
	sourceDone := make(chan error, 1)
	go func() { sourceDone <- source.cmd.Wait() }()
	host, _ := ordinaryGoContext(t, home, home, env)
	result, e := host.Call(context.Background(), "Workflow", rawMain(map[string]any{"script": script}))
	if e != nil {
		t.Fatal(e)
	}
	goLaunched := ordinaryData(t, result)
	select {
	case <-started:
	case <-time.After(8 * time.Second):
		t.Fatal("Go actual request absent")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	e = host.Close(ctx)
	if !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("Close failed to retain actual native continuation", e)
	}
	select {
	case e := <-sourceDone:
		t.Fatal("Source EOF exited with physical model still pending", e)
	default:
	}
	once.Do(func() { close(release) })
	if e := <-sourceDone; e != nil {
		t.Fatal("Source physical EOF join", e)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel2()
	if e := host.Close(ctx2); e != nil {
		t.Fatal("Go actual continuation did not join after completion", e)
	}
	ordinaryAwaitJournal(t, filepath.Join(goLaunched.String("transcriptDir"), "journal.jsonl"))
	ordinaryAwaitJournal(t, filepath.Join(launched.String("transcriptDir"), "journal.jsonl"))
	if _, e := host.Call(context.Background(), "Read", rawMain(map[string]any{"file_path": cert})); !errors.Is(e, cooperative.ErrClosed) {
		t.Fatal("closed context accepted new request", e)
	}
}
