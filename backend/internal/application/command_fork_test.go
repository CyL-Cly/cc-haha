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
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
)

func TestPublicNativeForkKeepsOriginalAndResumesIndependentConversation(t *testing.T) {
	var mu sync.Mutex
	var requests []json.RawMessage
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, body)
		ordinal := len(requests)
		mu.Unlock()
		response(w, "fork-response", "", nil, "FORK ANSWER")
		if ordinal > 2 {
			t.Error("fork or resume opened an extra model request")
		}
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	const oldSID = "086a1b3a-078c-45e4-8be8-7c6f388d3194"
	project := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "projects", projectpath.Sanitize(o.Native.CWD))
	originalPath := filepath.Join(project, oldSID+".jsonl")
	original := []byte(`{"type":"user","uuid":"10000000-0000-4000-8000-000000000001","sessionId":"` + oldSID + `","timestamp":"2026-01-01T00:00:00.000Z","parentUuid":null,"future":{"keep":true},"message":{"role":"user","content":"ORIGINAL REQUEST"}}` + "\n" + `{"type":"assistant","uuid":"10000000-0000-4000-8000-000000000002","sessionId":"` + oldSID + `","timestamp":"2026-01-01T00:00:01.000Z","parentUuid":"10000000-0000-4000-8000-000000000001","message":{"role":"assistant","model":"original-provider-model","content":[{"type":"text","text":"ORIGINAL ANSWER"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":3}}}` + "\n")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(originalPath, original, 0600); err != nil {
		t.Fatal(err)
	}
	run := func(sid string, fork bool, prompt string) string {
		t.Helper()
		args := []string{"--print", "--resume", sid, "--tools", "Read,Write,Edit,Bash,Glob,Grep", "--model", "fixture-model", "--thinking", "disabled", "--permission-mode", "bypassPermissions", "--system-prompt", "Use the actual inherited conversation.", "--output-format", "json"}
		if fork {
			args = append(args, "--fork-session")
		}
		args = append(args, prompt)
		command, _, err := ParseCommand(args, o.Version, o.WorkDir, o.Native.Temp)
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		code, err := RunCommand(context.Background(), command, o, strings.NewReader(""), &out, nil)
		if err != nil || code != 0 || obj(out.Bytes()).String("result") != "FORK ANSWER" {
			t.Fatalf("actual public fork/resume failed: code=%d err=%v output=%s", code, err, out.Bytes())
		}
		return obj(out.Bytes()).String("session_id")
	}
	forkSID := run(oldSID, true, "FIRST FORK REQUEST")
	if forkSID == "" || forkSID == oldSID {
		t.Fatal("fork reused the original SID", forkSID)
	}
	forkPath := filepath.Join(project, forkSID+".jsonl")
	if log, err := os.ReadFile(forkPath); err != nil || !bytes.Contains(log, []byte("FIRST FORK REQUEST")) || !bytes.Contains(log, []byte("ORIGINAL ANSWER")) {
		t.Fatalf("independent fork did not persist inherited history: %s %v", log, err)
	}
	if resumedSID := run(forkSID, false, "SECOND FORK REQUEST"); resumedSID != forkSID {
		t.Fatal("resuming the fork selected a different SID", resumedSID)
	}
	if after, err := os.ReadFile(originalPath); err != nil || !bytes.Equal(after, original) {
		t.Fatal("public fork or follow-up mutated original source history", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 || !bytes.Contains(requests[0], []byte("ORIGINAL ANSWER")) || !bytes.Contains(requests[0], []byte("FIRST FORK REQUEST")) || !bytes.Contains(requests[1], []byte("FORK ANSWER")) || !bytes.Contains(requests[1], []byte("SECOND FORK REQUEST")) {
		t.Fatalf("actual model requests lost inherited or fork history: %q", requests)
	}
}
