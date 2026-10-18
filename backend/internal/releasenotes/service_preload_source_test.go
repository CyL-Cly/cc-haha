package releasenotes

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestReleasePreloadActualSourceCachedReturnIndependentHTTPAndGuards(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("pinned actual Source required")
	}
	old, fresh := "## 1.0.0\n- cached\n", "## 2.0.0\n- fetched\n"
	type scenario struct {
		name, essential, previous, body string
		cached                          *string
		status                          int
		nonInteractive                  bool
	}
	rows := []scenario{
		{"version-changed", "", "1.0.0", fresh, &old, 200, false},
		{"empty-cache-same-version", "", "2.0.0", fresh, nil, 200, false},
		{"same-version-warm", "", "2.0.0", fresh, &old, 200, false},
		{"noninteractive", "", "1.0.0", fresh, &old, 200, true},
		{"essential-zero-is-truthy", "0", "1.0.0", fresh, &old, 200, false},
		{"essential-false-is-truthy", "false", "1.0.0", fresh, &old, 200, false},
		{"unchanged", "", "1.0.0", old, &old, 200, false},
		{"http-failure", "", "1.0.0", "failure", &old, 500, false},
		{"status-not-200", "", "1.0.0", fresh, &old, 201, false},
		{"non-string-body", "", "1.0.0", `{"not":"a changelog"}`, &old, 200, false},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			var hits atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				select {
				case <-time.After(200 * time.Millisecond):
				case <-r.Context().Done():
					return
				}
				w.WriteHeader(row.status)
				_, _ = w.Write([]byte(row.body))
			}))
			defer peer.Close()
			home := t.TempDir()
			fixture, err := filepath.Abs("testdata/release-cache.fixture.ts")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			command.Dir = home
			command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_RELEASE_CACHE_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NODE_ENV=production"}
			input, err := json.Marshal(map[string]any{"home": home, "url": peer.URL, "cached": row.cached, "nonInteractive": row.nonInteractive, "essential": row.essential, "preload": true, "previousVersion": row.previous, "version": "2.0.0", "waitAfter": 400})
			if err != nil {
				t.Fatal(err)
			}
			command.Stdin = bytes.NewReader(input)
			raw, err := command.CombinedOutput()
			if err != nil {
				t.Fatal(err, string(raw))
			}
			var source struct {
				First struct {
					HasReleaseNotes bool
					ReleaseNotes    []string
				}
				Elapsed               float64
				Disk                  *string
				InitialMemory, Memory string
				Timestamp             *float64
				Future                string
			}
			if err = json.Unmarshal(raw, &source); err != nil {
				t.Fatal(err, string(raw))
			}
			sourceHits := hits.Swap(0)
			cache := filepath.Join(t.TempDir(), "cache", "changelog.md")
			if row.cached != nil {
				if err = os.MkdirAll(filepath.Dir(cache), 0700); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(cache, []byte(*row.cached), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var timestamp atomic.Int64
			service, err := New(Options{CachePath: cache, ReadFile: os.ReadFile, MkdirAll: func(path string) error { return os.MkdirAll(path, 0700) }, WriteFile: func(path string, data []byte) error { return os.WriteFile(path, data, 0600) }, Fetch: HTTPFetcher(peer.Client(), peer.URL), SaveTimestamp: func(value float64) error { timestamp.Store(int64(value)); return nil }, NowMillis: func() float64 { return 1712345678000 }})
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			started := time.Now()
			initial := service.Preload(row.nonInteractive, row.essential != "", row.previous != "2.0.0")
			elapsed := time.Since(started)
			// Join the actual independent producer without canceling it. This is
			// distinct from Command's 500ms race and from process shutdown.
			service.wg.Wait()
			var disk *string
			if data, e := os.ReadFile(cache); e == nil {
				value := string(data)
				disk = &value
			}
			wantTimestamp := int64(0)
			if source.Timestamp != nil {
				wantTimestamp = int64(*source.Timestamp)
			}
			diskEqual := disk == nil && source.Disk == nil || disk != nil && source.Disk != nil && *disk == *source.Disk
			if initial != source.InitialMemory || service.Memory() != source.Memory || !diskEqual || timestamp.Load() != wantTimestamp || hits.Load() != sourceHits || source.Future != "preserved" {
				t.Fatalf("Go initial%q memory%q disk%v timestamp%d hits%d Source%s", initial, service.Memory(), disk, timestamp.Load(), hits.Load(), raw)
			}
			if sourceHits > 0 && (elapsed >= 150*time.Millisecond || source.Elapsed >= 150) {
				t.Fatal("startup waited for independent 200ms HTTP", elapsed, source.Elapsed)
			}
		})
	}
}

func TestReleasePreloadShutdownCancelsAndJoinsPendingPhysicalHTTP(t *testing.T) {
	entered, ended := make(chan struct{}), make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(ended)
	}))
	defer peer.Close()
	cache := filepath.Join(t.TempDir(), "cache", "changelog.md")
	s, err := New(Options{CachePath: cache, ReadFile: os.ReadFile, MkdirAll: func(string) error { t.Error("canceled refresh made directory"); return nil }, WriteFile: func(string, []byte) error { t.Error("canceled refresh wrote cache"); return nil }, Fetch: HTTPFetcher(peer.Client(), peer.URL), SaveTimestamp: func(float64) error { t.Error("canceled refresh saved timestamp"); return nil }, NowMillis: func() float64 { return 1 }})
	if err != nil {
		t.Fatal(err)
	}
	if initial := s.Preload(false, false, true); initial != "" {
		t.Fatal("absent cache populated before HTTP", initial)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("startup did not start physical HTTP")
	}
	s.Close()
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("startup HTTP survived process shutdown")
	}
	if _, err = os.Stat(cache); !os.IsNotExist(err) || s.Memory() != "" {
		t.Fatal("shutdown changed cached producer", err, s.Memory())
	}
}
