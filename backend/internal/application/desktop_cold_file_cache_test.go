package application

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualColdResumeDirectWriteUsesRestoredReadCacheWithoutExtraRead(t *testing.T) {
	for _, mode := range []string{"resume", "fork", "resume-at-final", "resume-at-before-read"} {
		t.Run(mode, func(t *testing.T) {
			denied := mode == "resume-at-before-read"
			var calls atomic.Int32
			path := ""
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				switch calls.Add(1) {
				case 1:
					response(w, "cold-cache-read", "Read", map[string]string{"file_path": path}, "")
				case 2:
					response(w, "cold-cache-warm-done", "", nil, "READ_CACHE_WARM_DONE")
				case 3:
					response(w, "cold-cache-write", "Write", map[string]string{"file_path": path, "content": "RESTORED_CACHE_WRITE_OK\n"}, "")
				case 4:
					if unread := strings.Contains(string(body), "File has not been read yet"); unread != denied {
						t.Errorf("cold cache validation unread=%v mode=%s", unread, mode)
					}
					response(w, "cold-cache-final", "", nil, "COLD_DIRECT_WRITE_DONE")
				default:
					t.Error("extra model request: a Read must not be inserted by the harness")
					http.Error(w, "extra request", 500)
				}
			}))
			defer provider.Close()
			o := nativeSessionOptions(t, provider.URL)
			o.Arguments.Tools = []string{"Read,Write"}
			path = filepath.Join(o.WorkDir, "cold-cache.txt")
			if err := os.WriteFile(path, []byte("before\n"), 0600); err != nil {
				t.Fatal(err)
			}
			warm, err := NewSession(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			if err := warm.Submit(context.Background(), tui.Input{Text: "Read this file once and finish.", TurnID: "warm"}, nil); err != nil {
				t.Fatal(err)
			}
			id := warm.process.Controller.SessionID()
			history := warm.process.Controller.History()
			if len(history) < 2 {
				t.Fatal("warm actual history missing")
			}
			if err := warm.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			o.Arguments.SessionID = ""
			o.Arguments.Resume = id
			o.Arguments.ForkSession = mode == "fork"
			if mode == "resume-at-final" {
				o.Arguments.ResumeAt = history[len(history)-1].String("uuid")
			}
			if mode == "resume-at-before-read" {
				o.Arguments.ResumeAt = history[0].String("uuid")
			}
			o.Native, err = newIsolatedProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: o.Environment, CWD: o.WorkDir, Platform: o.Platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
			if err != nil {
				t.Fatal(err)
			}
			cold, err := NewSession(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			defer cold.Close(context.Background())
			if err := cold.Submit(context.Background(), tui.Input{Text: "Use the prior Read and directly replace the file. Do not Read again.", TurnID: "cold"}, nil); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			expected := "RESTORED_CACHE_WRITE_OK\n"
			if denied {
				expected = "before\n"
			}
			if err != nil || string(data) != expected {
				t.Fatalf("actual cold write %q %v", data, err)
			}
			if (cold.process.Controller.SessionID() != id) != (mode == "fork") || calls.Load() != 4 {
				t.Fatal("cold changed SID or model loop", calls.Load())
			}
		})
	}
}
