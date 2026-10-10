package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/migrations"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

type lifecycleReadGate struct {
	storage.FileSystem
	path             string
	once             sync.Once
	started, release chan struct{}
}

func (fs *lifecycleReadGate) ReadFile(path string) ([]byte, error) {
	if path == fs.path {
		fs.once.Do(func() { close(fs.started); <-fs.release })
	}
	return fs.FileSystem.ReadFile(path)
}

type lifecycleStatusWriter struct {
	http.ResponseWriter
	status int
}

func (w *lifecycleStatusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *lifecycleStatusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *lifecycleStatusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(p)
}

func TestActualSourceRequestAbortSettles499AndCommittedSettingsWriteContinues(t *testing.T) {
	seed := map[string]any{"future": map[string]any{"preserved": true}, "chatSendBehavior": "enter"}
	patch := map[string]any{"chatSendBehavior": "shift-enter"}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual Source requires pinned Bun")
	}
	home := t.TempDir()
	fixture, e := filepath.Abs("testdata/request-abort-settings.fixture.ts")
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_REQUEST_ABORT_SETTINGS_FIXTURE=1", "NODE_ENV=test", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	input, _ := json.Marshal(map[string]any{"home": home, "seed": seed, "patch": patch})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatalf("original request/physical storage %v %s", e, stderr.String())
	}
	var source struct {
		ResponseStatus, OperationStatus              int
		OperationPendingAtResponse, HeldPhysicalRead bool
		Raw                                          any
		Consumer                                     string
		Capabilities                                 struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &source); e != nil || source.ResponseStatus != 499 || source.OperationStatus != 200 || !source.OperationPendingAtResponse || !source.HeldPhysicalRead || len(source.Capabilities.Blocked) != 0 {
		t.Fatalf("original observations %v %s", e, out)
	}
	o := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) {
		t.Error("settings update started Agent")
		return nil, errors.New("unexpected Agent")
	})
	if e = os.MkdirAll(filepath.Join(o.ConfigDir, "cc-haha"), 0700); e != nil {
		t.Fatal(e)
	}
	seedRaw, _ := json.Marshal(seed)
	file := filepath.Join(o.ConfigDir, "settings.json")
	if e = os.WriteFile(file, seedRaw, 0600); e != nil {
		t.Fatal(e)
	}
	managed := filepath.Join(o.ConfigDir, "cc-haha", "settings.json")
	if e = os.WriteFile(managed, []byte(`{}`), 0600); e != nil {
		t.Fatal(e)
	}
	gate := &lifecycleReadGate{FileSystem: storage.New(storage.Options{}).FileSystem(), path: managed, started: make(chan struct{}), release: make(chan struct{})}
	var release sync.Once
	releaseStorage := func() { release.Do(func() { close(gate.release) }) }
	defer releaseStorage()
	store := storage.New(storage.Options{FS: gate})
	upgrader, e := migrations.New(migrations.Options{Store: store})
	if e != nil {
		t.Fatal(e)
	}
	o.Settings, e = settings.New(settings.Options{Store: store, HomeDir: o.HomeDir, ConfigDir: o.ConfigDir, Environment: func() map[string]string { return map[string]string{} }, EnsureMigrated: upgrader.SettingsCallback, ShellEnvironment: func(context.Context) (map[string]string, error) { return map[string]string{}, nil }, GitIgnore: func(context.Context, string, string) error { return nil }})
	if e != nil {
		t.Fatal(e)
	}
	s, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { releaseStorage(); _ = s.Close(context.Background()) })
	returned, disconnected := make(chan int, 1), make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		go func() { <-r.Context().Done(); close(disconnected) }()
		capture := &lifecycleStatusWriter{ResponseWriter: w}
		s.ServeHTTP(capture, r)
		returned <- capture.status
	}))
	defer peer.Close()
	clientCtx, disconnect := context.WithCancel(context.Background())
	defer disconnect()
	body, _ := json.Marshal(patch)
	req, _ := http.NewRequestWithContext(clientCtx, "PUT", peer.URL+"/api/settings/user", bytes.NewReader(body))
	client := make(chan error, 1)
	go func() {
		resp, e := peer.Client().Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		client <- e
	}()
	select {
	case <-gate.started:
	case <-time.After(5 * time.Second):
		releaseStorage()
		t.Fatal("actual migration storage did not start")
	}
	disconnect()
	select {
	case e = <-client:
		if !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		releaseStorage()
		t.Fatal("client did not disconnect")
	}
	select {
	case <-disconnected:
	case <-time.After(5 * time.Second):
		releaseStorage()
		t.Fatal("Go server did not see real disconnect")
	}
	select {
	case status := <-returned:
		if status != source.ResponseStatus {
			t.Errorf("response status Go=%d Source=%d before held storage completes", status, source.ResponseStatus)
		}
	case <-time.After(5 * time.Second):
		releaseStorage()
		t.Fatal("response did not settle on request abort")
	}
	releaseStorage()
	// A subsequent real settings GET awaits the same original migration task;
	// use the real write-lock barrier to observe the committed PUT afterward.
	if e = store.WithWriteLock(file, func() error { return nil }); e != nil {
		t.Fatal(e)
	}
	_, e = upgrader.Ensure(context.Background(), o.ConfigDir)
	if e != nil {
		t.Fatal(e)
	}
	stored, e := os.ReadFile(file)
	if e != nil {
		t.Fatal(e)
	}
	var actual any
	_ = json.Unmarshal(stored, &actual)
	if !reflect.DeepEqual(actual, source.Raw) {
		t.Errorf("committed physical settings after disconnect Go=%s Source=%v", stored, source.Raw)
	}
}
