package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/access"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamworker"
)

func migrationOptions(t *testing.T) Options {
	o := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) {
		t.Error("validation admitted an Agent")
		return nil, context.Canceled
	})
	if err := os.MkdirAll(o.ConfigDir, 0700); err != nil {
		t.Fatal(err)
	}
	o.Authorization = access.Authorization{LocalToken: "private-migration-token", APIKey: "api-token", PetToken: "pet-token"}
	o.Environment = func() map[string]string { return map[string]string{"CLAUDE_CONFIG_DIR": o.ConfigDir} }
	return o
}

func migrationRequest(t *testing.T, host *httptest.Server, method, path string, status int) json.RawMessage {
	t.Helper()
	request, err := http.NewRequest(method, host.URL+"/api/runtime/migration/"+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer private-migration-token")
	response, err := host.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != status {
		t.Fatalf("%s %s: %d %s (%v)", method, path, response.StatusCode, body, err)
	}
	return body
}

func TestMigrationControlRejectsNonLocalCredentialsAndForwarding(t *testing.T) {
	o := migrationOptions(t)
	s, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	for _, profile := range []struct{ address, token, header string }{
		{"127.0.0.1:4010", "", ""}, {"192.168.0.4:4010", "private-migration-token", ""},
		{"127.0.0.1:4010", "api-token", ""}, {"127.0.0.1:4010", "pet-token", ""},
		{"127.0.0.1:4010", "private-migration-token", "Origin"},
		{"127.0.0.1:4010", "private-migration-token", "Forwarded"},
		{"127.0.0.1:4010", "private-migration-token", "X-Forwarded-For"},
		{"127.0.0.1:4010", "private-migration-token", "X-Forwarded-Host"},
		{"127.0.0.1:4010", "private-migration-token", "Via"},
	} {
		r := httptest.NewRequest("GET", "/api/runtime/migration/preview", nil)
		r.RemoteAddr = profile.address
		r.Header.Set("Authorization", "Bearer "+profile.token)
		if profile.header != "" {
			r.Header.Set(profile.header, "")
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("%+v bypassed local boundary: %d %s", profile, w.Code, w.Body.String())
		}
	}
	for _, address := range []string{"127.0.0.1:4010", "[::1]:4010"} {
		r := httptest.NewRequest("GET", "/api/runtime/migration/preview", nil)
		r.RemoteAddr = address
		r.Header.Set("Authorization", "Bearer private-migration-token")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(address, w.Code, w.Body.String())
		}
	}
}

func TestMigrationValidationReadsOldFilesWithoutBusinessStartupOrRepairs(t *testing.T) {
	o := migrationOptions(t)
	o.MigrationValidation = true
	project := filepath.Join(o.ConfigDir, "projects", "old-project")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	id := "e13ccaf8-c354-491d-a20e-7ce9f5802720"
	files := map[string]string{
		"settings.json": "\ufeff{\"futureOwner\":true}\n",
		".config.json":  "{\"legacyOwner\":true}\n",
		filepath.Join("projects", "old-project", id+".jsonl"): `{"type":"user","uuid":"message","sessionId":"` + id + `","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"Saved conversation"}}` + "\n",
	}
	for path, data := range files {
		if err := os.WriteFile(filepath.Join(o.ConfigDir, path), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s, host := testHost(t, o)
	if s.collaboration != nil || s.publicAccess != nil || s.options.Scheduler != nil || s.options.LocalIndex != nil || s.teams != nil {
		t.Fatal("validation created business services")
	}
	for _, path := range []string{"/api/sessions", "/api/settings", "/proxy/anything", "/sdk/session", "/ws/session", "/callback", "/"} {
		res, err := host.Client().Get(host.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 503 {
			t.Fatalf("validation admitted %s: %d", path, res.StatusCode)
		}
	}
	response := migrationRequest(t, host, "GET", "validate", 200)
	if strings.TrimSpace(string(response)) != `{"valid":true}` {
		t.Fatal(string(response))
	}
	for path, original := range files {
		data, err := os.ReadFile(filepath.Join(o.ConfigDir, path))
		if err != nil || string(data) != original {
			t.Fatal("validation changed owner data", path, err)
		}
	}
	entries, _ := os.ReadDir(o.ConfigDir)
	if len(entries) != 3 {
		t.Fatal("validation created files", entries)
	}
	if err := os.WriteFile(filepath.Join(o.ConfigDir, ".config.json"), []byte(`{"invalid":`), 0600); err != nil {
		t.Fatal(err)
	}
	migrationRequest(t, host, "GET", "validate", 409)
	if err := os.Remove(filepath.Join(o.ConfigDir, ".config.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(o.ConfigDir, ".claude.json"), []byte(`{"newGlobal":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	migrationRequest(t, host, "GET", "validate", 200)
}

func TestMigrationExternalRegistrationsArePreservedAndRecoveryDrains(t *testing.T) {
	o := migrationOptions(t)
	var shutdown atomic.Int32
	o.OnShutdown = func(context.Context) error { shutdown.Add(1); return nil }
	s, host := testHost(t, o)
	parent := os.Getppid()
	if parent <= 0 || !migrationProcessAlive(parent) {
		t.Skip("host has no observable live parent")
	}
	if err := os.MkdirAll(filepath.Join(o.ConfigDir, "sessions"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(o.ConfigDir, "sessions", fmt.Sprintf("%d.json", parent))
	original := []byte("malformed live external registration\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	preview := migrationRequest(t, host, "GET", "preview", 200)
	if !strings.Contains(string(preview), `"externalProcesses":1`) {
		t.Fatal(string(preview))
	}
	migrationRequest(t, host, "POST", "quiesce", 409)
	if shutdown.Load() != 0 {
		t.Fatal("closed before external process check")
	}
	res, err := host.Client().Get(host.URL + "/api/sessions")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 503 {
		t.Fatal("maintenance reopened after failure")
	}
	migrationRequest(t, host, "POST", "recover", 200)
	if shutdown.Load() != 1 {
		t.Fatal("recovery acknowledged without shutdown")
	}
	data, _ := os.ReadFile(path)
	if string(data) != string(original) {
		t.Fatal("inventory mutated external registration")
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationQuiescenceWaitsForDisconnectedAcceptedWritesBeforeShutdown(t *testing.T) {
	o := migrationOptions(t)
	path := filepath.Join(o.ConfigDir, "committed-setting.json")
	o.OnShutdown = func(context.Context) error {
		if _, err := os.Stat(path); err != nil {
			t.Error("closed resources before accepted write committed")
		}
		return nil
	}
	s, host := testHost(t, o)
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest("POST", "/accepted-write", nil).WithContext(ctx)
	entered, release, delivered, persisted := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(delivered)
		s.serveAPILifecycle(httptest.NewRecorder(), request, true, func(w http.ResponseWriter, r *http.Request) {
			close(entered)
			<-release
			if r.Context().Err() != nil {
				t.Error("migration canceled accepted persistence")
			}
			if err := os.WriteFile(path, []byte(`{"ownerSetting":"committed"}`), 0600); err != nil {
				t.Error(err)
			}
			close(persisted)
			writeJSON(w, 200, map[string]bool{"saved": true})
		})
	}()
	<-entered
	cancel()
	<-delivered
	result := make(chan error, 1)
	go func() { result <- s.quiesceForMigration(context.Background(), false) }()
	for i := 0; i < 100; i++ {
		s.mu.Lock()
		active := s.migration.active
		s.mu.Unlock()
		if active {
			break
		}
		time.Sleep(time.Millisecond)
	}
	res, err := host.Client().Get(host.URL + "/api/settings")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 503 {
		t.Fatal("new API admitted during migration")
	}
	pending(t, result)
	close(release)
	<-persisted
	receiveError(t, result)
}

func TestMigrationActivationDelegatesActualRuntime(t *testing.T) {
	o := migrationOptions(t)
	o.MigrationValidation = true
	var activations, shutdown atomic.Int32
	o.MigrationActivate = func(context.Context) (*Server, error) {
		activations.Add(1)
		next := o
		next.MigrationValidation, next.MigrationActivate = false, nil
		next.OnShutdown = func(context.Context) error { shutdown.Add(1); return nil }
		return New(next)
	}
	s, host := testHost(t, o)
	if err := os.WriteFile(filepath.Join(o.ConfigDir, "settings.json"), []byte(`[]`), 0600); err != nil {
		t.Fatal(err)
	}
	migrationRequest(t, host, "POST", "activate", 409)
	if activations.Load() != 0 {
		t.Fatal("started business before data validation")
	}
	if err := os.WriteFile(filepath.Join(o.ConfigDir, "settings.json"), []byte(`{"future":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	migrationRequest(t, host, "GET", "validate", 200)
	migrationRequest(t, host, "POST", "activate", 200)
	res, err := host.Client().Get(host.URL + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 || activations.Load() != 1 {
		t.Fatal("same listener failed to activate actual runtime", res.StatusCode, activations.Load())
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if shutdown.Load() != 1 {
		t.Fatal("outer shutdown leaked activated runtime")
	}
}

func TestMigrationShutdownWaitsForActivationAndClosesUnpublishedRuntime(t *testing.T) {
	o := migrationOptions(t)
	o.MigrationValidation = true
	entered, release := make(chan struct{}), make(chan struct{})
	var shutdown atomic.Int32
	o.MigrationActivate = func(context.Context) (*Server, error) {
		close(entered)
		<-release
		next := o
		next.MigrationValidation, next.MigrationActivate = false, nil
		next.OnShutdown = func(context.Context) error { shutdown.Add(1); return nil }
		return New(next)
	}
	s, _ := testHost(t, o)
	activation := make(chan error, 1)
	go func() { activation <- s.activateMigration(context.Background()) }()
	<-entered
	closing := make(chan error, 1)
	go func() { closing <- s.Close(context.Background()) }()
	pending(t, closing)
	close(release)
	if err := <-activation; err == nil || !strings.Contains(err.Error(), "closed during migration") {
		t.Fatal("published runtime after shutdown", err)
	}
	receiveError(t, closing)
	if shutdown.Load() != 1 {
		t.Fatal("unpublished activated runtime leaked", shutdown.Load())
	}
}

func TestMigrationQuiescenceJoinsResidentAgentAndCannotCreateAnother(t *testing.T) {
	live := &closingSession{closeStarted: make(chan struct{}), closeRelease: make(chan struct{})}
	o := migrationOptions(t)
	o.Factory = func(context.Context, SessionOptions, Emit) (LiveSession, error) { return live, nil }
	s, host := testHost(t, o)
	id := createTestSession(t, host, o.WorkDir)
	if _, err := s.live(id); err != nil {
		t.Fatal(err)
	}
	preview := migrationRequest(t, host, "GET", "preview", 200)
	if !strings.Contains(string(preview), `"activeTasks":1`) {
		t.Fatal("preview lost actual resident agent", string(preview))
	}
	quiesced := make(chan error, 1)
	go func() { quiesced <- s.quiesceForMigration(context.Background(), false) }()
	<-live.closeStarted
	pending(t, quiesced)
	if _, err := s.live(id); err == nil {
		t.Error("migration reopened resident runtime")
	}
	close(live.closeRelease)
	receiveError(t, quiesced)
	if live.closed.Load() != 1 {
		t.Fatal("native agent was not drained once", live.closed.Load())
	}
}

func TestMigrationInventoryCountsOnlyLiveUnownedNumericRegistrations(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "sessions")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"1.json", "002.json", "3.json", "4.json", "-5.json", "6a.json", "0.json", "1.0.json", "settings.json", "9007199254740992.json"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("unknown"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	count, err := countMigrationProcesses(context.Background(), root, []int{1, 3}, func(pid int) bool { return pid != 4 })
	if err != nil || count != 1 {
		t.Fatal(count, err)
	}
}

func TestMigrationRealListenerKeepsRuntimeCollaborationEndpointAfterActivation(t *testing.T) {
	o := migrationOptions(t)
	o.MigrationValidation = true
	o.MigrationActivate = func(context.Context) (*Server, error) {
		next := o
		next.MigrationValidation, next.MigrationActivate = false, nil
		return New(next)
	}
	s, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- s.Serve(ctx, listener) }()
	defer func() { cancel(); receiveError(t, served) }()
	base := "http://" + listener.Addr().String()
	request, err := http.NewRequest("POST", base+"/api/runtime/migration/activate", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer private-migration-token")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	s.mu.Lock()
	delegate := s.migration.delegate
	s.mu.Unlock()
	if delegate == nil || delegate.collaboration == nil {
		t.Fatal("activation lacks real collaboration host")
	}
	delegate.collaboration.mu.Lock()
	endpoint := delegate.collaboration.endpoint
	delegate.collaboration.mu.Unlock()
	if endpoint != base {
		t.Fatal("activation changed or omitted SDK collaboration endpoint", endpoint, base)
	}
}

func TestMigrationAdmissionRevokesPrivateTeamLaunchBeforeFactoryStarts(t *testing.T) {
	o := migrationOptions(t)
	var factories atomic.Int32
	o.Factory = func(context.Context, SessionOptions, Emit) (LiveSession, error) {
		factories.Add(1)
		return &transportSession{}, nil
	}
	s, host := testHost(t, o)
	id := createTestSession(t, host, o.WorkDir)
	if _, err := s.live(id); err != nil {
		t.Fatal(err)
	}
	if !s.NativeSessionConnected(id) {
		t.Fatal("fixture parent was not connected")
	}
	s.mu.Lock()
	s.migration.active = true
	s.mu.Unlock()
	if s.NativeSessionConnected(id) {
		t.Fatal("maintenance exposed parent to automatic Team continuation")
	}
	_, err := s.PrepareTeamWorker(context.Background(), TeamWorkerLaunch{WorkDir: o.WorkDir, Model: "fixture", Worker: teamworker.Start{ParentSessionID: id}})
	if err == nil || factories.Load() != 1 {
		t.Fatal("private worker started after migration admission closed", err, factories.Load())
	}
}
