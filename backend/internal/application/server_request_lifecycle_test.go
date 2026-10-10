package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/fsnotify/fsnotify"
)

func TestDesktopDingTalkHTTPResponseSettlesBeforeOriginalCommittedPollCompletes(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual Source Bun required")
	}
	seed := map[string]any{"future": 17, "dingtalk": map[string]any{"allowedUsers": []string{"retained"}}}
	reply := map[string]any{"errcode": 0, "status": "SUCCESS", "client_id": "fake-client", "client_secret": "fake-secret"}
	private := t.TempDir()
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-dingtalk-registration.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]any{"rows": []any{map[string]any{"seed": seed, "action": "poll", "input": map[string]string{"deviceCode": "private-device"}, "abortDuringRequest": true, "replies": []any{map[string]any{"body": reply}}}}})
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = private
	cmd.Env = []string{"HOME=" + private, "USERPROFILE=" + private, "CLAUDE_CONFIG_DIR=" + filepath.Join(private, ".claude"), "TMPDIR=" + private, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(private), "CC_HAHA_DINGTALK_REGISTRATION_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal("actual Source", err, stderr.String())
	}
	var expected []map[string]any
	if err = json.Unmarshal(output, &expected); err != nil || len(expected) != 1 {
		t.Fatal(err)
	}
	if expected[0]["aborted"] != true || expected[0]["status"] != float64(200) {
		t.Fatal("actual Source did not exercise client abort", expected)
	}
	started, release := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if r.URL.Path != "/app/registration/poll" || body["device_code"] != "private-device" {
			t.Error("actual upstream poll", r.URL.Path, body)
		}
		close(started)
		<-release
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(reply)
	}))
	defer upstream.Close()
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	home, config := o.Environment["HOME"], o.Environment["CLAUDE_CONFIG_DIR"]
	o.Environment["DINGTALK_REGISTRATION_BASE_URL"] = upstream.URL
	if err = os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(config, "adapters.json")
	initial, _ := json.Marshal(seed)
	if err = os.WriteFile(file, initial, 0600); err != nil {
		t.Fatal(err)
	}
	service, err := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: home, ConfigDir: config, WorkDir: home, Version: o.Version, Arguments: o.Arguments, Stderr: io.Discard, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close(context.Background()) })
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()
	if err = watcher.Add(config); err != nil {
		t.Fatal(err)
	}
	done, serverDisconnected := make(chan struct{}), make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		go func() { <-r.Context().Done(); close(serverDisconnected) }()
		service.ServeHTTP(w, r)
		close(done)
	}))
	defer peer.Close()
	requestCtx, disconnect := context.WithCancel(context.Background())
	defer disconnect()
	req, err := http.NewRequestWithContext(requestCtx, "POST", peer.URL+"/api/adapters/dingtalk/registration/poll", bytes.NewBufferString(`{"deviceCode":"private-device"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	clientResult := make(chan error, 1)
	go func() {
		res, err := peer.Client().Do(req)
		if res != nil {
			res.Body.Close()
		}
		clientResult <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("actual poll never started")
	}
	disconnect()
	select {
	case err := <-clientResult:
		if !errors.Is(err, context.Canceled) {
			close(release)
			t.Fatalf("client did not disconnect: %v", err)
		}
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("client abort did not settle")
	}
	select {
	case <-serverDisconnected:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("actual server did not observe client disconnect")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("Source response499 should settle before owned poll finishes")
	}
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not settle original committed poll")
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	committed := false
	for !committed {
		select {
		case event := <-watcher.Events:
			if filepath.Base(event.Name) == "adapters.json" {
				// Earlier initialization/migration can publish the same path.
				// Observe the exact committed Source data, not the first event.
				data, err := os.ReadFile(file)
				var value any
				if err == nil && json.Unmarshal(data, &value) == nil {
					committed = reflect.DeepEqual(value, expected[0]["raw"])
				}
			}
		case err := <-watcher.Errors:
			t.Fatal(err)
		case <-deadline.C:
			t.Fatal("owned committed poll did not publish the actual atomic adapters file")
		}
	}
	stored, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var actual any
	if err = json.Unmarshal(stored, &actual); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected[0]["raw"]) {
		t.Fatalf("client abort changed committed registration persistence: Go %v Source %v", actual, expected[0]["raw"])
	}
}
