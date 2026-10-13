package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestActualSourceAndPublicCLIAuthStatusInitializeAndOptOut(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	binary := filepath.Join(root, "actual-auth-status-cli")
	build := exec.CommandContext(ctx, "go", "build", "-p=4", "-tags=native_process_fixture", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	fixture, _ := filepath.Abs("../../testdata/native-sdk-initialize-options.fixture.ts")
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jsonSchemaCLIResponse(w, "normal-resume-response", "", nil, "PUBLIC_RESUME_NORMAL_OK")
	}))
	defer peer.Close()
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "opt out", true: "initial and repeated initialize"}[enabled], func(t *testing.T) {
			sourceHome := resumeErrorHome(t, root, "source-auth"+map[bool]string{false: "off", true: "on"}[enabled])
			goHome := resumeErrorHome(t, root, "go-auth"+map[bool]string{false: "off", true: "on"}[enabled])
			args := []string{"--bare", "--print", "--session-id", resumeErrorInitialSID, "--model", "fixture-model", "--no-computer-use", "--system-prompt", "Private auth status", "--tools", "", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose"}
			if enabled {
				args = append(args, "--enable-auth-status")
			}
			input, _ := json.Marshal(map[string]any{"authStatus": enabled, "sessionId": resumeErrorInitialSID, "initial": "fixture-model", "override": "fixture-model", "request": map[string]any{"subtype": "initialize"}})
			source := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			source.Dir = sourceHome
			source.Env = append(resumeErrorEnv(sourceHome, root, "http://127.0.0.1:1"), "CC_HAHA_SDK_OPTIONS_FIXTURE=1", "CLAUDE_CODE_ENTRYPOINT=sdk-cli", "CLAUDE_CODE_SIMPLE=1", "PATH=/usr/bin:/bin")
			actual := exec.CommandContext(ctx, binary, args...)
			actual.Dir = goHome
			actual.Env = resumeErrorEnv(goHome, root, peer.URL)
			source.Stdin = bytes.NewReader(input)
			sc, so, se := resumeErrorRun(t, source, false)
			var original struct {
				Output       []json.RawMessage
				Capabilities struct{ Blocked []string }
			}
			if err := json.Unmarshal(so, &original); err != nil || len(original.Capabilities.Blocked) != 0 {
				t.Fatal("actual initialize Source receipt", err, string(so), string(se))
			}
			so = bytes.Join(func() [][]byte {
				rows := [][]byte{}
				for _, row := range original.Output {
					rows = append(rows, row)
				}
				return rows
			}(), []byte{'\n'})
			gc, goOutput, goError := runAuthStatusCLI(t, actual)
			if sc != 0 || gc != 0 || len(se) != 0 || len(goError) != 0 {
				t.Fatal(sc, gc, string(se), string(goError), string(so), string(goOutput))
			}
			projection := func(output []byte) []map[string]any {
				auth := []map[string]any{}
				initialized := false
				for _, line := range bytes.Split(bytes.TrimSpace(output), []byte{'\n'}) {
					var row map[string]any
					if err := json.Unmarshal(line, &row); err != nil {
						t.Fatal(err, string(output))
					}
					if row["type"] == "control_response" {
						response, _ := row["response"].(map[string]any)
						if response["request_id"] == "actual-init" && response["subtype"] == "success" {
							initialized = true
						}
					}
					if row["type"] == "auth_status" {
						if !initialized {
							t.Fatal("auth status preceded successful SDK initialize", string(output))
						}
						if row["uuid"] == nil {
							t.Fatal("missing source UUID", row)
						}
						delete(row, "uuid")
						auth = append(auth, row)
					}
				}
				return auth
			}
			want, got := projection(so), projection(goOutput)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Source%s Go%s", so, goOutput)
			}
			if enabled && len(got) != 1 || !enabled && len(got) != 0 {
				t.Fatal("wrong original opt-in/duplicate initialization cardinality", got)
			}
		})
	}
}

type authStatusProcessWriter struct {
	mu                  sync.Mutex
	all, pending        bytes.Buffer
	initialized, result chan struct{}
}

func (writer *authStatusProcessWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.all.Write(data)
	writer.pending.Write(data)
	for {
		line, err := writer.pending.ReadBytes('\n')
		if err != nil {
			writer.pending.Write(line)
			break
		}
		var row map[string]any
		if json.Unmarshal(line, &row) != nil {
			continue
		}
		if row["type"] == "control_response" {
			response, _ := row["response"].(map[string]any)
			if response["request_id"] == "actual-init" {
				select {
				case writer.initialized <- struct{}{}:
				default:
				}
			}
		}
		if row["type"] == "result" {
			select {
			case writer.result <- struct{}{}:
			default:
			}
		}
	}
	return len(data), nil
}
func runAuthStatusCLI(t *testing.T, cmd *exec.Cmd) (int, []byte, []byte) {
	t.Helper()
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output := &authStatusProcessWriter{initialized: make(chan struct{}, 1), result: make(chan struct{}, 1)}
	var stderr bytes.Buffer
	cmd.Stdout = output
	cmd.Stderr = &stderr
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	wait := func(ready chan struct{}) {
		select {
		case <-ready:
		case err := <-done:
			t.Fatalf("process exited before physical SDK phase: %v", err)
		case <-time.After(12 * time.Second):
			_ = cmd.Process.Kill()
			_ = input.Close()
			err := <-done
			output.mu.Lock()
			defer output.mu.Unlock()
			t.Fatalf("physical SDK phase did not finish: %v output%s stderr%s", err, output.all.Bytes(), stderr.Bytes())
		}
	}
	if _, err = io.WriteString(input, `{"type":"control_request","request_id":"actual-init","request":{"subtype":"initialize"}}`+"\n"); err != nil {
		t.Fatal(err)
	}
	wait(output.initialized)
	for _, line := range []string{`{"type":"control_request","request_id":"repeated-init","request":{"subtype":"initialize"}}`, `{"type":"user","message":{"role":"user","content":"Actual authentication status ASK"}}`} {
		if _, err = io.WriteString(input, line+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	wait(output.result)
	_ = input.Close()
	err = <-done
	code := 0
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else {
			t.Fatal(fmt.Errorf("CLI wait: %w", err))
		}
	}
	output.mu.Lock()
	defer output.mu.Unlock()
	return code, bytes.Clone(output.all.Bytes()), bytes.Clone(stderr.Bytes())
}
