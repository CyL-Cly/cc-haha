//go:build darwin

package sdkhost_test

import (
	"bufio"
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
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type workloadReturnObservation struct {
	Dequeues int  `json:"dequeues"`
	Queued   bool `json:"queued"`
}

type workloadReturnReceipt struct {
	Code         int                         `json:"code"`
	Events       []string                    `json:"events"`
	Observations []workloadReturnObservation `json:"observations"`
	Identities   []bool                      `json:"identities"`
	Dequeues     int                         `json:"dequeues"`
	Enqueues     int                         `json:"enqueues"`
	Capabilities struct{ Blocked []string }  `json:"capabilities"`
}

// This is one actual print/Host.RunStream consumer, not an isolated Promise
// counter. The second SDK line crosses a real named pipe after the first POST;
// the server keeps that first response held until the actual queue admits it.
func TestActualSDKWorkloadCallbackReturnAwaitsBeforeNextDequeuedCommand(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	var original workloadReturnReceipt
	for _, profile := range []string{"source", "native"} {
		if !t.Run(profile, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			allocation, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			home := filepath.Join(allocation, "home")
			config := filepath.Join(home, ".claude")
			for _, path := range []string{home, config, filepath.Join(home, "bin"), filepath.Join(home, "managed")} {
				if err := os.MkdirAll(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			fifo := func(name string) *os.File {
				t.Helper()
				path := filepath.Join(home, name)
				if err := syscall.Mkfifo(path, 0600); err != nil {
					t.Fatal(err)
				}
				file, err := os.OpenFile(path, os.O_RDWR, 0600)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = file.Close() })
				return file
			}
			writer := fifo("sdk-workload-input.fifo")
			enqueuedPipe := fifo("sdk-workload-enqueued.fifo")
			firstPOST, secondEnqueued := make(chan struct{}), make(chan struct{})
			var firstOnce, enqueueOnce sync.Once
			var posts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Errorf("unexpected model method: %s", r.Method)
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				body, err := io.ReadAll(r.Body)
				index := posts.Add(1)
				marker := fmt.Sprintf("WORKLOAD_RETURN_%d", index)
				if err != nil || !bytes.Contains(body, []byte(marker)) || index > 2 {
					t.Errorf("actual model POST %d: %v %s", index, err, body)
				}
				if index == 1 {
					firstOnce.Do(func() { close(firstPOST) })
					select {
					case <-secondEnqueued:
					case <-r.Context().Done():
						return
					case <-ctx.Done():
						return
					}
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprintf(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"workload-%d\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"fixture-model\",\"content\":[],\"usage\":{\"input_tokens\":2,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"WORKLOAD_RETURN_OK\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", index)
			}))
			defer func() { cancel(); server.Close() }()
			feedDone := make(chan error, 1)
			go func() {
				write := func(index int) error {
					_, err := fmt.Fprintf(writer, "{\"type\":\"user\",\"uuid\":\"workload-user-%d\",\"message\":{\"role\":\"user\",\"content\":\"WORKLOAD_RETURN_%d\"}}\n", index, index)
					return err
				}
				defer writer.Close()
				if err := write(1); err != nil {
					feedDone <- err
					return
				}
				select {
				case <-firstPOST:
					feedDone <- write(2)
				case <-ctx.Done():
					feedDone <- ctx.Err()
				}
			}()
			environment := map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": config, "TMPDIR": home, "CC_HAHA_FIXTURE_TEMP_ROOT": allocation, "BUN_OPTIONS": "--no-env-file", "NODE_ENV": "production", "TZ": "UTC", "ANTHROPIC_API_KEY": "offline-fixture-key", "ANTHROPIC_BASE_URL": server.URL, "CLAUDE_CODE_SIMPLE": "1", "DISABLE_TELEMETRY": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY": "1", "CLAUDE_CODE_DISABLE_FAST_MODE": "1", "CLAUDE_CODE_MAX_RETRIES": "0", "CLAUDE_STREAM_TRANSIENT_RETRY_MAX": "0", "USE_BUILTIN_RIPGREP": "false", "PATH": filepath.Join(home, "bin")}
			if profile == "source" {
				enqueuedRead := make(chan error, 1)
				go func() {
					line, err := bufio.NewReader(enqueuedPipe).ReadString('\n')
					if err == nil && line != "second command actually enqueued\n" {
						err = fmt.Errorf("unexpected queue admission receipt: %q", line)
					}
					if err == nil {
						enqueueOnce.Do(func() { close(secondEnqueued) })
					}
					enqueuedRead <- err
				}()
				fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-sdk-workload-return.fixture.ts")
				profilePath, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
				command := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", profilePath, bun, "--no-env-file", "--feature=TRANSCRIPT_CLASSIFIER", fixture)
				command.Dir = home
				for key, value := range environment {
					command.Env = append(command.Env, key+"="+value)
				}
				command.Env = append(command.Env, "CC_HAHA_SDK_WORKLOAD_RETURN=1")
				command.Stdin = strings.NewReader(source(map[string]string{"origin": server.URL}))
				var stderr bytes.Buffer
				command.Stderr = &stderr
				stdout, err := command.Output()
				_ = enqueuedPipe.Close()
				if err != nil || stderr.Len() != 0 {
					t.Fatalf("actual runHeadless: %v stderr=%s stdout=%s", err, stderr.String(), stdout)
				}
				if bytes.Count(stdout, []byte(`"type":"result"`)) != 2 {
					t.Fatalf("actual original two ASK results: %s", stdout)
				}
				if err := <-enqueuedRead; err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(filepath.Join(home, "sdk-workload-return-result.json"))
				if err != nil || json.Unmarshal(data, &original) != nil {
					t.Fatalf("actual Source receipt: %v %s", err, data)
				}
				if original.Code != 0 || original.Dequeues != 2 || original.Enqueues != 2 || len(original.Capabilities.Blocked) != 0 || !reflect.DeepEqual(original.Identities, []bool{true, true}) || !reflect.DeepEqual(original.Events, []string{"dequeue", "callback-returned", "dequeue", "callback-returned"}) || !reflect.DeepEqual(original.Observations, []workloadReturnObservation{{1, true}, {2, false}}) {
					t.Fatalf("original callback Promise/queue FIFO: %s", data)
				}
			} else {
				reader, err := os.Open(filepath.Join(home, "sdk-workload-input.fifo"))
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Close()
				native, err := newIsolatedProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: environment, CWD: home, Platform: "darwin", ManagedRoot: filepath.Join(home, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
				if err != nil {
					t.Fatal(err)
				}
				var process *clibootstrap.MainPrint
				var enqueues, dequeues, completions int
				var observed []workloadReturnObservation
				var events []string
				var output, stderr bytes.Buffer
				disabled := false
				process, err = clibootstrap.NewMainPrint(clibootstrap.MainPrintOptions{Native: native, PID: os.Getpid(), Platform: "darwin", Version: "fixture", Input: reader, Output: &output, Error: &stderr, Arguments: clibootstrap.PrintArguments{ComputerUse: &disabled, Model: "fixture-model", Tools: []string{""}, MaxTurns: 1, Replay: true, PermissionMode: "default", SystemPrompt: "original SDK workload return system", HasCustomSystemPrompt: true, SessionPersistenceDisabled: true, Sources: clibootstrap.DefaultState()}, OnControllerPhase: func(phase string) {
					if phase == "enqueue" {
						enqueues++
						if enqueues == 2 {
							enqueueOnce.Do(func() { close(secondEnqueued) })
						}
					}
					if phase == "dequeue" {
						dequeues++
						events = append(events, "dequeue")
					}
				}, OnSDKPhase: func(phase string) {
					if phase != "lifecycle:completed" {
						return
					}
					completions++
					// This existing lifecycle phase precedes the actual completion
					// future's Resolve. Its queued observer probes that settlement
					// boundary; it does not assert JS Promise identity or resolve
					// or replace any producer/iterator promise.
					_, err := process.Owner.Microtask(cooperative.Read, func(scope *cooperative.Scope) error {
						queued, err := process.Controller.HasMainThreadQueuedCommandOnOwner(scope)
						if err != nil {
							return err
						}
						observed = append(observed, workloadReturnObservation{dequeues, queued})
						events = append(events, "callback-returned")
						return nil
					})
					if err != nil {
						t.Error(err)
					}
				}})
				if err != nil {
					t.Fatal(err)
				}
				var closeOnce sync.Once
				var closeError error
				closeProcess := func() error {
					closeOnce.Do(func() {
						closeContext, stopClose := context.WithTimeout(context.Background(), 3*time.Second)
						defer stopClose()
						closeError = process.ShutdownSession(closeContext)
					})
					return closeError
				}
				t.Cleanup(func() {
					_ = writer.Close()
					_ = reader.Close()
					if err := closeProcess(); err != nil {
						t.Error("bounded native SDK cleanup", err)
					}
				})
				code, runErr := process.Run(ctx)
				_ = writer.Close()
				_ = reader.Close()
				closeErr := closeProcess()
				if runErr != nil || closeErr != nil || code != 0 || stderr.Len() != 0 {
					t.Fatalf("actual SDK Run/Close: code=%d run=%v close=%v stderr=%s", code, runErr, closeErr, stderr.String())
				}
				if enqueues != 2 || dequeues != 2 || completions != 2 || bytes.Count(output.Bytes(), []byte(`"type":"result"`)) != 2 || !reflect.DeepEqual(observed, original.Observations) || !reflect.DeepEqual(events, original.Events) {
					t.Fatalf("actual callback return crossed next dequeue: observations=%v original=%v events=%v original=%v enqueues=%d dequeues=%d completions=%d", observed, original.Observations, events, original.Events, enqueues, dequeues, completions)
				}
			}
			if err := <-feedDone; err != nil || posts.Load() != 2 {
				t.Fatal("real FIFO feed/two model POSTs", err, posts.Load())
			}
		}) {
			return
		}
	}
}
