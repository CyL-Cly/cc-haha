package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPublicCLIEndSessionSameBatchExitsWithStdinOpen(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	binary := filepath.Join(root, "public-end-session-cli")
	buildArgs := []string{"build", "-p=4", "-race", "-tags=native_process_fixture", "-o", binary}
	// A parent go-test overlay does not reach this real nested go build.
	// The optional test-only explicit overlay is pinned by Root's admission;
	// normal physical verification leaves it unset.
	if overlay := os.Getenv("CC_HAHA_PUBLIC_CLI_BUILD_OVERLAY"); overlay != "" {
		info, statErr := os.Lstat(overlay)
		if !filepath.IsAbs(overlay) || statErr != nil || !info.Mode().IsRegular() {
			t.Fatal("explicit actual CLI build overlay must be an absolute regular file", statErr)
		}
		if os.Getenv("GOCOVERDIR") != "" {
			t.Fatal("explicit overlay CLI diagnostic requires an uninstrumented build; use normal physical integration for coverage")
		}
		buildArgs = append(buildArgs, "-overlay="+overlay)
	}
	buildArgs = append(buildArgs, ".")
	build := exec.CommandContext(ctx, "go", buildArgs...)
	capturePublicGoCoverage(t, build)
	t.Logf("actual public CLI build argv=%q", build.Args)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatal("public CLI build", err, string(output))
	}
	home := resumeErrorHome(t, root, "go-end-session")
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	settingsBefore, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var modelCalls atomic.Int32
	peer := httptest.NewServer(nativeCLIModelHTTPHandler(t, func(w http.ResponseWriter, r *http.Request) {
		modelCalls.Add(1)
		http.Error(w, "end_session must not open a model", http.StatusInternalServerError)
	}))
	defer peer.Close()
	processCtx, processCancel := context.WithTimeout(ctx, 12*time.Second)
	defer processCancel()
	args := []string{"--bare", "--print", "--session-id", resumeErrorInitialSID, "--model", "fixture-model", "--no-computer-use", "--no-chrome", "--thinking", "disabled", "--system-prompt", "Private public end-session process", "--tools", "", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--setting-sources", "user"}
	process := exec.CommandContext(processCtx, binary, args...)
	process.Dir = home
	process.Env = resumeErrorEnv(home, root, peer.URL)
	var stdout, stderr bytes.Buffer
	process.Stdout = &stdout
	process.Stderr = &stderr
	stdin, err := process.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close() // Never close before natural process Wait completes.
	capturePublicGoCoverage(t, process)
	if err = process.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- process.Wait() }()
	const unreadMarker = "PUBLIC_END_SAME_BATCH_USER_MUST_REMAIN_UNREAD"
	batch := `{"type":"control_request","request_id":"public-end-held-stdin","request":{"subtype":"end_session"}}` + "\n" + `{"type":"user","uuid":"0d72a4b2-c04d-4ea8-9f8d-925f541ee511","message":{"role":"user","content":"` + unreadMarker + `"}}` + "\n"
	// One actual pipe write contains both protocol lines. EOF is deliberately
	// withheld: closing stdin cannot be what makes end_session terminate.
	written, writeErr := stdin.Write([]byte(batch))
	if writeErr != nil || written != len(batch) {
		processCancel()
		<-done
		t.Fatalf("actual same-batch pipe write=%d/%d: %v", written, len(batch), writeErr)
	}
	waitErr := <-done
	if waitErr != nil || processCtx.Err() != nil {
		t.Fatalf("public CLI did not naturally exit with stdin open: %v context=%v stderr%s stdout%s", waitErr, processCtx.Err(), stderr.Bytes(), stdout.Bytes())
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected public CLI stderr: %s", stderr.Bytes())
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	acks, frames := 0, 0
	for {
		var frame map[string]any
		err := decoder.Decode(&frame)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("actual public stdout was not complete JSON: %v stdout%s", err, stdout.Bytes())
		}
		frames++
		response, ok := frame["response"].(map[string]any)
		if frame["type"] != "control_response" || !ok || response["request_id"] != "public-end-held-stdin" || response["subtype"] != "success" {
			t.Fatalf("unexpected end-session output or later user execution: %v", frame)
		}
		acks++
	}
	if acks != 1 || frames != 1 || modelCalls.Load() != 0 {
		t.Fatalf("public end-session ACK=%d frames=%d modelCalls=%d stdout%s", acks, frames, modelCalls.Load(), stdout.Bytes())
	}
	err = filepath.WalkDir(home, func(path string, entry fs.DirEntry, failure error) error {
		if failure != nil {
			return failure
		}
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".jsonl") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte(unreadMarker)) {
			t.Errorf("later same-batch user was persisted in %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	settingsAfter, err := os.ReadFile(settingsPath)
	if err != nil || !bytes.Equal(settingsBefore, settingsAfter) {
		t.Fatal("end-session mutated private saved settings", err)
	}
}
