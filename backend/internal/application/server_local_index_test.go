package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/localindex"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestDesktopFactorySearchUsesActualPrimaryContentFilesAndRebuild(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("non-model index/search requested a provider")
		http.Error(w, "model forbidden", 500)
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	project := "disposable-project"
	path := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "projects", project, o.Arguments.SessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	entry := func(id, content string) []byte {
		return append(raw(map[string]any{"type": "user", "uuid": id, "sessionId": o.Arguments.SessionID, "cwd": o.WorkDir, "timestamp": "2026-10-07T00:00:00.000Z", "message": map[string]any{"role": "user", "content": content}}), '\n')
	}
	content := append(entry("original", "PRIMARY_REAL_NEEDLE"), []byte(`{"type":"custom-title","customTitle":"Actual indexed custom title"}`+"\n")...)
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(o.WorkDir, "search-source.txt"), []byte("WORKSPACE_REAL_NEEDLE\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewDesktopServer(ServerOptions{HomeDir: o.Environment["HOME"], ConfigDir: o.Environment["CLAUDE_CONFIG_DIR"], WorkDir: o.WorkDir, Environment: o.Environment, Arguments: o.Arguments, Version: o.Version, NativeFactory: func(map[string]string, string, string) (*clibootstrap.NativeBoundary, error) { return o.Native, nil }})
	if err != nil {
		t.Fatal(err)
	}
	host := httptest.NewServer(s)
	defer host.Close()
	defer s.Close(context.Background())
	request := func(method, path string, body any) protocol.JSONObject {
		t.Helper()
		var payload []byte
		if body != nil {
			payload = raw(body)
		}
		req, err := http.NewRequest(method, host.URL+path, bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		res, err := host.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("actual factory %s %s: %d %s", method, path, res.StatusCode, data)
		}
		return obj(data)
	}
	request("GET", "/health", nil)
	deadline := time.Now().Add(15 * time.Second)
	for {
		status := request("GET", "/api/diagnostics/local-index", nil)
		if status.String("state") == "ready" {
			if string(status.Get("indexed")) != "1" {
				t.Fatal("primary diagnostics read a different producer", status)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("primary never became ready", status)
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, q := range []struct {
		path   string
		body   any
		marker string
	}{{"/api/search/sessions", map[string]any{"query": "PRIMARY_REAL_NEEDLE", "project": project}, "PRIMARY_REAL_NEEDLE"}, {"/api/search", map[string]any{"query": "WORKSPACE_REAL_NEEDLE", "cwd": o.WorkDir}, "WORKSPACE_REAL_NEEDLE"}} {
		response := request("POST", q.path, q.body)
		data, _ := response.MarshalJSON()
		if !bytes.Contains(data, []byte(q.marker)) {
			t.Fatalf("actual canonical search missing %s: %s", q.marker, data)
		}
	}
	// Appending real canonical bytes must be visible even before watcher catchup;
	// the index adapters verify stale fingerprints and fall back to the file.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Write(entry("appended", "APPEND_REAL_NEEDLE"))
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	data, _ := request("POST", "/api/search/sessions", map[string]any{"query": "APPEND_REAL_NEEDLE", "project": project}).MarshalJSON()
	if !bytes.Contains(data, []byte("APPEND_REAL_NEEDLE")) {
		t.Fatal("append not found through actual search", string(data))
	}
	request("POST", "/api/diagnostics/local-index/rebuild", map[string]any{})
	backups := []string{}
	_ = filepath.Walk(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "cc-haha"), func(p string, i os.FileInfo, e error) error {
		if e == nil && !i.IsDir() && strings.Contains(p, "backup") {
			backups = append(backups, p)
		}
		return nil
	})
	if len(backups) == 0 {
		t.Fatal("actual rebuild did not preserve original database family")
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal("duplicate close", err)
	}
	if data, err := os.ReadFile(path); err != nil || !strings.Contains(string(data), "APPEND_REAL_NEEDLE") {
		t.Fatal("closing/rebuild modified canonical transcript", fmt.Sprint(err))
	}
	// Keep strict JSON checks separate from byte-content checks above.
	if !json.Valid(data) {
		t.Fatal("search response was not JSON")
	}
}

func TestFailedPrimaryStillStartsContentAndExpiredCloseJoinsActualIndexes(t *testing.T) {
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	primaryPath := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "cc-haha", "db", "index-v1.sqlite")
	// A real invalid native path causes the primary producer to degrade. The
	// separate content database remains safe and usable, as in Source startup.
	if err := os.MkdirAll(primaryPath, 0700); err != nil {
		t.Fatal(err)
	}
	d, err := newDesktopLocalIndexes(ServerOptions{HomeDir: o.Environment["HOME"], ConfigDir: o.Environment["CLAUDE_CONFIG_DIR"], WorkDir: o.WorkDir, Environment: o.Environment}, o.Native)
	if err != nil {
		t.Fatal(err)
	}
	defer d.close(context.Background())
	d.start()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	select {
	case <-d.done:
	case <-ctx.Done():
		t.Fatal("background startup did not settle", ctx.Err())
	}
	if d.primary.Status().State != "degraded" {
		t.Fatal("failed primary falsely ready", d.primary.Status())
	}
	if err := d.content.WaitInitial(ctx); err != nil {
		t.Fatal(err)
	}
	if d.content.Status().State != "ready" {
		t.Fatal("primary failure suppressed independent content startup", d.content.Status())
	}
	expired, stop := context.WithCancel(context.Background())
	stop()
	if err := d.close(expired); !errors.Is(err, context.Canceled) {
		t.Fatal("expired close result changed", err)
	}
	if result, err := d.content.Query(context.Background(), "needle", localindex.ContentQueryOptions{}); err != nil || result != nil {
		t.Fatal("deadline close leaked live content coordinator", result, err)
	}
	if d.primary.ConsumerAvailable() {
		t.Fatal("deadline close leaked primary coordinator")
	}
}
