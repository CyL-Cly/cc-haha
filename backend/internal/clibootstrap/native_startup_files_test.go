package clibootstrap

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/remoteio"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
)

func TestCLIStartupFilesParsePathsAndInvalidTraversal(t *testing.T) {
	files := parseStartupFileSpecs([]string{"file_one:dir/a.txt file_two:b:colon.txt", "bad", ":empty", "file_empty:", "file_tab:tab\tpath"})
	want := []startupFileSpec{{"file_one", "dir/a.txt"}, {"file_two", "b:colon.txt"}, {"file_tab", "tab\tpath"}}
	if !reflect.DeepEqual(files, want) {
		t.Fatal(files)
	}
	base := t.TempDir()
	uploads := filepath.Join(base, "session", "uploads")
	for _, row := range []struct{ input, want string }{
		{"dir/../a.txt", filepath.Join(uploads, "a.txt")},
		{"../a.txt", ""}, {"..name", ""},
		{filepath.Join(uploads, "a.txt"), filepath.Join(uploads, "a.txt")},
		{string(filepath.Separator) + "uploads" + string(filepath.Separator) + "a.txt", filepath.Join(uploads, "a.txt")},
	} {
		if actual := startupFilePath(base, "session", row.input); actual != row.want {
			t.Fatal(row, actual)
		}
	}
}

func TestCLIStartupFilesBearerHeadersRetryPolicyAndPhysicalWrites(t *testing.T) {
	n := &NativeBoundary{CWD: t.TempDir(), NativeProcess: true}
	var mu sync.Mutex
	attempts := map[string]int{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ingress-only-fixture" || r.Header.Get("anthropic-beta") != "files-api-2025-04-14,oauth-2025-04-20" || r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Error("wrong Files API ingress headers", r.Header)
		}
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/gateway/v1/files/"), "/content")
		mu.Lock()
		attempts[id]++
		attempt := attempts[id]
		mu.Unlock()
		switch id {
		case "retry":
			if attempt < 3 {
				w.WriteHeader(503)
				return
			}
		case "missing":
			w.WriteHeader(404)
			return
		case "denied":
			w.WriteHeader(403)
			return
		case "auth":
			w.WriteHeader(401)
			return
		case "exhausted":
			w.WriteHeader(429)
			return
		}
		_, _ = fmt.Fprint(w, "downloaded:"+id)
	}))
	defer peer.Close()
	var delays []time.Duration
	var waitMu sync.Mutex
	job := beginStartupFiles(n, parseStartupFileSpecs([]string{"retry:docs/retry.txt missing:missing.txt denied:denied.txt auth:auth.txt exhausted:exhausted.txt invalid:../escape.txt"}), "session", peer.URL+"/gateway/v1/", "ingress-only-fixture", peer.Client(), func(ctx context.Context, d time.Duration) error {
		waitMu.Lock()
		delays = append(delays, d)
		waitMu.Unlock()
		return ctx.Err()
	})
	var warnings bytes.Buffer
	m := &MainPrint{startupFiles: job, options: MainPrintOptions{Error: &warnings}}
	defer m.closeCLIStartupFiles()
	if err := m.AwaitCLIStartupFiles(context.Background()); err != nil {
		t.Fatal(err)
	}
	if warnings.String() != "Warning: 5/6 file(s) failed to download.\n" {
		t.Fatal(warnings.String())
	}
	content, err := os.ReadFile(filepath.Join(n.CWD, "session", "uploads", "docs", "retry.txt"))
	if err != nil || string(content) != "downloaded:retry" {
		t.Fatal(string(content), err)
	}
	if !reflect.DeepEqual(attempts, map[string]int{"retry": 3, "missing": 1, "denied": 1, "auth": 1, "exhausted": 3}) || len(delays) != 4 {
		t.Fatal("wrong retry/nonretry policy", attempts, delays)
	}
	if !strings.Contains(job.results[4].err.Error(), "after 3 attempts") || job.results[0].bytes != len(content) || job.results[5].path != "" {
		t.Fatal(job.results)
	}
	for _, file := range []string{"missing.txt", "denied.txt", "auth.txt", "exhausted.txt"} {
		if _, err := os.Stat(filepath.Join(n.CWD, "session", "uploads", file)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("failed download wrote a file", file, err)
		}
	}
}

func TestCLIStartupFilesFiveConcurrentWorkersAndRuntimeCancelJoin(t *testing.T) {
	n := &NativeBoundary{CWD: t.TempDir(), NativeProcess: true}
	var active, maximum, requests atomic.Int32
	started := make(chan struct{}, 8)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		count := active.Add(1)
		defer active.Add(-1)
		for prior := maximum.Load(); count > prior && !maximum.CompareAndSwap(prior, count); prior = maximum.Load() {
		}
		started <- struct{}{}
		<-r.Context().Done()
	}))
	defer peer.Close()
	var files []startupFileSpec
	for i := 0; i < 8; i++ {
		files = append(files, startupFileSpec{fmt.Sprint(i), fmt.Sprintf("%d.txt", i)})
	}
	job := beginStartupFiles(n, files, "session", peer.URL, "fake-token", peer.Client(), startupFileWait)
	m := &MainPrint{startupFiles: job}
	defer m.closeCLIStartupFiles()
	for i := 0; i < 5; i++ {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("five workers did not start", maximum.Load())
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.AwaitCLIStartupFiles(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("resume wait ignored cancel", err)
	}
	m.closeCLIStartupFiles()
	if maximum.Load() != 5 || requests.Load() > 5 {
		t.Fatal("wrong concurrency or cancelled queued HTTP work", maximum.Load(), requests.Load())
	}
	if _, err := os.Stat(filepath.Join(n.CWD, "session")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancelled download wrote files", err)
	}
}

func TestCLIStartupFilesRequiresSharedIngressTokenEvenForInvalidSpecsAndPrefersRemoteSID(t *testing.T) {
	n := &NativeBoundary{CWD: t.TempDir(), NativeProcess: true, Environment: map[string]string{"CLAUDE_SESSION_INGRESS_TOKEN_FILE": filepath.Join(t.TempDir(), "missing")}}
	m := &MainPrint{options: MainPrintOptions{Native: n, Arguments: PrintArguments{FileSpecs: []string{"invalid"}}, SessionIngressAuth: remoteio.NewIngressAuth(n.Environment)}}
	if err := m.startCLIStartupFiles("local"); err == nil || !strings.Contains(err.Error(), "Session token required") {
		t.Fatal("invalid specs bypassed ingress auth", err)
	}
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte("file-ingress-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer file-ingress-token" {
			t.Error("shared token file not consumed", r.Header)
		}
		_, _ = fmt.Fprint(w, "physical remote file")
	}))
	defer peer.Close()
	n.Environment["CLAUDE_SESSION_INGRESS_TOKEN_FILE"] = tokenPath
	n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	n.Environment["CLAUDE_CODE_REMOTE_SESSION_ID"] = "remote-session"
	m.options.SessionIngressAuth = remoteio.NewIngressAuth(n.Environment)
	m.options.Arguments.FileSpecs = []string{"file_one:nested/a.txt"}
	var err error
	m.tls, err = tlsconfig.NewState(os.ReadFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.startCLIStartupFiles("local-session"); err != nil {
		t.Fatal(err)
	}
	defer m.closeCLIStartupFiles()
	if err := m.AwaitCLIStartupFiles(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(n.CWD, "remote-session", "uploads", "nested", "a.txt")); err != nil {
		t.Fatal("remote sid did not own actual downloaded file", err)
	}
	if token := m.options.SessionIngressAuth.Token(); token != "file-ingress-token" {
		t.Fatal("ingress credential cache diverged", token)
	}
}

func TestCLIStartupFilesParentArgumentsDoNotDownloadAgainForDetachedAgent(t *testing.T) {
	m := &MainPrint{agentChild: true, options: MainPrintOptions{Arguments: PrintArguments{FileSpecs: []string{"file_parent:parent.txt"}}}}
	if err := m.startCLIStartupFiles("child"); err != nil || m.startupFiles != nil {
		t.Fatal("Source ASK child reran its parent's CLI startup producer", err)
	}
	if err := m.AwaitCLIStartupFiles(context.Background()); err != nil {
		t.Fatal(err)
	}
}
