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
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
)

func TestCLIFileAndFrozenDeepLinkMetadataReachSharedStartupArguments(t *testing.T) {
	dir := t.TempDir()
	command, _, err := ParseCommand([]string{"--file", "file_one:a.txt", "file_two:nested/b.txt", "--deep-link-origin", "--deep-link-repo", "owner/repo", "--deep-link-last-fetch", "1700000000000", "--prefill", "review this"}, "fixture", dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	a := command.Arguments
	if !reflect.DeepEqual(a.FileSpecs, []string{"file_one:a.txt", "file_two:nested/b.txt"}) || !a.DeepLinkOrigin || a.DeepLinkRepo != "owner/repo" || a.DeepLinkLastFetch == nil || *a.DeepLinkLastFetch != 1700000000000 || a.Prefill != "review this" {
		t.Fatal(a)
	}
	// The frozen normal profile enables TRANSCRIPT_CLASSIFIER only; LODESTONE
	// metadata is accepted without rendering its gated banner or submitting q.
	if command.Prompt != "" {
		t.Fatal("external prefill became a submitted user prompt", command.Prompt)
	}
	command, _, err = ParseCommand([]string{"--deep-link-last-fetch", "not-a-number"}, "fixture", dir, dir)
	if err != nil || command.Arguments.DeepLinkLastFetch != nil {
		t.Fatal("Source nonfinite deep-link timestamp was not undefined", command, err)
	}
}

func TestPublicCLIFileDownloadDoesNotDelayPrintInferenceAndCloseCancels(t *testing.T) {
	fileStarted, fileCancelled := make(chan struct{}), make(chan struct{})
	var messages atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if r.URL.Path == "/v1/files/file_slow/content" {
			if r.Header.Get("Authorization") != "Bearer public-cli-file-ingress" {
				t.Error("file used provider key instead of session ingress token", r.Header)
			}
			close(fileStarted)
			<-r.Context().Done()
			close(fileCancelled)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-fileStarted:
		case <-time.After(3 * time.Second):
			t.Error("file producer did not start during CLI startup")
		}
		messages.Add(1)
		response(w, "async-file-print", "", nil, "PRINT_DID_NOT_WAIT_FOR_FILES")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Native.Environment["CLAUDE_CODE_SESSION_ACCESS_TOKEN"] = "public-cli-file-ingress"
	args := []string{"--print", "--model", "fixture-model", "--system-prompt", "actual asynchronous CLI file fixture", "--tools", "", "--thinking", "disabled", "--file", "file_slow:slow.txt", "--", "run before file completes"}
	command, _, err := ParseCommand(args, o.Version, o.WorkDir, t.TempDir(), o.Environment)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var output bytes.Buffer
	code, err := RunCommand(ctx, command, o, strings.NewReader(""), &output, nil)
	if err != nil || code != 0 || messages.Load() != 1 || !strings.Contains(output.String(), "PRINT_DID_NOT_WAIT_FOR_FILES") {
		t.Fatal("print incorrectly waited for startup file", code, err, output.String())
	}
	select {
	case <-fileCancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("public CLI close did not cancel actual file HTTP request")
	}
	_ = filepath.WalkDir(o.WorkDir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == "slow.txt" {
			t.Error("cancelled background download wrote a partial file", path)
		}
		return err
	})
}

func TestPublicCLIResumeWaitsForFileBeforeTUIAndLeavesParentUnchanged(t *testing.T) {
	fileStarted, release := make(chan struct{}), make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if r.URL.Path != "/v1/files/file_resume/content" {
			t.Error("resume TUI fixture unexpectedly called inference", r.URL.Path)
			w.WriteHeader(500)
			return
		}
		close(fileStarted)
		select {
		case <-release:
			_, _ = fmt.Fprint(w, "RESUME_FILE_READY")
		case <-r.Context().Done():
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Native.Environment["CLAUDE_CODE_SESSION_ACCESS_TOKEN"] = "resume-file-ingress"
	const sid = "b1111111-1111-4111-8111-111111111111"
	path := filepath.Join(o.Native.Environment["CLAUDE_CONFIG_DIR"], "projects", projectpath.Sanitize(o.Native.CWD), sid+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	parent := []byte(`{"type":"user","uuid":"owned-prior-user","parentUuid":null,"sessionId":"` + sid + `","timestamp":"2026-10-01T00:00:00Z","message":{"role":"user","content":"owned parent"}}` + "\n" + `{"type":"assistant","uuid":"owned-prior-assistant","parentUuid":"owned-prior-user","sessionId":"` + sid + `","timestamp":"2026-10-01T00:00:01Z","message":{"role":"assistant","id":"prior-answer","content":[{"type":"text","text":"owned answer"}]}}` + "\n")
	if err := os.WriteFile(path, parent, 0600); err != nil {
		t.Fatal(err)
	}
	command, _, err := ParseCommand([]string{"--resume", sid, "--fork-session", "--model", "fixture-model", "--system-prompt", "resume file boundary", "--tools", "", "--thinking", "disabled", "--file", "file_resume:ready.txt"}, o.Version, o.WorkDir, t.TempDir(), o.Environment)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	var output bytes.Buffer
	done := make(chan error, 1)
	go func() {
		code, err := RunCommand(ctx, command, o, strings.NewReader("/exit\n"), &output, []string{"TERM=dumb"})
		if err == nil && code != 0 {
			err = fmt.Errorf("CLI exit %d", code)
		}
		done <- err
	}()
	select {
	case <-fileStarted:
	case err := <-done:
		t.Fatal("public CLI exited before file start", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case err := <-done:
		t.Fatal("resume TUI rendered and exited before file resolved", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err, output.String())
		}
	case <-ctx.Done():
		t.Fatal("TUI did not continue after file completed", ctx.Err())
	}
	var downloaded bool
	_ = filepath.WalkDir(o.WorkDir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == "ready.txt" {
			content, err := os.ReadFile(path)
			downloaded = err == nil && string(content) == "RESUME_FILE_READY"
		}
		return err
	})
	if !downloaded {
		t.Fatal("resume TUI proceeded without actual downloaded file")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(parent, after) {
		t.Fatal("forked startup altered the parent transcript", err)
	}
}

func TestPublicCLINoIDResumeWaitsForFilesBeforeChooserCancellation(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if r.URL.Path != "/v1/files/file_chooser/content" {
			t.Error("unexpected chooser request", r.URL.Path)
			w.WriteHeader(500)
			return
		}
		close(started)
		select {
		case <-release:
			_, _ = io.WriteString(w, "CHOOSER_FILE_READY")
		case <-r.Context().Done():
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Native.Environment["CLAUDE_CODE_SESSION_ACCESS_TOKEN"] = "owned-chooser-ingress"
	command, _, err := ParseCommand([]string{"--resume", "--file", "file_chooser:chooser.txt", "--model", "fixture-model", "--tools", "", "--thinking", "disabled"}, o.Version, o.WorkDir, t.TempDir(), o.Environment)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := RunCommand(ctx, command, o, strings.NewReader("\x1b"), io.Discard, []string{"TERM=dumb"})
		done <- err
	}()
	select {
	case <-started:
	case err := <-done:
		t.Fatal("chooser exited before async producer", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case err := <-done:
		t.Fatal("chooser cancellation overtook file promise", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("chooser did not resume after file", ctx.Err())
	}
	var ready bool
	_ = filepath.WalkDir(o.WorkDir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == "chooser.txt" {
			data, err := os.ReadFile(path)
			ready = err == nil && string(data) == "CHOOSER_FILE_READY"
		}
		return nil
	})
	if !ready {
		t.Fatal("no-ID resume chooser did not preserve startup download")
	}
}
