package application

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualSkillToolCarriesPromptPermissionAndModelOnlyWithinCurrentHumanTurn(t *testing.T) {
	var posts atomic.Int32
	var requests [][]byte
	var mu sync.Mutex
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, body)
		mu.Unlock()
		switch posts.Add(1) {
		case 1:
			response(w, "skill", "Skill", map[string]any{"skill": "scoped-test"}, "")
		case 2:
			response(w, "write-skill", "Write", map[string]any{"file_path": "skill-result.txt", "content": "ACTUAL_SKILL_MARKER"}, "")
		case 3:
			response(w, "read-skill", "Read", map[string]any{"file_path": "skill-result.txt"}, "")
		case 4:
			response(w, "skill-done", "", nil, "SKILL_CHAIN_OK")
		case 5:
			response(w, "next-human-write", "Write", map[string]any{"file_path": "next-human.txt", "content": "NEXT_HUMAN_MARKER"}, "")
		default:
			response(w, "next-human-done", "", nil, "NEXT_HUMAN_OK")
		}
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	o.Arguments.Tools = []string{"Skill,Write,Read"}
	o.Arguments.AllowedTools = []string{"Skill,Read"}
	o.Arguments.PermissionMode = "default"
	o.Environment["CLAUDE_CODE_ALWAYS_ENABLE_EFFORT"] = "1"
	o.Native.Environment["CLAUDE_CODE_ALWAYS_ENABLE_EFFORT"] = "1"
	dir := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "skills", "scoped-test")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: scoped-test\ndescription: Actual scoped modifier regression\nallowed-tools: Write, Read\nmodel: claude-opus-4-6\neffort: low\n---\nUse the actual Write tool then Read to verify ACTUAL_SKILL_MARKER.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := s.Submit(ctx, tui.Input{Text: "Invoke scoped-test and execute its instructions."}, func(e tui.Event) error {
		if e.Kind == tui.PermissionAsked {
			return fmt.Errorf("Skill allowed tool unexpectedly asked: %s", e.Permission.ToolName)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(o.WorkDir, "skill-result.txt"))
	if err != nil || string(data) != "ACTUAL_SKILL_MARKER" {
		t.Fatal("actual Skill/Write/Read chain did not execute", string(data), err)
	}
	mu.Lock()
	if len(requests) != 4 {
		t.Fatal("first human turn did not finish", len(requests))
	}
	for i, body := range requests {
		model := obj(body).String("model")
		want := "fixture-model"
		if i > 0 {
			want = "claude-opus-4-6"
		}
		if model != want {
			t.Fatalf("round %d model=%s want=%s", i, model, want)
		}
		effort := obj(obj(body).Get("output_config")).String("effort")
		wantEffort := "high"
		if i > 0 {
			wantEffort = "low"
		}
		if effort != wantEffort {
			t.Fatalf("round %d effort=%s want=%s", i, effort, wantEffort)
		}
		if i > 0 && !bytes.Contains(body, []byte("Use the actual Write tool then Read")) {
			t.Fatalf("real Skill prompt missing at round %d", i)
		}
	}
	mu.Unlock()
	asked := 0
	responses := make(chan error, 1)
	if err := s.Submit(ctx, tui.Input{Text: "Write another marker in a new human turn."}, func(e tui.Event) error {
		if e.Kind == tui.PermissionAsked {
			asked++
			id := e.Permission.ID
			go func() { responses <- s.RespondPermission(ctx, id, tui.PermissionDecision{Allowed: true}) }()
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if asked != 1 {
		t.Fatal("Skill permission leaked into next human turn", asked)
	}
	if err := <-responses; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 6 || obj(requests[4]).String("model") != "fixture-model" || obj(requests[5]).String("model") != "fixture-model" {
		t.Fatal("Skill model persisted beyond current ASK", len(requests))
	}
	for _, body := range requests[4:] {
		if obj(obj(body).Get("output_config")).String("effort") != "high" {
			t.Fatal("Skill effort persisted beyond current ASK")
		}
	}
	if _, err := os.Stat(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "settings.json")); !os.IsNotExist(err) {
		t.Fatal("Skill modifier persisted global settings", err)
	}
}
