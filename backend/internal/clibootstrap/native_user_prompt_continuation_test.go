package clibootstrap

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualNativeUserPromptBlockThenSameHostNextHumanWritesReadsAndConsumesAttachmentsOnce(t *testing.T) {
	var journal, target string
	posts := atomic.Int32{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		index := posts.Add(1)
		if strings.Contains(string(body), "FIRST_PROMPT_ERASED") || strings.Contains(string(body), "UserPromptSubmit operation blocked") {
			t.Error("erased user/warning entered actual next-turn API")
		}
		if !strings.Contains(string(body), "actual-next-human-context") {
			t.Error("next human Hook context absent in actual POST")
		}
		switch index {
		case 1:
			emitNativeTaskTool(w, "next-human-write", "Write", map[string]any{"file_path": target, "content": "actual-next-human-physical-result"})
		case 2:
			emitNativeTaskTool(w, "next-human-read", "Read", map[string]any{"file_path": target})
		default:
			if !strings.Contains(string(body), "actual-next-human-physical-result") {
				t.Error("actual Read feedback missing")
			}
			emitPlanFixture(w, int(index), "", "NEXT_HUMAN_DONE")
		}
	}))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		delete(n.Environment, "CLAUDE_CODE_SIMPLE")
		delete(n.Environment, "CLAUDE_CODE_DISABLE_ATTACHMENTS")
		n.Environment["NODE_ENV"] = "production"
		journal = filepath.Join(n.CWD, "prompt-hook-inputs.jsonl")
		target = filepath.Join(n.CWD, "next-human.txt")
		script := filepath.Join(n.CWD, "prompt-hook.py")
		code := fmt.Sprintf("import os,json,sys\nx=json.load(sys.stdin)\nx['physical_pid']=os.getpid()\npath=%q\nfirst=not os.path.exists(path)\nwith open(path,'a') as f:f.write(json.dumps(x)+'\\n')\nprint(json.dumps({'decision':'block','reason':'actual-first-block'} if first else {'hookSpecificOutput':{'hookEventName':'UserPromptSubmit','additionalContext':'actual-next-human-context'}}))\n", journal)
		if err := os.WriteFile(script, []byte(code), 0600); err != nil {
			t.Fatal(err)
		}
		skillDirectory := filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "skills", "user-prompt-once")
		if err := os.MkdirAll(skillDirectory, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skillDirectory, "SKILL.md"), []byte("---\nname: user-prompt-once\ndescription: actual input-stage listing latch\n---\nPrivate fixture prompt.\n"), 0600); err != nil {
			t.Fatal(err)
		}
		command := "/usr/bin/python3 " + strconv.Quote(script)
		if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), rawMain(map[string]any{"hooks": map[string]any{"UserPromptSubmit": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command}}}}}}), 0600); err != nil {
			t.Fatal(err)
		}
	}, func(o *MainPrintOptions) {
		o.Arguments.Tools = []string{"Read,Write,Skill"}
		o.Arguments.PermissionMode = "bypassPermissions"
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	input := func(text string) protocol.JSONObject {
		return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": text})})
	}
	if err := m.RunTurn(ctx, input("FIRST_PROMPT_ERASED")); err != nil {
		t.Fatal("first blocked input", err)
	}
	defer m.ShutdownSession(context.Background())
	if posts.Load() != 0 {
		t.Fatal("blocked input posted model request")
	}
	firstSID := m.Controller.SessionID()
	err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		history := m.Controller.History()
		if len(history) != 1 || history[0].String("type") != "system" || history[0].String("level") != "warning" {
			t.Fatal("Source block did not erase base input/attachments", string(rawMain(history)))
		}
		if m.skillListing == nil || !m.skillListing.sent[""]["user-prompt-once"] {
			t.Fatal("original attachment producer was delayed until after blocking hook")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = m.RunTurn(ctx, input("actual next human input")); err != nil {
		t.Fatal("same Host next human", err)
	}
	if m.Controller.SessionID() != firstSID || posts.Load() != 3 {
		t.Fatal("same shared session/physical model rounds", m.Controller.SessionID(), firstSID, posts.Load())
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "actual-next-human-physical-result" {
		t.Fatal("actual Write/Read", string(data), err)
	}
	data, err := os.ReadFile(journal)
	if err != nil || len(strings.Split(strings.TrimSpace(string(data)), "\n")) != 2 {
		t.Fatal("one actual Hook process per human input", err, string(data))
	}
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		for _, message := range m.Controller.History() {
			if attachment, ok := message.Object("attachment"); ok && attachment.String("type") == "skill_listing" && strings.Contains(attachment.String("content"), "user-prompt-once") {
				t.Fatal("initial skill listing was reconsumed after block")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = m.ShutdownSession(context.Background()); err != nil {
		t.Fatal(err)
	}
}
