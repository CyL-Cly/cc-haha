package application

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestActualPublicCLIResumeAtForkAndNoSessionPersistenceUsesSharedModelConsumer(t *testing.T) {
	const first = "PUBLIC_CLI_PARENT_BOUNDARY_B7A1"
	const after = "PUBLIC_CLI_AFTER_BOUNDARY_MUST_BE_OMITTED_043A"
	const appendix = "PUBLIC_CLI_APPEND_SYSTEM_4521"
	var requests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, e := io.ReadAll(r.Body)
		if e != nil {
			t.Error(e)
		}
		if !bytes.Contains(data, []byte(first)) || bytes.Contains(data, []byte(after)) || !bytes.Contains(data, []byte(appendix)) || !bytes.Contains(data, []byte("CLI new instruction")) {
			t.Error("Real public option consumer lost boundary/append semantics", string(data))
		}
		requests.Add(1)
		response(w, "cli-ephemeral", "", nil, "PUBLIC_CLI_EPHEMERAL_OK")
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	parent, child := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	path := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "projects", "private", parent+".JSONL")
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	original := []byte(`{"type":"user","uuid":"u1","parentUuid":null,"sessionId":"` + parent + `","timestamp":"2026-10-01T00:00:00Z","message":{"role":"user","content":"` + first + `"}}` + "\n" + `{"type":"assistant","uuid":"a1","parentUuid":"u1","sessionId":"` + parent + `","timestamp":"2026-10-01T00:00:01Z","message":{"role":"assistant","id":"a1","content":[{"type":"text","text":"Initial assistant"}]}}` + "\n" + `{"type":"user","uuid":"later","parentUuid":"a1","sessionId":"` + parent + `","timestamp":"2026-10-01T00:00:02Z","message":{"role":"user","content":"` + after + `"}}` + "\n")
	if e := os.WriteFile(path, original, 0600); e != nil {
		t.Fatal(e)
	}
	args := []string{"--print", "--model", "fixture-model", "--thinking", "disabled", "--resume", path, "--resume-session-at", "a1", "--fork-session", "--session-id", child, "--no-session-persistence", "--append-system-prompt", appendix, "CLI new instruction"}
	command, _, e := ParseCommand(args, o.Version, o.WorkDir, t.TempDir(), o.Environment)
	if e != nil {
		t.Fatal(e)
	}
	var out bytes.Buffer
	code, e := RunCommand(context.Background(), command, o, strings.NewReader(""), &out, nil)
	if e != nil || code != 0 || requests.Load() != 1 || !strings.Contains(out.String(), "PUBLIC_CLI_EPHEMERAL_OK") {
		t.Fatal(code, e, requests.Load(), out.String())
	}
	parentAfter, e := os.ReadFile(path)
	if e != nil || !bytes.Equal(original, parentAfter) {
		t.Fatal("No-persistence CLI changed original parent", e)
	}
	_ = filepath.WalkDir(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "projects"), func(path string, d os.DirEntry, e error) error {
		if e == nil && !d.IsDir() && filepath.Base(path) == child+".jsonl" {
			t.Error("CLI created disabled transcript", path)
		}
		return e
	})
	if command, _, e = ParseCommand([]string{"--print", "--model", "fixture-model", "hello"}, o.Version, o.WorkDir, t.TempDir()); e != nil || command.Arguments.SessionPersistenceDisabled {
		t.Fatal("Omitted flag changed normal persistence", e, command)
	}
}
