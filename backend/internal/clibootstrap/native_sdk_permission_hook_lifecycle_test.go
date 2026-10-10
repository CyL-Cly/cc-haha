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
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualDefaultMainPrintSDKPermissionHooksParentCancelEOFAndOwnerCloseDrainBothProducers(t *testing.T) {
	for _, action := range []string{"cancel", "EOF", "OwnerClose"} {
		t.Run(action, func(t *testing.T) {
			f := worktreeMainFixture(t)
			f.write(t, filepath.Join(f.config, "settings.json"), `{}`)
			path := filepath.Join(f.home, "permission-lifetime-external.txt")
			f.write(t, path, "SHOULD_NOT_BE_READ_BEFORE_PERMISSION")
			f.n.Environment["ANTHROPIC_API_KEY"] = "fake-private-permission-lifetime"
			f.n.Environment["CLAUDE_CODE_ENTRYPOINT"] = "sdk-cli"
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.ReadAll(r.Body)
				n := calls.Add(1)
				if n == 1 {
					emitNativeTaskTool(w, "permission-lifetime-read", "Read", map[string]any{"file_path": path})
				} else if action == "EOF" && n == 2 {
					emitPlanFixture(w, 2, "", "PERMISSION_EOF_DENIAL_OBSERVED")
				} else {
					t.Error("new model after actual terminal action", action, n)
					http.Error(w, "unexpected", 500)
				}
			}))
			defer server.Close()
			f.n.Environment["ANTHROPIC_BASE_URL"] = server.URL
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			frames := make(chan protocol.JSONObject, 64)
			var diagnostic bytes.Buffer
			disabled := false
			m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{PermissionPromptToolName: "stdio", ComputerUse: &disabled, Model: "fixture-model", PermissionMode: "default", MaxTurns: 100400, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: reader, Output: io.Discard, Error: &diagnostic, OnOutput: func(data []byte) error {
				var frame protocol.JSONObject
				if err := frame.UnmarshalJSON(bytes.TrimSpace(data)); err != nil {
					return err
				}
				frames <- frame
				return nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			limit, finish := context.WithTimeout(context.Background(), 10*time.Second)
			defer finish()
			ctx, cancel := context.WithCancel(limit)
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := m.Run(ctx); done <- err }()
			joined := false
			defer func() {
				_ = writer.Close()
				_ = reader.Close()
				if !joined {
					<-done
				}
				_ = m.ShutdownSession(context.Background())
			}()
			go func() {
				init := map[string]any{"type": "control_request", "request_id": "permission-lifetime-init", "request": map[string]any{"subtype": "initialize", "hooks": map[string]any{"PermissionRequest": []any{map[string]any{"matcher": "Read", "hookCallbackIds": []string{"held-lifetime-hook"}}}}}}
				input := append(rawMain(init), '\n')
				input = append(input, rawMain(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "Read actual private permission-controlled input"}})...)
				input = append(input, '\n')
				_, _ = writer.Write(input)
			}()
			requests := map[string]string{}
			for len(requests) != 2 {
				select {
				case frame := <-frames:
					request, _ := frame.Object("request")
					if frame.String("type") == "control_request" {
						requests[request.String("subtype")] = frame.String("request_id")
					}
				case err := <-done:
					joined = true
					t.Fatal("both real permission producers not reached", action, err, diagnostic.String())
				case <-limit.Done():
					t.Fatal("both real permission producers timeout", action)
				}
			}
			switch action {
			case "cancel":
				cancel()
			case "EOF":
				_ = writer.Close()
			case "OwnerClose":
				m.Owner.Close()
			}
			select {
			case err = <-done:
				joined = true
			case <-limit.Done():
				t.Fatal("terminal permission action stranded actual Owner", action)
			}
			if action == "OwnerClose" && !errors.Is(err, cooperative.ErrClosed) {
				t.Fatal("Owner terminal cause", err)
			}
			if action == "cancel" && err != nil && !errors.Is(err, context.Canceled) {
				t.Fatal("parent cancellation cause", err)
			}
			if action == "EOF" && err != nil {
				t.Fatal("EOF original callback/permission catch", err, diagnostic.String())
			}
			cancelled := map[string]bool{}
		drain:
			for {
				select {
				case frame := <-frames:
					if frame.String("type") == "control_cancel_request" {
						cancelled[frame.String("request_id")] = true
					}
				default:
					break drain
				}
			}
			if action == "cancel" && (!cancelled[requests["hook_callback"]] || !cancelled[requests["can_use_tool"]]) {
				t.Fatal("parent abort failed to cancel actual SDK and Hook requests", cancelled, requests)
			}
			want := int32(1)
			if action == "EOF" {
				want = 2
			}
			if calls.Load() != want {
				t.Fatal("terminal model request cardinality", action, calls.Load(), want)
			}
			select {
			case <-m.Owner.Done():
			default:
				t.Fatal("public Run did not join its canonical Owner")
			}
		})
	}
}
