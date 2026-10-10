package application

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNativeCommandUsesRealSharedHarnessForTextJSONAndSDK(t *testing.T) {
	for _, format := range []string{"text", "json", "stream-json"} {
		t.Run(format, func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { response(w, "cli", "", nil, "SHARED CLI") }))
			defer provider.Close()
			o := nativeSessionOptions(t, provider.URL)
			args := []string{"--print", "--model", "fixture-model", "--tools", "Read,Write,Edit,Bash,Glob,Grep", "--system-prompt", "Actual shared CLI test.", "--thinking", "disabled", "--output-format", format}
			if format == "stream-json" {
				args = append(args, "--verbose", "--include-partial-messages", "--input-format", "stream-json")
			} else {
				args = append(args, "hello")
			}
			command, _, err := ParseCommand(args, o.Version, o.WorkDir, filepath.Join(o.WorkDir, "tmp"))
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			input := ""
			if format == "stream-json" {
				input = `{"type":"user","message":{"role":"user","content":"hello"}}` + "\n"
			}
			code, err := RunCommand(context.Background(), command, o, strings.NewReader(input), &out, nil)
			if err != nil || code != 0 {
				t.Fatalf("code=%d output=%s err=%v", code, out.String(), err)
			}
			if format == "text" && out.String() != "SHARED CLI\n" {
				t.Fatalf("text %q", out.String())
			}
			if format == "json" && (obj(out.Bytes()).String("type") != "result" || obj(out.Bytes()).String("result") != "SHARED CLI") {
				t.Fatalf("JSON %s", out.String())
			}
			if format == "stream-json" && (!strings.Contains(out.String(), `"type":"stream_event"`) || !strings.Contains(out.String(), `"result":"SHARED CLI"`)) {
				t.Fatalf("SDK %s", out.String())
			}
		})
	}
}

func TestNativeCommandRetainsCompletedTextBeforeStreamError(t *testing.T) {
	for _, format := range []string{"text", "json", "stream-json"} {
		t.Run(format, func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				for _, event := range []map[string]any{
					{"type": "message_start", "message": map[string]any{"id": "partial-text", "type": "message", "role": "assistant", "model": "fixture-model", "content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 1, "output_tokens": 0}}},
					{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}},
					{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "COMPLETED_PARTIAL_51D2"}},
					{"type": "content_block_stop", "index": 0},
					{"type": "error", "error": map[string]any{"type": "api_error", "message": "TERMINAL_STREAM_FAILURE_41E7"}},
				} {
					data, _ := json.Marshal(event)
					_, _ = io.WriteString(w, "event: "+event["type"].(string)+"\ndata: "+string(data)+"\n\n")
				}
			}))
			defer provider.Close()
			o := nativeSessionOptions(t, provider.URL)
			o.Environment["CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK"] = "1"
			o.Environment["CLAUDE_STREAM_TRANSIENT_RETRY_MAX"] = "0"
			o.Native.Environment["CLAUDE_STREAM_TRANSIENT_RETRY_MAX"] = "0"
			args := []string{"--bare", "-p", "--model", "fixture-model", "--thinking", "disabled", "--output-format", format}
			if format == "stream-json" {
				args = append(args, "--verbose", "--include-partial-messages")
			}
			args = append(args, "hello")
			command, _, err := ParseCommand(args, o.Version, o.WorkDir, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			code, err := RunCommand(context.Background(), command, o, strings.NewReader(""), &output, nil)
			if err != nil || code != 1 {
				t.Fatalf("code=%d err=%v output=%q", code, err, output.String())
			}
			if format != "json" && strings.Count(output.String(), "COMPLETED_PARTIAL_51D2") < 1 || !strings.Contains(output.String(), "TERMINAL_STREAM_FAILURE_41E7") {
				t.Fatalf("partial or terminal output missing: %q", output.String())
			}
		})
	}
}

func TestNativeCommandRetainsCompletedTextAfterPhysicalSocketResetWithoutResend(t *testing.T) {
	var requests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.ReadAll(r.Body)
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer connection.Close()
		var payload strings.Builder
		for _, event := range []map[string]any{
			{"type": "message_start", "message": map[string]any{"id": "physical-socket-text", "type": "message", "role": "assistant", "model": "fixture-model", "content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 1, "output_tokens": 0}}},
			{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}},
			{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "PHYSICAL_SOCKET_PARTIAL_D2B7"}},
			{"type": "content_block_stop", "index": 0},
		} {
			data, _ := json.Marshal(event)
			_, _ = payload.WriteString("event: " + event["type"].(string) + "\ndata: " + string(data) + "\n\n")
		}
		_, _ = io.WriteString(connection, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n")
		_, _ = fmt.Fprintf(connection, "%x\r\n%s\r\n", payload.Len(), payload.String())
		time.Sleep(30 * time.Millisecond)
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	o.Environment["CLAUDE_STREAM_TRANSIENT_RETRY_MAX"] = "0"
	o.Native.Environment["CLAUDE_STREAM_TRANSIENT_RETRY_MAX"] = "0"
	command, _, err := ParseCommand([]string{"--bare", "-p", "--model", "fixture-model", "--thinking", "disabled", "hello"}, o.Version, o.WorkDir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var output bytes.Buffer
	code, err := RunCommand(ctx, command, o, strings.NewReader(""), &output, nil)
	if err != nil || code != 1 || requests.Load() != 1 || strings.Count(output.String(), "PHYSICAL_SOCKET_PARTIAL_D2B7") != 1 || !strings.Contains(output.String(), "The socket connection was closed unexpectedly.") {
		t.Fatalf("physical partial failure changed: code=%d requests=%d err=%v output=%q", code, requests.Load(), err, output.String())
	}
}

func TestBarePublicCommandUsesOnlyOriginalThreeToolsAndSkipsProjectMemory(t *testing.T) {
	var captured []byte
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		response(w, "bare-public", "", nil, "BARE_SHARED_OK")
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	if e := os.WriteFile(filepath.Join(o.WorkDir, "CLAUDE.md"), []byte("BARE_MUST_NOT_READ_PROJECT_MEMORY_4A81"), 0600); e != nil {
		t.Fatal(e)
	}
	command, _, err := ParseCommand([]string{"--bare", "-p", "--model", "fixture-model", "--thinking", "disabled", "bare task"}, o.Version, o.WorkDir, t.TempDir())
	if err != nil || !command.Bare {
		t.Fatal(command, err)
	}
	var output bytes.Buffer
	code, err := RunCommand(context.Background(), command, o, strings.NewReader(""), &output, nil)
	if err != nil || code != 0 || output.String() != "BARE_SHARED_OK\n" {
		t.Fatal(code, err, output.String())
	}
	var tools []struct {
		Name string `json:"name"`
	}
	if err = json.Unmarshal(obj(captured).Get("tools"), &tools); err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	if !reflect.DeepEqual(names, []string{"Bash", "Edit", "Read"}) || bytes.Contains(captured, []byte("BARE_MUST_NOT_READ_PROJECT_MEMORY_4A81")) {
		t.Fatal("bare tool/memory boundary changed", names)
	}
}

func TestSimpleEnvironmentKeepsBarePresetAndNormalDefaultOmitted(t *testing.T) {
	for _, flag := range []string{"1", "TRUE", " yes ", "on", "0", "false", "off", ""} {
		command, _, err := ParseCommand([]string{"-p", "task"}, "fixture", t.TempDir(), t.TempDir(), map[string]string{"CLAUDE_CODE_SIMPLE": flag})
		if commandTruthy(flag) {
			if err != nil || !reflect.DeepEqual(command.Arguments.Tools, []string{"Bash,Read,Edit"}) || command.Bare {
				t.Fatal(flag, command, err)
			}
		} else if err != nil || len(command.Arguments.Tools) != 0 {
			t.Fatal("normal omitted tools became an explicit subset", flag, err, command.Arguments.Tools)
		}
	}
}

func TestNativeCommandKeepsPermissionIntentAndDirectoryArguments(t *testing.T) {
	for _, row := range []struct {
		flags       []string
		mode        string
		skip, allow bool
		dirs        []string
	}{
		{flags: nil, mode: ""},
		{flags: []string{"--permission-mode", "default"}, mode: "default"},
		{flags: []string{"--dangerously-skip-permissions"}, skip: true},
		{flags: []string{"--allow-dangerously-skip-permissions", "--permission-mode", "plan"}, mode: "plan", allow: true},
		{flags: []string{"--add-dir", "/fixture/one", "/fixture/space directory"}, dirs: []string{"/fixture/one", "/fixture/space directory"}},
	} {
		args := append([]string{"--print", "--tools", "Read"}, row.flags...)
		args = append(args, "--", "actual prompt")
		command, _, err := ParseCommand(args, "1.2.3", t.TempDir(), t.TempDir())
		if err != nil {
			t.Fatal(row.flags, err)
		}
		got := command.Arguments
		if got.PermissionMode != row.mode || got.DangerouslySkipPermissions != row.skip || got.AllowDangerouslySkipPermissions != row.allow || !reflect.DeepEqual(got.AdditionalDirectories, row.dirs) || command.Prompt != "actual prompt" {
			t.Fatalf("original permission intent/variadic args lost: flags=%v args=%+v prompt=%q", row.flags, got, command.Prompt)
		}
	}
}

func TestNativeCommandPreservesHelpAndExplicitUnportedBoundaries(t *testing.T) {
	help, result, err := ParseCommand([]string{"--help"}, "1.2.3", t.TempDir(), t.TempDir())
	if err != nil || result.ExitCode != 0 || !strings.HasPrefix(result.Stdout, "Usage: claude") || help.Print {
		t.Fatal(result, err)
	}
	_, _, err = ParseCommand([]string{"--print", "--thinking", "disabled", "--system-prompt", "test", "--tools", "Read", "--fallback-model", "unported", "hello"}, "1.2.3", t.TempDir(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "--fallback-model") {
		t.Fatal("unported flag silently accepted", err)
	}
}

func TestNativeCommandUsesActualParserOptionMatchesAndKeepsDashValues(t *testing.T) {
	for _, argv := range [][]string{
		{"-p", "--tools", "Read", "--system-prompt", "--fallback-model", "--max-thinking-tokens", "-1", "hello"},
		{"-p", "--tools=Read", "--system-prompt=--fallback-model", "--max-thinking-tokens=-1", "--", "--literal-prompt"},
	} {
		command, result, err := ParseCommand(argv, "1.2.3", t.TempDir(), t.TempDir())
		if err != nil || result.Parsed == nil || command.Arguments.SystemPrompt != "--fallback-model" || command.Arguments.MaxThinkingTokens == nil || *command.Arguments.MaxThinkingTokens != -1 {
			t.Fatalf("parser value classified as unported flag: %v %+v", err, command)
		}
	}
	_, _, err := ParseCommand([]string{"-pc", "--tools", "Read", "hello"}, "1.2.3", t.TempDir(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "--continue") {
		t.Fatal("combined short option bypassed unported business boundary", err)
	}
}

func TestNativeCommandPreservesOriginalNegativeComputerUseOption(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		args := []string{"--print", "--tools", "Read", "--thinking", "disabled"}
		if disabled {
			args = append(args, "--no-computer-use")
		}
		args = append(args, "hello")
		command, _, err := ParseCommand(args, "1.2.3", t.TempDir(), t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if command.Arguments.ComputerUse == nil || *command.Arguments.ComputerUse == disabled {
			t.Fatalf("lost original negative option: disabled=%v value=%v", disabled, command.Arguments.ComputerUse)
		}
	}
}
