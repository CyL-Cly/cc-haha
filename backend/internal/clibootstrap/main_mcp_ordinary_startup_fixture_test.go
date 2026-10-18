package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

type ordinaryStartupSourceReceipt struct {
	Code       int
	Prefetched []struct {
		Command            string
		Args               []string
		Timeout, PID, Code int
		Signal             *string
		Stdout, Stderr     string
	}
	SyncCalls []struct {
		Kind     string
		Admitted bool
		Error    string
		Result   struct {
			Status         int
			Signal         *string
			Stdout, Stderr struct {
				IsBuffer bool
				Bytes    int
				UTF8     string
			}
		}
	}
	Caps struct{ Blocked, SecureReads []string }
}

// The ordinary tool fixture intentionally starts below main. Its mocked store
// read count is logical; this separate actual main->serve call observes the
// same physical startup producers as NewMainCommand, without changing that
// tool fixture or replacing the original SecureStorage/cache/getters.
func ordinaryStartupSource(t *testing.T, ctx context.Context, home string, environment map[string]string) ordinaryStartupSourceReceipt {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual Source Bun required")
	}
	fixture, err := filepath.Abs("../../testdata/native-mcp-ordinary-startup.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", profile, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{}
	for name, value := range environment {
		command.Env = append(command.Env, name+"="+value)
	}
	command.Env = append(command.Env, "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT="+filepath.Dir(home), "CC_HAHA_MCP_ORDINARY_STARTUP_SOURCE=1")
	command.Stdin = strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/list\",\"params\":{}}\n")
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("whole ordinary Source startup/serve failed: %v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("whole ordinary Source startup/serve stderr: %q", stderr.String())
	}
	var response struct {
		ID     int
		Result struct{ Tools []json.RawMessage }
		Error  json.RawMessage
	}
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &response); err != nil || response.ID != 1 || len(response.Error) != 0 {
		t.Fatalf("whole original tools/list did not complete: %v %s", err, stdout.Bytes())
	}
	wantTools := 20
	if environment["ENABLE_TOOL_SEARCH"] == "true" {
		wantTools = 21
	}
	if len(response.Result.Tools) != wantTools {
		t.Fatalf("whole original tools/list count %d, want %d", len(response.Result.Tools), wantTools)
	}
	data, err := os.ReadFile(filepath.Join(home, "mcp-ordinary-startup-source.json"))
	var receipt ordinaryStartupSourceReceipt
	if err != nil || json.Unmarshal(data, &receipt) != nil || receipt.Code != 0 || len(receipt.Caps.Blocked) != 0 || len(receipt.Caps.SecureReads) != 0 {
		t.Fatalf("whole original credential receipt missing/guarded: %v %s", err, data)
	}
	return receipt
}

func checkOrdinaryPhysicalSecureReadReceipt(t *testing.T, source ordinaryStartupSourceReceipt, native []securestorage.Command) {
	t.Helper()
	want := map[string]int{}
	for _, call := range source.Prefetched {
		if call.Command != "security" || call.PID <= 0 || len(call.Args) != 6 || call.Args[0] != "find-generic-password" || call.Args[1] != "-a" || call.Args[2] != "owned-mcp-fixture" || call.Args[3] != "-w" || call.Args[4] != "-s" || !strings.HasPrefix(call.Args[5], "Claude Code") || call.Timeout != 10000 || call.Code != 44 || call.Signal != nil || call.Stdout != "" || call.Stderr != "" {
			t.Fatalf("Source physical prefetch was not the original owned absent process: %+v", call)
		}
		kind := "async-legacy"
		if strings.Contains(call.Args[5], "-credentials") {
			kind = "async-oauth"
		}
		want[kind]++
	}
	if len(source.Prefetched) != 2 || want["async-oauth"] != 1 || want["async-legacy"] != 1 {
		t.Fatalf("ordinary Source lost either actual startup arm: %+v", source.Prefetched)
	}
	for _, call := range source.SyncCalls {
		if !call.Admitted || call.Error != "" || call.Kind != "legacy" && call.Kind != "oauth" || call.Result.Status != 44 || call.Result.Signal != nil || !call.Result.Stdout.IsBuffer || call.Result.Stdout.Bytes != 0 || call.Result.Stdout.UTF8 != "" || !call.Result.Stderr.IsBuffer || call.Result.Stderr.Bytes != 0 || call.Result.Stderr.UTF8 != "" {
			t.Fatalf("Source physical synchronous receipt was not the original owned absent process: %+v", call)
		}
		want["sync-"+call.Kind]++
	}
	got := map[string]int{}
	for _, call := range native {
		if len(call.Args) != 6 || call.Args[0] != "find-generic-password" || call.Args[1] != "-a" || call.Args[2] != "owned-mcp-fixture" || call.Args[3] != "-w" || call.Args[4] != "-s" || call.Input != "" {
			t.Fatalf("Native physical store receipt was not the original readonly operation: %+v", call)
		}
		kind := "legacy"
		if strings.Contains(call.Args[5], "-credentials") {
			kind = "oauth"
		}
		if call.ShellScript == "" {
			kind = "async-" + kind
		} else {
			kind = "sync-" + kind
		}
		got[kind]++
	}
	if !reflect.DeepEqual(got, want) || len(native) != len(source.Prefetched)+len(source.SyncCalls) {
		t.Fatalf("whole original startup+serve physical readonly producers differ: Source=%v Native=%v", want, got)
	}
	t.Logf("whole original startup+serve physical readonly producers Source=%v Native=%v; direct server logical store receipt is separate", want, got)
}
