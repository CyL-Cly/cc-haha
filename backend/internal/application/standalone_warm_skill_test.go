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
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualStandaloneFirstSkillDirectoryInstallUpdatesIdleCommandsAndSameSession(t *testing.T) {
	requests := make(chan []byte, 4)
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- body
		response(w, "standalone-skill", "", nil, "ACTUAL STANDALONE COMPLETE")
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	changes := make(chan []sdkhost.ControlCommand, 16)
	var menuCalls atomic.Int32
	o.OnCommandsChanged = func(commands []sdkhost.ControlCommand) error { menuCalls.Add(1); changes <- commands; return nil }
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := s.Submit(ctx, tui.Input{Text: "Warm this ordinary CLI before skills exist."}, nil); err != nil {
		t.Fatal(err)
	}
	<-requests
	file := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "skills", "first-disk-skill", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("---\ndescription: First physical installation\n---\nSTANDALONE_FIRST_SKILL $ARGUMENTS"), 0600); err != nil {
		t.Fatal(err)
	}
	waitMenu := func(want bool) {
		t.Helper()
		for {
			select {
			case commands := <-changes:
				present := false
				for _, c := range commands {
					present = present || c.Name == "first-disk-skill"
				}
				if present == want {
					return
				}
			case <-ctx.Done():
				t.Fatal("physical first installation/uninstall never refreshed idle CLI menu")
			}
		}
	}
	waitMenu(true)
	if err := s.Submit(ctx, tui.Input{Text: "/first-disk-skill same-cli"}, nil); err != nil {
		t.Fatal(err)
	}
	second := <-requests
	if !strings.Contains(string(second), "STANDALONE_FIRST_SKILL same-cli") || !strings.Contains(string(second), "Warm this ordinary CLI before skills exist.") {
		t.Fatal("physical change failed to update same live CLI expansion/history", string(second))
	}
	if err := os.RemoveAll(filepath.Dir(file)); err != nil {
		t.Fatal(err)
	}
	waitMenu(false)
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	closedCalls := menuCalls.Load()
	if err := clibootstrap.NotifyNativeSkillCommandsChanged(o.Environment["CLAUDE_CONFIG_DIR"]); err != nil {
		t.Fatal("closed session retained managed admission", err)
	}
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("post-close"), 0600); err != nil {
		t.Fatal(err)
	}
	if menuCalls.Load() != closedCalls {
		t.Fatal("closed session retained managed subscription")
	}
}
