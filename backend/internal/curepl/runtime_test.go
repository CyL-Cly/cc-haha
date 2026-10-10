package curepl

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// This enters the actual kernel in an isolated subprocess of the test binary.
// No provider/native-call seam or compiled business outcome is supplied.
func TestWorkerChildProcess(t *testing.T) {
	for _, arg := range os.Args {
		if arg == "-test.run=^TestWorkerChildProcess$" {
			if err := RunWorker(context.Background(), os.Stdin, os.Stdout); err != nil {
				os.Stderr.WriteString(err.Error())
				os.Exit(1)
			}
			os.Exit(0)
		}
	}
}
func isolatedRuntime(t *testing.T) *Runtime {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("actual macOS sandbox worker requires Darwin")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewRuntime(RuntimeOptions{Executable: executable, TempDir: t.TempDir(), WorkerArguments: []string{"-test.run=^TestWorkerChildProcess$"}, HealthCheck: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	return r
}
func noCalls(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return nil, errors.New("unexpected native dispatch")
}
func TestIsolatedSandboxKernelRetainsCellsAndResetsAfterTimeout(t *testing.T) {
	r := isolatedRuntime(t)
	first := r.Run(context.Background(), `let retained=41; nodeRepl.write(retained)`, 10000, noCalls)
	if first.IsError || len(first.Content) != 1 || first.Content[0].Text != "41" {
		t.Fatal(first)
	}
	second := r.Run(context.Background(), `retained++; nodeRepl.write(retained)`, 10000, noCalls)
	if second.IsError || len(second.Content) != 1 || second.Content[0].Text != "42" {
		t.Fatal(second)
	}
	failure := r.Run(context.Background(), `while(true) {}`, 60, noCalls)
	if !failure.IsError || failure.StructuredContent["bindingsReset"] != true || !strings.Contains(failure.Content[len(failure.Content)-1].Text, "timed out") {
		t.Fatal(failure)
	}
	fresh := r.Run(context.Background(), `nodeRepl.write(typeof retained)`, 10000, noCalls)
	if fresh.IsError || len(fresh.Content) != 1 || fresh.Content[0].Text != "undefined" {
		t.Fatal(fresh)
	}
}
func TestIsolatedSandboxBridgeCancellationDrainsActualPendingDispatch(t *testing.T) {
	r := isolatedRuntime(t)
	started := make(chan struct{})
	drained := make(chan struct{})
	result := make(chan Result, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		result <- r.Run(ctx, `await cua.computer.list_apps()`, 10000, func(ctx context.Context, name string, _ json.RawMessage) (json.RawMessage, error) {
			if name != "list_apps" {
				t.Error(name)
			}
			close(started)
			<-ctx.Done()
			close(drained)
			return nil, ctx.Err()
		})
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("dispatch did not start")
	}
	cancel()
	select {
	case got := <-result:
		select {
		case <-drained:
		default:
			t.Fatal("returned before native dispatch drained")
		}
		if !got.IsError || got.StructuredContent["nativeCallsStarted"] != 1 || got.StructuredContent["nativeResultUnknown"] != true || got.StructuredContent["bindingsReset"] != true {
			t.Fatal(got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled kernel did not terminate")
	}
}
func TestContentBoundaryPreservesEmptyTextAndLoneUTF16(t *testing.T) {
	for _, raw := range []string{`{"type":"text","text":""}`, `{"type":"text","text":"\ud800"}`, `{"type":"image","data":"","mimeType":"image/png"}`} {
		var c Content
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(c)
		if err != nil || string(encoded) != raw {
			t.Fatalf("%s => %s %v", raw, encoded, err)
		}
	}
	for _, raw := range []string{`{"type":"text"}`, `{"type":"text","text":null}`, `{"type":"image","data":""}`} {
		var c Content
		if json.Unmarshal([]byte(raw), &c) == nil {
			t.Fatal("accepted invalid content", raw)
		}
	}
}
