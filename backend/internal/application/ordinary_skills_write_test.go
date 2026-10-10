package application

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestOrdinarySkillDoesNotBlockActualWriteAndRead(t *testing.T) {
	var step atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch step.Add(1) {
		case 1:
			response(w, "ordinary-write", "Write", map[string]any{"file_path": "ordinary-skill-result.txt", "content": "ORDINARY_SKILL_WRITE_OK"}, "")
		case 2:
			response(w, "ordinary-read", "Read", map[string]any{"file_path": "ordinary-skill-result.txt"}, "")
		default:
			response(w, "ordinary-finished", "", nil, "ORDINARY_SKILL_WRITE_OK")
		}
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	o.Arguments.Tools = []string{"Read,Write"}
	skill := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "skills", "ordinary", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skill), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skill, []byte("---\nname: ordinary\ndescription: Normal skill without path conditions\n---\nRead the fixture marker.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	if len(s.process.Registry.Catalog().Skills) == 0 {
		t.Fatal("regression lacks an actually loaded ordinary skill")
	}
	if err := s.Submit(context.Background(), tui.Input{Text: "Write and read the ordinary skill marker."}, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(o.WorkDir, "ordinary-skill-result.txt"))
	if err != nil || string(data) != "ORDINARY_SKILL_WRITE_OK" {
		t.Fatal("actual Write was incorrectly blocked by ordinary skills", string(data), err)
	}
	if step.Load() != 3 {
		t.Fatal("actual write/read/model chain did not finish", step.Load())
	}
}
