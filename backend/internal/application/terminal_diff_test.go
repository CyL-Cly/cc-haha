package application

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/terminaldiff"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualTerminalDiffGitAndWriteTurnUseSameHarnessNextHuman(t *testing.T) {
	var mu sync.Mutex
	var requests [][]byte
	var target string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, body)
		n := len(requests)
		mu.Unlock()
		if n == 1 {
			response(w, "diff-read", "Read", map[string]any{"file_path": target}, "")
		} else if n == 2 {
			response(w, "diff-write", "Write", map[string]any{"file_path": target, "content": "after actual tool\n"}, "")
		} else {
			response(w, "diff-ready", "", nil, "ACTUAL_DIFF_READY")
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive = true
	o.ForegroundShellProgress = true
	seedTerminalReleaseWarmFixture(t, o)
	o.Environment["NO_PROXY"] = "127.0.0.1,localhost"
	o.Native.Environment["NO_PROXY"] = "127.0.0.1,localhost"
	target = filepath.Join(o.WorkDir, "tracked.txt")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("/usr/bin/git", args...)
		cmd.Dir = o.WorkDir
		cmd.Env = []string{"HOME=" + o.Environment["HOME"], "PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1"}
		if out, e := cmd.CombinedOutput(); e != nil {
			t.Fatal(e, string(out))
		}
	}
	git("init", "-q")
	git("config", "user.name", "Owned fixture")
	git("config", "user.email", "fixture@example.invalid")
	if e := os.WriteFile(target, []byte("before actual tool\n"), 0600); e != nil {
		t.Fatal(e)
	}
	git("add", "tracked.txt")
	git("commit", "-qm", "private fixture")
	s, e := NewSession(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	owner, controller, host := s.process.Owner, s.process.Controller, s.process.Host
	if e = s.Submit(context.Background(), tui.Input{Text: "write a physical change"}, nil); e != nil {
		t.Fatal(e)
	}
	events := make(chan tui.Event, 10)
	emit := func(event tui.Event) error { events <- event; return nil }
	if handled, e := s.InvokeTerminalCommand(context.Background(), "diff", "", emit); !handled || e != nil {
		t.Fatal(handled, e)
	}
	initial := <-events
	if initial.LocalPanel == nil {
		t.Fatal(initial)
	}
	var loading terminaldiff.Dialog
	if e = json.Unmarshal(initial.LocalPanel.Presentation, &loading); e != nil {
		t.Fatal(e)
	}
	if len(loading.Sources) != 2 || !loading.Sources[0].Data.Loading {
		snapshot, _ := s.process.TerminalConversation(context.Background())
		t.Logf("actual structured history: %s", raw(snapshot.Messages))
		t.Fatalf("Source mount/turn input missing %+v", loading)
	}
	var refreshed tui.Event
	select {
	case refreshed = <-events:
	case <-time.After(10 * time.Second):
		t.Fatal("actual git completion missing")
	}
	var data terminaldiff.Dialog
	if e = json.Unmarshal(refreshed.LocalPanel.Presentation, &data); e != nil {
		t.Fatal(e)
	}
	if len(data.Sources) != 2 || len(data.Sources[0].Data.Files) != 1 || data.Sources[0].Data.Files[0].Path != "tracked.txt" || data.Sources[0].Data.Files[0].LinesAdded != 1 || data.Sources[0].Data.Files[0].LinesRemoved != 1 || len(data.Sources[1].Data.Files) != 1 || data.Sources[1].Data.Files[0].Path != target {
		t.Fatalf("actual git/tool turn %+v", data)
	}
	if e = s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: initial.LocalPanel.ID, Action: "diff-close"}, nil); e != nil {
		t.Fatal(e)
	}
	if e = s.Submit(context.Background(), tui.Input{Text: "human after diff"}, nil); e != nil {
		t.Fatal(e)
	}
	if owner != s.process.Owner || controller != s.process.Controller || host != s.process.Host {
		t.Fatal("diff replaced Harness")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 4 || !strings.Contains(string(requests[3]), "human after diff") || !strings.Contains(string(requests[3]), "after actual tool") {
		t.Fatalf("next actual model history missing %d", len(requests))
	}
}
