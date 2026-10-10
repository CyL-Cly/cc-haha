package releasenotes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestReleaseCacheCommandActualSourceHTTPDiskTimeoutAndLateCompletion(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	old := "## 1.0.0\n- cached\n"
	fresh := "## 2.0.0\n- fetched\n"
	type scenario struct {
		Name           string
		Cached         *string
		Body           string
		Status         int
		Delay          time.Duration
		NonInteractive bool
		Essential      string
		Prime          bool
	}
	scenarios := []scenario{{"fresh", nil, fresh, 200, 0, false, "", false}, {"cached_failure", &old, "failure", 500, 0, false, "", false}, {"fallback", nil, "failure", 500, 0, false, "", false}, {"noninteractive", &old, fresh, 200, 0, true, "", false}, {"essential_0_is_truthy", &old, fresh, 200, 0, false, "0", false}, {"unchanged", &old, old, 200, 0, false, "", true}, {"disk_not_primed", &old, old, 200, 0, false, "", false}, {"late_write", &old, fresh, 200, 750 * time.Millisecond, false, "", false}, {"late_first", nil, fresh, 200, 750 * time.Millisecond, false, "", false}, {"status_201", &old, fresh, 201, 0, false, "", false}, {"json_object", &old, `{"not":"a changelog string"}`, 200, 0, false, "", false}, {"json_string", nil, `"## 3.0.0\n- JSON string\n"`, 200, 0, false, "", false}}
	for _, row := range scenarios {
		t.Run(row.Name, func(t *testing.T) {
			var hits atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				select {
				case <-time.After(row.Delay):
				case <-r.Context().Done():
					return
				}
				w.WriteHeader(row.Status)
				_, _ = w.Write([]byte(row.Body))
			}))
			defer peer.Close()
			home := t.TempDir()
			fixture, _ := filepath.Abs("testdata/release-cache.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_RELEASE_CACHE_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NODE_ENV=production"}
			wait := 0
			if row.Delay > 0 {
				wait = 400
			}
			input, _ := json.Marshal(map[string]any{"home": home, "url": peer.URL, "cached": row.Cached, "nonInteractive": row.NonInteractive, "essential": row.Essential, "prime": row.Prime, "waitAfter": wait})
			cmd.Stdin = bytes.NewReader(input)
			raw, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatal(err, string(raw))
			}
			var source struct {
				First     struct{ Type, Value string }
				Elapsed   float64
				Disk      *string
				Memory    string
				Timestamp *float64
				Future    string
			}
			if err = json.Unmarshal(raw, &source); err != nil {
				t.Fatal(err, string(raw))
			}
			sourceHits := hits.Swap(0)
			native := t.TempDir()
			cache := filepath.Join(native, "cache", "changelog.md")
			if row.Cached != nil {
				_ = os.MkdirAll(filepath.Dir(cache), 0755)
				_ = os.WriteFile(cache, []byte(*row.Cached), 0644)
			}
			var timestamp atomic.Int64
			service, err := New(Options{CachePath: cache, ReadFile: os.ReadFile, MkdirAll: func(s string) error { return os.MkdirAll(s, 0755) }, WriteFile: func(s string, data []byte) error { return os.WriteFile(s, data, 0644) }, Fetch: HTTPFetcher(peer.Client(), peer.URL), SaveTimestamp: func(n float64) error { timestamp.Store(int64(n)); return nil }, NowMillis: func() float64 { return 1712345678000 }})
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			if row.Prime {
				service.Stored()
			}
			started := time.Now()
			text, err := service.Command(ctx, row.NonInteractive, row.Essential != "")
			elapsed := time.Since(started)
			if wait > 0 {
				time.Sleep(time.Duration(wait) * time.Millisecond)
			}
			var disk *string
			if data, e := os.ReadFile(cache); e == nil {
				value := string(data)
				disk = &value
			}
			eq := func(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }
			wantTimestamp := int64(0)
			if source.Timestamp != nil {
				wantTimestamp = int64(*source.Timestamp)
			}
			if err != nil || source.First.Type != "text" || text != source.First.Value || !eq(disk, source.Disk) || service.Memory() != source.Memory || timestamp.Load() != wantTimestamp || hits.Load() != sourceHits || source.Future != "preserved" {
				t.Fatalf("Go text%q disk%v mem%q timestamp%d hits%d err%v Source%s", text, disk, service.Memory(), timestamp.Load(), hits.Load(), err, raw)
			}
			if row.Delay > 0 && (elapsed < 450*time.Millisecond || elapsed > 700*time.Millisecond || source.Elapsed < 450 || source.Elapsed > 700) {
				t.Error("race did not return before independent fetch", elapsed, source.Elapsed)
			}
		})
	}
}

func TestShutdownCancelsAndJoinsActualPendingHTTP(t *testing.T) {
	entered := make(chan struct{})
	ended := make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done(); close(ended) }))
	defer peer.Close()
	s, err := New(Options{CachePath: filepath.Join(t.TempDir(), "cache"), ReadFile: os.ReadFile, MkdirAll: func(string) error { return nil }, WriteFile: func(string, []byte) error { return nil }, Fetch: HTTPFetcher(peer.Client(), peer.URL), SaveTimestamp: func(float64) error { return fmt.Errorf("canceled HTTP must not save") }, NowMillis: func() float64 { return 1 }})
	if err != nil {
		t.Fatal(err)
	}
	text, err := s.Command(context.Background(), false, false)
	if err != nil || text != "See the full changelog at: "+ChangelogURL {
		t.Fatal(text, err)
	}
	<-entered
	s.Close()
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("native HTTP survived process shutdown")
	}
	if _, err = s.Command(context.Background(), true, true); err == nil {
		t.Fatal("closed producer accepted another command")
	}
}
