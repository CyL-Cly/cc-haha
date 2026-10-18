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
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPublicMainPrintInheritedFDInitializesAndReusesPipeAcrossAsks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("inherited Unix descriptor regression")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	binary := filepath.Join(root, "public-print-fd-cli")
	buildArgs := []string{"build", "-mod=readonly", "-p=4", "-race", "-tags=native_process_fixture", "-o", binary}
	// Reuse the public end-session harness's explicit child build admission.
	// Ordinary physical verification leaves the diagnostic overlay unset.
	if overlay := os.Getenv("CC_HAHA_PUBLIC_CLI_BUILD_OVERLAY"); overlay != "" {
		info, statErr := os.Lstat(overlay)
		if !filepath.IsAbs(overlay) || statErr != nil || !info.Mode().IsRegular() {
			t.Fatal("explicit actual CLI build overlay must be an absolute regular file", statErr)
		}
		if os.Getenv("GOCOVERDIR") != "" {
			t.Fatal("explicit overlay CLI diagnostic requires an uninstrumented build; use normal physical integration for coverage")
		}
		buildArgs = append(buildArgs, "-overlay="+overlay)
	}
	buildArgs = append(buildArgs, ".")
	build := exec.CommandContext(ctx, "go", buildArgs...)
	capturePublicGoCoverage(t, build)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	for _, row := range []struct {
		name, slot, key, token, tokenSource string
		extra                               []string
		bare                                bool
	}{
		{"api-pipe", "CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR", "fake-public-pipe", "", "none", nil, false},
		{"oauth-pipe", "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR", "", "Bearer fake-public-pipe", "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR", nil, false},
		{"print-env-before-api-fd", "CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR", "fake-public-env", "", "none", []string{"ANTHROPIC_API_KEY=fake-public-env"}, false},
		{"oauth-env-before-fd", "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR", "", "Bearer fake-public-env-token", "CLAUDE_CODE_OAUTH_TOKEN", []string{"CLAUDE_CODE_OAUTH_TOKEN=fake-public-env-token"}, false},
		{"bare-ignores-fd", "CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR", "fake-public-bare", "", "none", []string{"ANTHROPIC_API_KEY=fake-public-bare"}, true},
	} {
		t.Run(row.name, func(t *testing.T) {
			var mu sync.Mutex
			var headers []http.Header
			peer := httptest.NewServer(nativeCLIModelHTTPHandler(t, func(w http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodPost || request.URL.Path != "/v1/messages" {
					t.Error("unexpected model request", request.Method, request.URL.Path)
				}
				_, _ = io.Copy(io.Discard, request.Body)
				mu.Lock()
				headers = append(headers, request.Header.Clone())
				mu.Unlock()
				jsonSchemaCLIResponse(w, "public-fd-response", "", nil, "PUBLIC_PRINT_FD_OK")
			}))
			defer peer.Close()
			home := resumeErrorHome(t, root, row.name)
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			if _, err = writer.WriteString(" \ufefffake-public-pipe\u00a0\n"); err != nil {
				_ = writer.Close()
				t.Fatal(err)
			}
			if err = writer.Close(); err != nil {
				t.Fatal(err)
			}
			args := []string{"--print", "--model", "fixture-model", "--thinking", "disabled", "--no-computer-use", "--system-prompt", "Private inherited FD public ASK", "--tools", "", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose"}
			if row.bare {
				args = append([]string{"--bare"}, args...)
			}
			command := exec.CommandContext(ctx, binary, args...)
			command.Dir = home
			for _, item := range resumeErrorEnv(home, root, peer.URL) {
				if !strings.HasPrefix(item, "ANTHROPIC_API_KEY=") {
					command.Env = append(command.Env, item)
				}
			}
			command.Env = append(command.Env, row.slot+"=  +3tail")
			command.Env = append(command.Env, row.extra...)
			command.ExtraFiles = []*os.File{reader}
			input, err := command.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			output := &authStatusProcessWriter{initialized: make(chan struct{}, 1), result: make(chan struct{}, 1)}
			var stderr bytes.Buffer
			command.Stdout, command.Stderr = output, &stderr
			capturePublicGoCoverage(t, command)
			if err = command.Start(); err != nil {
				_ = input.Close()
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- command.Wait() }()
			waited := false
			defer func() {
				_ = input.Close()
				if !waited {
					_ = command.Process.Kill()
					<-done
				}
			}()
			wait := func(ready <-chan struct{}, phase string) {
				t.Helper()
				select {
				case <-ready:
				case failure := <-done:
					waited = true
					t.Fatalf("public FD process exited before %s: %v stderr=%s", phase, failure, stderr.String())
				case <-ctx.Done():
					t.Fatalf("public FD process did not reach %s: %v", phase, ctx.Err())
				}
			}
			if _, err = io.WriteString(input, `{"type":"control_request","request_id":"actual-init","request":{"subtype":"initialize"}}`+"\n"); err != nil {
				t.Fatal(err)
			}
			wait(output.initialized, "initialize")
			for _, prompt := range []string{"FIRST_FD_ASK", "SECOND_FD_ASK"} {
				line, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": prompt}})
				if _, err = input.Write(append(line, '\n')); err != nil {
					t.Fatal(err)
				}
				wait(output.result, prompt)
			}
			_ = input.Close()
			err = <-done
			waited = true
			if err != nil {
				t.Fatalf("public FD exit: %v stderr=%s", err, stderr.String())
			}
			output.mu.Lock()
			raw := bytes.Clone(output.all.Bytes())
			output.mu.Unlock()
			if bytes.Contains(raw, []byte("fake-public-")) || strings.Contains(stderr.String(), "fake-public-") {
				t.Fatal("public output exposed fake credentials")
			}
			mu.Lock()
			observed := append([]http.Header(nil), headers...)
			mu.Unlock()
			if len(observed) != 2 {
				t.Fatal("actual two-ASK request count", len(observed), string(raw), stderr.String())
			}
			for _, header := range observed {
				if header.Get("X-Api-Key") != row.key || header.Get("Authorization") != row.token {
					t.Fatal("inherited credential did not reach both real model requests", header)
				}
			}
			initialized, systemInits, results := false, 0, 0
			for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte{'\n'}) {
				var frame map[string]any
				if err := json.Unmarshal(line, &frame); err != nil {
					t.Fatal(err, string(raw))
				}
				if frame["type"] == "control_response" {
					response, _ := frame["response"].(map[string]any)
					if response["request_id"] == "actual-init" {
						body, _ := response["response"].(map[string]any)
						account, _ := body["account"].(map[string]any)
						if response["subtype"] != "success" || account["tokenSource"] != row.tokenSource {
							t.Fatal("real initialize did not consume the FD account", response)
						}
						initialized = true
					}
				}
				if frame["type"] == "system" && frame["subtype"] == "init" {
					systemInits++
					want := "none"
					if row.key != "" {
						want = "ANTHROPIC_API_KEY"
					}
					if frame["apiKeySource"] != want {
						t.Fatal("real init credential category", frame["apiKeySource"], want)
					}
				}
				if frame["type"] == "result" {
					results++
					if frame["is_error"] == true || frame["subtype"] != "success" || !strings.Contains(fmt.Sprint(frame["result"]), "PUBLIC_PRINT_FD_OK") {
						t.Fatal("actual FD ASK failed", frame)
					}
				}
			}
			if !initialized || systemInits != 2 || results != 2 {
				t.Fatal("public FD consumer cardinality", initialized, systemInits, results, string(raw))
			}
		})
	}
}
