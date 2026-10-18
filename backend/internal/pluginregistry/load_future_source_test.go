package pluginregistry

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestCachedPluginProducerSamplesSyncFlagInsideDistinctMemoizedWrapper(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual pinned Source Bun required")
	}
	for _, firstSync := range []bool{false, true} {
		name := "cached-first"
		if firstSync {
			name = "full-first"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			f := newCacheFixture(t)
			put(t, filepath.Join(f.home, ".claude", "plugins", "known_marketplaces.json"), string(raw(map[string]any{"mkt": map[string]any{"source": map[string]string{"source": "directory", "path": f.market}, "installLocation": f.market, "lastUpdated": "2026-10-01T00:00:00.000Z"}})))
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-query-init-catalog.fixture.ts")
			command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			command.Dir = f.home
			if err := os.MkdirAll(filepath.Join(f.home, "tmp"), 0700); err != nil {
				t.Fatal(err)
			}
			command.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + filepath.Join(f.home, ".claude"), "TMPDIR=" + filepath.Join(f.home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_QUERY_INIT_CATALOG_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "ANTHROPIC_API_KEY=offline-query-catalog", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "PATH=" + filepath.Join(f.home, "bin")}
			input, _ := json.Marshal(map[string]any{"home": f.home, "peer": "http://127.0.0.1:1", "pluginFlagProfile": map[string]bool{"first": firstSync}})
			command.Stdin = bytes.NewReader(input)
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			if err := command.Run(); err != nil {
				t.Fatalf("Source actual cache-only wrapper %v %s %s", err, stdout.String(), stderr.String())
			}
			var source struct {
				SameBeforeCompletion, WrapperDistinct, ReplacedByFullWarm bool
				Blocked                                                   []string
			}
			if err := json.Unmarshal(stdout.Bytes(), &source); err != nil || len(source.Blocked) > 0 || !source.SameBeforeCompletion || !source.WrapperDistinct || !source.ReplacedByFullWarm {
				t.Fatalf("Source actual identity/flag %v %s", err, stdout.String())
			}
			flag := "0"
			if firstSync {
				flag = "1"
			}
			f.options.Environment = func(key string) string {
				if key == "CLAUDE_CODE_SYNC_PLUGIN_INSTALL" {
					return flag
				}
				return ""
			}
			f.options.Prepare = func(_ context.Context, _ protocol.JSONObject, _, _ string, _ protocol.JSONObject) (Prepared, error) {
				return Prepared{Path: filepath.Join(f.market, "plugins", "sample")}, nil
			}
			loader, err := New(f.options)
			if err != nil {
				t.Fatal(err)
			}
			first := loader.CachedProducer()
			if firstSync {
				flag = "0"
			} else {
				flag = "1"
			}
			if loader.CachedProducer() != first {
				t.Fatal("warm caller re-sampled changed SYNC flag")
			}
			var full *LoadFuture
			if firstSync {
				full = loader.FullProducer()
				if first == full || first.DelegatedProducer() != full {
					t.Fatal("cache-only lost distinct actual async return wrapper")
				}
			}
			if _, err = first.Load(ctx); err != nil {
				t.Fatal(err)
			}
			current := loader.CachedProducer()
			if firstSync {
				if current == first || current == full {
					t.Fatal("full completion did not install distinct cached warm operation")
				}
			} else if current != first {
				t.Fatal("flag flip changed already memoized cache-only operation")
			}
		})
	}
}
