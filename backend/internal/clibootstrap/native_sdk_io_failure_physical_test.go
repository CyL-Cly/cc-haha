package clibootstrap

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type sdkPhysicalFaultReader struct{ fault error }

func (r sdkPhysicalFaultReader) Read([]byte) (int, error) { return 0, r.fault }

func TestActualSDKTransportFaultClosesNativeRuntimeAndPreservesAdmittedWork(t *testing.T) {
	for _, scenario := range []string{"before-input", "after-completed-read", "assistant-consumer"} {
		t.Run(scenario, func(t *testing.T) {
			f := newNativePluginFixture(t)
			f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
			f.n.Environment["ANTHROPIC_API_KEY"] = "fake-physical-sdk-io"
			f.n.Environment["CLAUDE_CODE_ENTRYPOINT"] = "sdk-cli"
			f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
			f.n.Environment["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
			readPath, writePath := filepath.Join(f.cwd, "physical-read.txt"), filepath.Join(f.cwd, "must-not-write.txt")
			const marker = "PHYSICAL_SDK_READ_BEFORE_INPUT_FAULT"
			f.write(t, readPath, marker)
			fault := errors.New("injected actual SDK transport fault: " + scenario)
			var calls atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				n := calls.Add(1)
				if scenario == "before-input" {
					t.Error("failed SDK input opened a model request")
					http.Error(w, "must not be called", http.StatusBadRequest)
					return
				}
				if n == 1 {
					if scenario == "assistant-consumer" {
						emitNativeTaskTool(w, "io-output-write", "Write", map[string]any{"file_path": writePath, "content": "must-not-execute-after-consumer-fault"})
					} else {
						emitNativeTaskTool(w, "io-input-read", "Read", map[string]any{"file_path": readPath})
					}
					return
				}
				if n != 2 || scenario != "after-completed-read" || !bytes.Contains(data, []byte(marker)) {
					t.Errorf("unexpected replay or missing physical Read feedback: call %d %s", n, data)
				}
				emitPlanFixture(w, int(n), "", "ACTUAL_SDK_IO_TURN_FINISHED")
			}))
			defer peer.Close()
			f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
			user := append(rawMain(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "Use the actual private file tool"}}), '\n')
			var input io.Reader = sdkPhysicalFaultReader{fault}
			var reader *io.PipeReader
			var writer *io.PipeWriter
			if scenario == "assistant-consumer" {
				input = bytes.NewReader(user)
			} else if scenario == "after-completed-read" {
				reader, writer = io.Pipe()
				input = reader
				defer reader.Close()
				defer writer.Close()
			}
			var resultCount, rejectedAssistant atomic.Int32
			var diagnostics bytes.Buffer
			disabled := false
			m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", PermissionMode: "default", AllowedTools: []string{"Read", "Write"}, MaxTurns: 100400, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: input, Output: io.Discard, Error: &diagnostics, OnOutput: func(data []byte) error {
				var frame protocol.JSONObject
				if err := frame.UnmarshalJSON(data); err != nil {
					return err
				}
				if scenario == "assistant-consumer" && frame.String("type") == "assistant" {
					rejectedAssistant.Add(1)
					return fault
				}
				if frame.String("type") == "result" {
					resultCount.Add(1)
					if writer != nil {
						_ = writer.CloseWithError(fault)
					}
				}
				return nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if writer != nil {
				go func() {
					if _, err := writer.Write(user); err != nil {
						_ = writer.CloseWithError(err)
					}
				}()
			}
			code, err := m.Run(ctx)
			if code != 1 || !errors.Is(err, fault) {
				t.Fatal("real SDK transport cause was lost", code, err, diagnostics.String())
			}
			select {
			case <-m.Owner.Done():
			default:
				t.Fatal("transport fault left the actual Owner alive")
			}
			if _, err := os.Stat(writePath); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("assistant consumer fault still executed Write", err)
			}
			wantCalls := int32(0)
			if scenario == "assistant-consumer" {
				wantCalls = 1
				if rejectedAssistant.Load() != 1 || resultCount.Load() != 0 {
					t.Fatal("failed assistant was replayed or falsely completed", rejectedAssistant.Load(), resultCount.Load())
				}
			} else if scenario == "after-completed-read" {
				wantCalls = 2
				if resultCount.Load() != 1 {
					t.Fatal("completed turn lost its SDK result before input failure")
				}
			}
			if calls.Load() != wantCalls {
				t.Fatal("physical model calls changed across I/O failure", calls.Load(), wantCalls)
			}
			if scenario != "before-input" {
				paths, err := filepath.Glob(filepath.Join(f.config, "projects", "*", m.Controller.SessionID()+".jsonl"))
				if err != nil || len(paths) != 1 {
					t.Fatal("accepted transcript was not drained before Close", paths, err)
				}
				data, err := os.ReadFile(paths[0])
				if err != nil || strings.Contains(string(data), fault.Error()) {
					t.Fatal("transport failure polluted canonical history", err, string(data))
				}
				if scenario == "after-completed-read" && (!bytes.Contains(data, []byte(marker)) || !bytes.Contains(data, []byte("ACTUAL_SDK_IO_TURN_FINISHED"))) {
					t.Fatal("physical completed turn disappeared during SDK failure cleanup", string(data))
				}
				if scenario == "assistant-consumer" && (!bytes.Contains(data, []byte("io-output-write")) || bytes.Contains(data, []byte(`"tool_use_id":"io-output-write"`))) {
					t.Fatal("failed consumer lost accepted assistant or invented tool execution", string(data))
				}
			}
		})
	}
}
