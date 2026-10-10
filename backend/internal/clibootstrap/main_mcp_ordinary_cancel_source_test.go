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
	"testing"
	"time"
)

func TestNativeMCPOrdinaryCancelledResponseStillRunsActualSideEffectSource(t *testing.T) {
	home, env := ordinaryFixture(t)
	marker := filepath.Join(home, "cancelled-completed.txt")
	readPath := filepath.Join(home, "read.txt")
	_ = os.WriteFile(readPath, []byte("owned cancellation comparison\n"), 0600)
	packets := []map[string]any{{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "Bash", "arguments": map[string]any{"command": "/bin/sleep 0.3; printf completed > " + marker, "description": "original cancellation side effect", "timeout": 2000}}}, {"jsonrpc": "2.0", "method": "notifications/cancelled", "params": map[string]any{"requestId": 1, "reason": "owned fixture"}}, {"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "Read", "arguments": map[string]any{"file_path": readPath}}}}
	source := startOrdinarySource(t, home, home, env)
	for _, p := range packets {
		_, _ = source.in.Write(append(rawMain(p), '\n'))
	}
	_ = source.in.Close()
	read := func(scanner *bufio.Scanner) map[int]protocol.JSONObject {
		t.Helper()
		out := map[int]protocol.JSONObject{}
		for scanner.Scan() {
			var response protocol.JSONObject
			if e := response.UnmarshalJSON(scanner.Bytes()); e != nil {
				t.Fatal(e)
			}
			var id int
			_ = json.Unmarshal(response.Get("id"), &id)
			out[id], _ = response.Object("result")
		}
		if e := scanner.Err(); e != nil {
			t.Fatal(e)
		}
		return out
	}
	want := read(source.scanner)
	source.close(t)
	if len(want) != 1 || want[2].Get("content") == nil {
		t.Fatalf("actual Source cancellation shape %s", rawMain(want))
	}
	if b, e := os.ReadFile(marker); e != nil || string(b) != "completed" {
		t.Fatal("Source cancelled tool did not finish original side effect", e, string(b))
	}
	_ = os.Remove(marker)
	host, _ := ordinaryGoContext(t, home, home, env)
	inR, inW, _ := os.Pipe()
	outR, outW, _ := os.Pipe()
	defer inR.Close()
	defer outR.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- mcpcli.Serve(ctx, mcpcli.ServeOptions{Version: "fixture", Input: inR, Output: outW, Host: host})
		_ = outW.Close()
	}()
	for _, p := range packets {
		_, _ = inW.Write(append(rawMain(p), '\n'))
	}
	_ = inW.Close()
	scanner := bufio.NewScanner(outR)
	scanner.Buffer(make([]byte, 4096), 2*1024*1024)
	got := read(scanner)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if len(got) != 1 || !reflect.DeepEqual(mcpOrdinaryResult(t, got[2]), mcpOrdinaryResult(t, want[2])) {
		t.Errorf("cancelled responses differ GoIDs=%v SourceIDs=%v", mcpResponseIDs(got), mcpResponseIDs(want))
	}
	if b, e := os.ReadFile(marker); e != nil || string(b) != "completed" {
		t.Fatal("Go cancelled tool side effect missing", e, string(b))
	}
}
func mcpResponseIDs(in map[int]protocol.JSONObject) []int {
	out := []int{}
	for id := range in {
		out = append(out, id)
	}
	return out
}
