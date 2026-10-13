package mcpheaders

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestDynamicHeadersAdmissionEnvironmentMergeAndFailuresAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	type row struct {
		Config         map[string]any `json:"config"`
		NonInteractive bool           `json:"nonInteractive"`
		Trusted        bool           `json:"trusted"`
		Throw          bool           `json:"throw"`
		Output         map[string]any `json:"output"`
	}
	rows := []row{}
	for _, scope := range []string{"user", "project", "local", "dynamic"} {
		for _, nonInteractive := range []bool{false, true} {
			for _, trusted := range []bool{false, true} {
				for _, out := range []string{`{"Authorization":"dynamic","X-Peer":"value"}`, `[]`, `null`, `{"bad":1}`, `{"bad":null}`, `{"bad":{}}`, `{"good":"before","bad":true}`, "", `invalid`, " \n{\"X-Peer\":\" 中文 😀 \"}\n"} {
					rows = append(rows, row{map[string]any{"type": "ws", "scope": scope, "url": "ws://loopback.invalid", "headersHelper": "actual private helper", "headers": map[string]string{"Authorization": "static", "X-Static": "retained"}}, nonInteractive, trusted, false, map[string]any{"code": 0, "stdout": out}})
				}
			}
		}
	}
	rows = append(rows, row{map[string]any{"scope": "user", "url": "ws://loopback.invalid", "headersHelper": "actual private helper"}, true, false, true, map[string]any{"code": 1, "stdout": "not run"}})
	env := map[string]string{"PATH": "isolated-path", "HOME": t.TempDir(), "KEEP": "untouched"}
	home := t.TempDir()
	input, _ := json.Marshal(map[string]any{"homeDir": home, "environment": env, "cases": rows})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-mcp-headers.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(input)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	var expected []struct {
		Headers  map[string]string `json:"headers"`
		Requests []json.RawMessage `json:"requests"`
	}
	if e = json.Unmarshal(out, &expected); e != nil {
		t.Fatal(e, string(out))
	}
	for i, r := range rows {
		data, _ := json.Marshal(r.Config)
		var cfg protocol.JSONObject
		_ = cfg.UnmarshalJSON(data)
		calls := []Request{}
		actual := Resolve(ctx, "physical-header-peer", cfg, Options{NonInteractive: r.NonInteractive, TrustAccepted: r.Trusted, Environment: env, CWD: home, Run: func(_ context.Context, request Request) (ProcessResult, error) {
			calls = append(calls, request)
			if r.Throw {
				return ProcessResult{}, errors.New("native process fixture failure")
			}
			return ProcessResult{Code: r.Output["code"].(int), Stdout: r.Output["stdout"].(string)}, nil
		}})
		if !reflect.DeepEqual(actual, expected[i].Headers) || len(calls) != len(expected[i].Requests) {
			t.Fatalf("row%d actual%#v calls%d Source%#v calls%d", i, actual, len(calls), expected[i].Headers, len(expected[i].Requests))
		}
		for _, call := range calls {
			if call.Timeout != 10*time.Second || call.Command != "actual private helper" || call.Environment["KEEP"] != "untouched" || call.Environment["CLAUDE_CODE_MCP_SERVER_NAME"] != "physical-header-peer" || call.Environment["CLAUDE_CODE_MCP_SERVER_URL"] != "ws://loopback.invalid" {
				t.Fatal("actual Source exec input differs", call)
			}
		}
		if env["CLAUDE_CODE_MCP_SERVER_NAME"] != "" {
			t.Fatal("mutated shared process environment")
		}
	}
}

func TestNativeDynamicHeadersExecutesRealIsolatedProcessAndFailureLeavesStatic(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("actual Unix shell physical peer, Win crosscompile separately")
	}
	home := t.TempDir()
	fixture := filepath.Join(home, "headers.sh")
	if e := os.WriteFile(fixture, []byte("printf '{\"X-Peer\":\"%s\",\"X-URL\":\"%s\",\"Authorization\":\"private-native\"}' \"$CLAUDE_CODE_MCP_SERVER_NAME\" \"$CLAUDE_CODE_MCP_SERVER_URL\"\n"), 0700); e != nil {
		t.Fatal(e)
	}
	cfg := protocol.NewJSONObject()
	data, _ := json.Marshal(map[string]any{"type": "ws", "scope": "user", "url": "ws://127.0.0.1/physical", "headersHelper": "/bin/sh '" + fixture + "'", "headers": map[string]string{"X-Static": "retained"}})
	_ = cfg.UnmarshalJSON(data)
	options := Options{Environment: map[string]string{"HOME": home, "PATH": "/usr/bin:/bin"}, CWD: home, Run: NativeRun}
	actual := Resolve(context.Background(), "real-native-peer", cfg, options)
	if actual["X-Peer"] != "real-native-peer" || actual["X-URL"] != "ws://127.0.0.1/physical" || actual["Authorization"] != "private-native" {
		t.Fatal(actual)
	}
	cfg.Set("headersHelper", []byte(`"exit 7"`))
	actual = Resolve(context.Background(), "real-native-peer", cfg, options)
	if len(actual) != 1 || actual["X-Static"] != "retained" {
		t.Fatal("failed native helper did not preserve static headers", actual)
	}
}
