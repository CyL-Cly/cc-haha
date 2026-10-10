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
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestNativeSkillSlashInvocationReachesActualHarnessAndRemainsTurnScoped(t *testing.T) {
	var calls atomic.Int32
	var requests [][]byte
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests = append(requests, body)
		calls.Add(1)
		response(w, "skill-prompt", "", nil, "SKILL COMPLETE")
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	directory := filepath.Join(o.WorkDir, ".claude", "skills", "native-skill")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte("---\ndescription: Use actual skill instructions\narguments: target\nmodel: SkillModel\neffort: low\n---\nInspect $target using real instructions from ${CLAUDE_SKILL_DIR}. Session ${CLAUDE_SESSION_ID}."), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	if err = s.Submit(context.Background(), tui.Input{Text: "/native-skill owned-file"}, nil); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || obj(requests[0]).String("model") != "SkillModel" {
		t.Fatalf("skill model override missing %s", requests[0])
	}
	for _, want := range []string{"<command-name>/native-skill</command-name>", "<command-args>owned-file</command-args>", "Inspect owned-file using real instructions", directory, s.process.Controller.SessionID()} {
		if !bytes.Contains(requests[0], []byte(want)) {
			t.Fatalf("actual skill expansion missing %q: %s", want, requests[0])
		}
	}
	if bytes.Contains(requests[0], []byte("command_permissions")) {
		t.Fatal("permission bookkeeping leaked to model prompt")
	}
	if err = s.Submit(context.Background(), tui.Input{Text: "Continue this task."}, nil); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || obj(requests[1]).String("model") != "fixture-model" {
		t.Fatalf("skill changed following turn model %s", requests[1])
	}
	files, err := filepath.Glob(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "projects", "*", s.process.Controller.SessionID()+".jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("Inspect owned-file")) {
		t.Fatalf("actual skill history was not recorded: %s", data)
	}
	history, _ := json.Marshal(s.process.Controller.History())
	if !bytes.Contains(history, []byte("command_permissions")) {
		t.Fatal("permission attachment was lost from live Harness history")
	}
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte{'\n'}) {
		if !json.Valid(line) {
			t.Fatal("invalid actual transcript", string(line))
		}
	}
}
