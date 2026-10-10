package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNativeMCPOrdinaryWorkflowGitIsolationPhysicalCleanupActualSource(t *testing.T) {
	home, env := ordinaryFixture(t)
	cwd := filepath.Join(home, "repo")
	_ = os.Mkdir(cwd, 0700)
	_ = os.WriteFile(filepath.Join(cwd, "tracked.txt"), []byte("owned fixture\n"), 0600)
	for _, args := range [][]string{{"init", "-b", "main"}, {"add", "."}, {"commit", "-m", "owned fixture"}} {
		r, e := worktree.NativeRun(context.Background(), worktree.ProcessRequest{Command: "/usr/bin/git", Arguments: args, CWD: cwd, Environment: env, IgnoreStdin: true})
		if e != nil || r.Code != 0 {
			t.Fatal(e, r.Stderr)
		}
	}
	var mu sync.Mutex
	paths := []string{}
	trees := []string{}
	round := 0
	pattern := regexp.MustCompile(`Working directory: ([^\n]+)`)
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body protocol.JSONObject
		_ = body.UnmarshalJSON(raw)
		var system []protocol.JSONObject
		_ = json.Unmarshal(body.Get("system"), &system)
		path := ""
		for _, block := range system {
			if m := pattern.FindStringSubmatch(block.String("text")); len(m) == 2 {
				path = m[1]
			}
		}
		t.Logf("actual native model environment cwd: %s", path)
		if _, e := os.Stat(filepath.Join(path, ".git")); e != nil {
			t.Error("physical isolated git producer missing at actual model", e)
		}
		mu.Lock()
		paths = append(paths, path)
		round++
		i := round
		entries, _ := filepath.Glob(filepath.Join(cwd, ".claude", "worktrees", "agent-*"))
		if len(entries) != 1 {
			t.Errorf("actual owned worktree at model request%d: %v", i, entries)
		} else {
			trees = append(trees, entries[0])
		}
		mu.Unlock()
		if i%2 == 1 {
			emitNativeTaskTool(w, fmt.Sprintf("isolated-write-%d", i), "Write", map[string]any{"file_path": "isolation-marker.txt", "content": "actual relative worker write\n"})
		} else {
			emitPlanFixture(w, i, "", "ACTUAL_ISOLATED_WORKER")
		}
	}))
	defer peer.Close()
	cert := filepath.Join(home, "cert.pem")
	_ = os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw}), 0600)
	env["ANTHROPIC_BASE_URL"], env["NODE_EXTRA_CA_CERTS"] = peer.URL, cert
	script := `export const meta={name:'isolation',description:'actual isolated producer'};return await agent('Inspect the owned git worktree',{isolation:'worktree'})`
	source := startOrdinarySource(t, home, cwd, env)
	want := ordinaryData(t, source.call(t, "Workflow", map[string]any{"script": script}))
	ordinaryAwaitJournal(t, filepath.Join(want.String("transcriptDir"), "journal.jsonl"))
	awaitOrdinaryAgentTranscript(t, want.String("transcriptDir"))
	source.disposeWatcher(t)
	checkOrdinaryIsolatedAgentArtifacts(t, want.String("transcriptDir"), trees[0])
	marker := filepath.Join(cwd, "isolation-marker.txt")
	sourceMarker, e := os.ReadFile(marker)
	if e != nil || string(sourceMarker) != "actual relative worker write\n" {
		t.Fatal("Source actual relative Write location", e, string(sourceMarker))
	}
	_ = os.Remove(marker)
	host, _ := ordinaryGoContext(t, home, cwd, env)
	got, e := host.Call(context.Background(), "Workflow", rawMain(map[string]any{"script": script}))
	if e != nil {
		t.Fatal(e)
	}
	launched := ordinaryData(t, got)
	if e = host.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	ordinaryAwaitJournal(t, filepath.Join(launched.String("transcriptDir"), "journal.jsonl"))
	checkOrdinaryIsolatedAgentArtifacts(t, launched.String("transcriptDir"), trees[2])
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 4 || len(trees) != 4 {
		t.Fatal("actual Source/Go worker requests", paths)
	}
	goMarker, e := os.ReadFile(marker)
	if e != nil || string(goMarker) != string(sourceMarker) {
		t.Error("Go actual relative Write must match Source original directory", e, string(goMarker))
	}
	for _, path := range paths {
		if path != cwd {
			t.Errorf("Source/Go actual model CWD differs: Source original=%s actual=%s", cwd, path)
		}
	}
	for _, path := range trees {
		if _, e := os.Stat(path); !os.IsNotExist(e) {
			t.Fatal("unchanged isolated worktree not physically removed", path, e)
		}
	}
}

func checkOrdinaryIsolatedAgentArtifacts(t *testing.T, transcriptDir, actualTree string) {
	t.Helper()
	directory := filepath.Dir(filepath.Dir(transcriptDir))
	files, e := filepath.Glob(filepath.Join(directory, "agent-*.meta.json"))
	if e != nil || len(files) != 1 {
		t.Fatal("actual flat agent metadata", e, files)
	}
	raw, e := os.ReadFile(files[0])
	var metadata protocol.JSONObject
	if e != nil || metadata.UnmarshalJSON(raw) != nil || metadata.String("worktreePath") != actualTree {
		t.Fatal("actual worktree metadata path", e, string(raw), actualTree)
	}
	log, e := os.ReadFile(strings.TrimSuffix(files[0], ".meta.json") + ".jsonl")
	if e != nil || !bytes.Contains(log, []byte("ACTUAL_ISOLATED_WORKER")) || !bytes.Contains(log, []byte("isolation-marker.txt")) {
		t.Fatalf("actual flat agent tool/model transcript: error=%v data=%s", e, log)
	}
}

// The stock sidechain logger queues writes behind its own flush timer. Observe
// that physical completion before terminating the disposable branch watcher;
// journal completion alone does not mean the sidechain file has been flushed.
func awaitOrdinaryAgentTranscript(t *testing.T, transcriptDir string) {
	t.Helper()
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		files, _ := filepath.Glob(filepath.Join(filepath.Dir(filepath.Dir(transcriptDir)), "agent-*.jsonl"))
		if len(files) == 1 {
			raw, _ := os.ReadFile(files[0])
			if bytes.Contains(raw, []byte("ACTUAL_ISOLATED_WORKER")) && bytes.Contains(raw, []byte("isolation-marker.txt")) {
				return
			}
		}
		select {
		case <-deadline.C:
			t.Fatal("actual Source logger did not flush before fixture cleanup", files)
		case <-ticker.C:
		}
	}
}
