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

func TestPublicCLIDisabledSlashReturnsSourceNoQueryThenModelUsesSkillOnColdResume(t *testing.T) {
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		if calls.Add(1) == 1 {
			response(w, "cli-disabled-skill", "Skill", map[string]any{"skill": "private-skill"}, "")
		} else {
			if !bytes.Contains(data, []byte("PRIVATE_SKILL_TASK_EXECUTED")) {
				t.Error("disable-slash incorrectly disabled model Skill")
			}
			response(w, "cli-disabled-final", "", nil, "DISABLED_SLASH_MODEL_TASK_DONE")
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	path := filepath.Join(o.WorkDir, ".claude", "skills", "private-skill", "SKILL.md")
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	_ = os.WriteFile(path, []byte("---\ndescription: Private CLI skill\n---\nPRIVATE_SKILL_TASK_EXECUTED"), 0600)
	for _, cold := range []bool{false, true} {
		flag, prompt := "--session-id", "/private-skill no model request"
		if cold {
			flag = "--resume"
			prompt = "Use private-skill and finish the private task"
		}
		argv := []string{"--print", "--model", "fixture-model", "--tools", "Skill,Read", "--system-prompt", "Perform the private CLI skill task", flag, o.Arguments.SessionID, "--disable-slash-commands", "--permission-mode", "bypassPermissions", "--", prompt}
		command, _, err := ParseCommand(argv, o.Version, o.WorkDir, t.TempDir(), o.Environment)
		if err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		code, err := RunCommand(context.Background(), command, o, strings.NewReader(""), &output, nil)
		if err != nil || code != 0 {
			t.Fatal(cold, code, err, output.String())
		}
		if !cold {
			if calls.Load() != 0 || !strings.Contains(output.String(), "Unknown skill: private-skill") {
				t.Fatal("disabled slash did not preserve Source no-query result", calls.Load(), output.String())
			}
		} else if !strings.Contains(output.String(), "DISABLED_SLASH_MODEL_TASK_DONE") {
			t.Fatal("model task after cold resume failed", output.String())
		}
	}
	if calls.Load() != 2 {
		t.Fatal("disabled slash or Skill replayed model", calls.Load())
	}
}
