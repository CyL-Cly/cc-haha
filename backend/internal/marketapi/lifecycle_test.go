package marketapi

import (
	"bytes"
	"context"
	"encoding/json"
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
)

func TestActualSourceMarketStaleCacheRetryDisableAndRecovery(t *testing.T) {
	var failing atomic.Bool
	var requests atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__control" {
			var body object
			_ = json.NewDecoder(r.Body).Decode(&body)
			failing.Store(body["fail"] == true)
			fmt.Fprint(w, "ok")
			return
		}
		requests.Add(1)
		if failing.Load() {
			http.Error(w, "unavailable", 503)
			return
		}
		fmt.Fprint(w, `{"items":[{"slug":"test","stats":{"downloads":1}}]}`)
	}))
	defer peer.Close()
	disabled, enabled := "clawhub", ""
	start := int64(1800000000000)
	steps := []step{{Path: "/api/market/skills?scope=market&source=clawhub&limit=1"}, {Path: "/api/market/skills?scope=market&source=clawhub&limit=1"}, {Path: "/api/market/skills?scope=market&source=clawhub&limit=1", Now: start + 360001, Control: object{"fail": true}}, {Path: "/api/market/status"}, {Path: "/api/market/skills?scope=market&source=clawhub&limit=1"}, {Path: "/api/market/status"}, {Path: "/api/market/skills?scope=market&source=clawhub&limit=1", Disabled: &disabled}, {Path: "/api/market/skills?scope=market&source=clawhub&limit=1", Disabled: &enabled, Control: object{"fail": false}}, {Path: "/api/market/status"}}
	sourceHome := t.TempDir()
	expected := sourceContract(t, sourceHome, peer.URL, steps)
	sourceRequests := requests.Load()
	if sourceRequests != 6 {
		t.Fatalf("actual Source retry physical count=%d", sourceRequests)
	}
	requests.Store(0)
	failing.Store(false)
	clock := start
	disabledValue := ""
	home := t.TempDir()
	s, e := New(Options{ConfigDir: filepath.Join(home, ".claude"), Now: func() time.Time { return time.UnixMilli(clock) }, Environment: func() map[string]string {
		return map[string]string{"HAHA_MARKET_BASE_CLAWHUB": peer.URL, "HAHA_MARKET_BASE_SKILLHUB": peer.URL, "HAHA_MARKET_DISABLE_PROVIDERS": disabledValue}
	}})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	for i, step := range steps {
		if step.Now != 0 {
			clock = step.Now
		}
		if step.Disabled != nil {
			disabledValue = *step.Disabled
		}
		if step.Control != nil {
			body, _ := json.Marshal(step.Control)
			r, e := peer.Client().Post(peer.URL+"/__control", "application/json", bytes.NewReader(body))
			if e != nil {
				t.Fatal(e)
			}
			io.Copy(io.Discard, r.Body)
			r.Body.Close()
		}
		req := httptest.NewRequest("GET", "http://fixture.invalid"+step.Path, nil)
		rec := httptest.NewRecorder()
		s.HandleHTTP(rec, req, strings.Split(strings.Trim(req.URL.EscapedPath(), "/"), "/"))
		var actual any
		if json.Unmarshal(rec.Body.Bytes(), &actual) != nil {
			t.Fatal(rec.Body.String())
		}
		if rec.Code != expected[i].Status || !reflect.DeepEqual(actual, expected[i].Body) {
			t.Errorf("case%d actual%s Source%v", i, rec.Body.String(), expected[i])
		}
	}
	if requests.Load() != sourceRequests {
		t.Fatalf("Go physical retry/cache count=%d Source%d", requests.Load(), sourceRequests)
	}
}
func TestMarketProtectedDirectoriesAndOriginalMetadata(t *testing.T) {
	for _, row := range []struct {
		name, marker string
		status       int
		code         string
	}{{"manual", "", 409, "MARKET_NOT_MANAGED"}, {"malformed", "{", 409, "MARKET_NOT_MANAGED"}, {"other-source", `{"id":"skillhub:test","source":"skillhub","slug":"test","installedAt":"old","fileCount":0}`, 409, "MARKET_NOT_MANAGED"}, {"invalid-count", `{"id":"clawhub:test","source":"clawhub","slug":"test","installedAt":"old","fileCount":-1}`, 409, "MARKET_NOT_MANAGED"}, {"original-with-future", `{"id":"clawhub:test","source":"clawhub","slug":"test","installedAt":"2026-01-01T00:00:00Z","fileCount":1,"version":"old","future":{"keep":17}}`, 200, ""}} {
		t.Run(row.name, func(t *testing.T) {
			home := t.TempDir()
			config := filepath.Join(home, ".claude")
			dir := filepath.Join(config, "skills/test")
			os.MkdirAll(dir, 0700)
			sentinel := filepath.Join(dir, "SKILL.md")
			os.WriteFile(sentinel, []byte("protected-original"), 0600)
			if row.marker != "" {
				os.WriteFile(filepath.Join(dir, metaFilename), []byte(row.marker), 0600)
			}
			s, e := New(Options{ConfigDir: config, Environment: func() map[string]string {
				return map[string]string{"HAHA_MARKET_DISABLE_PROVIDERS": "clawhub,skillhub"}
			}})
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close(context.Background())
			req := httptest.NewRequest("POST", "http://fixture.invalid/api/market/uninstall", strings.NewReader(`{"id":"clawhub:test"}`))
			rec := httptest.NewRecorder()
			s.HandleHTTP(rec, req, []string{"api", "market", "uninstall"})
			if rec.Code != row.status {
				t.Fatalf("status%d %s", rec.Code, rec.Body.String())
			}
			if row.status == 200 {
				if _, e = os.Stat(dir); !os.IsNotExist(e) {
					t.Fatal("original managed marker not removed")
				}
			} else {
				data, e := os.ReadFile(sentinel)
				if e != nil || string(data) != "protected-original" {
					t.Fatal("protected manual/invalid directory changed")
				}
				var body object
				json.Unmarshal(rec.Body.Bytes(), &body)
				if body["error"] != row.code {
					t.Fatal(body)
				}
			}
		})
	}
}
func TestMarketCloseCancelsActualPendingUpstreamAndJoinsHTTP(t *testing.T) {
	started, aborted := make(chan struct{}), make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-started:
		default:
			close(started)
		}
		<-r.Context().Done()
		select {
		case <-aborted:
		default:
			close(aborted)
		}
	}))
	defer peer.Close()
	s, e := New(Options{ConfigDir: filepath.Join(t.TempDir(), ".claude"), Environment: func() map[string]string { return map[string]string{"HAHA_MARKET_BASE_CLAWHUB": peer.URL} }})
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.HandleHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "http://fixture.invalid/api/market/skills?scope=market&source=clawhub", nil), []string{"api", "market", "skills"})
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("physical HTTP never started")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if e = s.Close(ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case <-aborted:
	case <-ctx.Done():
		t.Fatal("actual upstream not canceled")
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("HTTP did not join")
	}
}
