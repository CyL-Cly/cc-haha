package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNativePrintReturnsUnapprovedToolToModelWithoutWaitingForUI(t *testing.T) {
	var calls atomic.Int32
	var target string
	var followup json.RawMessage
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if calls.Add(1) == 1 {
			response(w, "unapproved-print", "Write", map[string]any{"file_path": target, "content": "must not be written"}, "")
			return
		}
		followup, _ = io.ReadAll(r.Body)
		response(w, "permission-feedback", "", nil, "Permission was not granted; file unchanged.")
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	target = filepath.Join(o.WorkDir, "unapproved.txt")
	args := []string{"-p", "Create a file.", "--model", "fixture-model", "--tools", "Read,Write", "--system-prompt", "Use the actual tool.", "--thinking", "disabled", "--permission-mode", "default", "--output-format", "json"}
	command, _, err := ParseCommand(args, o.Version, o.WorkDir, o.Native.Temp)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var out bytes.Buffer
	code, err := RunCommand(ctx, command, o, strings.NewReader(""), &out, nil)
	if err != nil || code != 0 || obj(out.Bytes()).String("result") != "Permission was not granted; file unchanged." {
		t.Fatalf("non-interactive print awaited a nonexistent permission responder: code=%d err=%v output=%s", code, err, out.Bytes())
	}
	if _, err = os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("unapproved write executed", err)
	}
	if calls.Load() != 2 || !bytes.Contains(followup, []byte(`"type":"tool_result"`)) || !bytes.Contains(followup, []byte(`"is_error":true`)) || !bytes.Contains(followup, []byte(`"tool_use_id":"call-unapproved-print"`)) {
		t.Fatalf("original denied tool result did not reach the next model request: calls=%d body=%s", calls.Load(), followup)
	}
}
