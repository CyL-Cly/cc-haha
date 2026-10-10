package clibootstrap

import (
	"bufio"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpcli"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"syscall"
	"testing"
	"time"
)

func TestNativeMCPOrdinaryConcurrentFIFORequestsActualSource(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("actual FIFO fixture is Unix")
	}
	home, env := ordinaryFixture(t)
	fifo := filepath.Join(home, "owned.fifo")
	if e := syscall.Mkfifo(fifo, 0600); e != nil {
		t.Fatal(e)
	}
	packets := []map[string]any{{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "Bash", "arguments": map[string]any{"command": "read value < " + fifo + "; printf received-$value", "description": "actual blocking FIFO consumer", "timeout": 5000}}}, {"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "Bash", "arguments": map[string]any{"command": "printf released > " + fifo + "; printf writer-complete", "description": "actual FIFO producer", "timeout": 5000}}}}
	source := startOrdinarySource(t, home, home, env)
	for _, p := range packets {
		if _, e := source.in.Write(append(rawMain(p), '\n')); e != nil {
			t.Fatal(e)
		}
	}
	read := func(scanner *bufio.Scanner) map[int]protocol.JSONObject {
		t.Helper()
		out := map[int]protocol.JSONObject{}
		for range 2 {
			if !scanner.Scan() {
				t.Fatal("concurrent physical stdio response absent", scanner.Err())
			}
			var response protocol.JSONObject
			_ = response.UnmarshalJSON(scanner.Bytes())
			var id int
			_ = json.Unmarshal(response.Get("id"), &id)
			out[id], _ = response.Object("result")
			if string(out[id].Get("isError")) == "true" {
				t.Fatal("actual FIFO request failed", id, string(scanner.Bytes()))
			}
		}
		return out
	}
	want := read(source.scanner)
	source.close(t)
	host, _ := ordinaryGoContext(t, home, home, env)
	inputR, inputW, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	defer inputR.Close()
	defer inputW.Close()
	outputR, outputW, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	defer outputR.Close()
	defer outputW.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- mcpcli.Serve(ctx, mcpcli.ServeOptions{Version: "fixture", Input: inputR, Output: outputW, Host: host})
		_ = outputW.Close()
	}()
	for _, p := range packets {
		if _, e := inputW.Write(append(rawMain(p), '\n')); e != nil {
			t.Fatal(e)
		}
	}
	_ = inputW.Close()
	scanner := bufio.NewScanner(outputR)
	scanner.Buffer(make([]byte, 4096), 2*1024*1024)
	got := read(scanner)
	if e := <-done; e != nil {
		t.Fatal("Go actual EOF/Close", e)
	}
	for id, w := range want {
		if !reflect.DeepEqual(mcpOrdinaryResult(t, got[id]), mcpOrdinaryResult(t, w)) {
			t.Fatalf("concurrent stdio id%d Go%v Source%v", id, mcpOrdinaryResult(t, got[id]), mcpOrdinaryResult(t, w))
		}
	}
}
