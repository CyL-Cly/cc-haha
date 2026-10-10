package pluginmcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// These tests isolate the external bundle-loader contract, not bundle download
// or compiler behavior. The actual mcpb package has its independent tests;
// ordinary producer/storage/filesystem business has actual Source comparisons.
func TestBundleLoaderClassificationNeedsConfigIdentityAndOrderedConcurrentMerge(t *testing.T) {
	s, home, _ := newFixture(t, `{}`, `{}`)
	root := filepath.Join(home, "plugin")
	_ = os.Mkdir(root, 0700)
	plugin := parse(t, `{"name":"bundle","source":"bundle@new","repository":"bundle@old","enabled":true,"manifest":{"mcpServers":["slow.mcpb","fast.dxt","https://owned.invalid/download.mcpb","bad-manifest.mcpb","bad-extract.dxt","needs-config.mcpb"]}}`)
	plugin.Set("path", raw(root))
	var mutex sync.Mutex
	calls := []string{}
	entered := make(chan string, 2)
	release := make(chan struct{})
	s.options.LoadMCPB = func(ctx context.Context, path, pluginPath, id string, onStatus func(string)) (MCPBResult, error) {
		if pluginPath != root || id != "bundle@old" {
			t.Error("MCPB identity/root changed")
		}
		mutex.Lock()
		calls = append(calls, path)
		mutex.Unlock()
		onStatus("owned status")
		switch path {
		case "slow.mcpb":
			entered <- path
			select {
			case <-release:
			case <-ctx.Done():
				return MCPBResult{}, ctx.Err()
			}
			return MCPBResult{Manifest: parse(t, `{"name":"same"}`), Config: parse(t, `{"command":"slow-first"}`)}, nil
		case "fast.dxt":
			entered <- path
			return MCPBResult{Manifest: parse(t, `{"name":"same"}`), Config: parse(t, `{"command":"fast-last"}`)}, nil
		case "https://owned.invalid/download.mcpb":
			return MCPBResult{}, errors.New("download network failed")
		case "bad-manifest.mcpb":
			return MCPBResult{}, errors.New("invalid manifest")
		case "bad-extract.dxt":
			return MCPBResult{}, errors.New("extract failed")
		case "needs-config.mcpb":
			return MCPBResult{NeedsConfig: true}, nil
		default:
			t.Error(path)
			return MCPBResult{}, errors.New("unexpected")
		}
	}
	done := make(chan struct{})
	var result *protocol.JSONObject
	var err error
	failures := []protocol.JSONObject{}
	go func() { result, err = s.LoadPluginMCPServers(context.Background(), plugin, &failures); close(done) }()
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("MCPB specs no longer load concurrently")
		}
	}
	close(release)
	<-done
	if err != nil || result == nil || object(result.Get("same")).String("command") != "fast-last" {
		t.Fatal("ordered merge failed", result, err)
	}
	types := map[string]bool{}
	for _, failure := range failures {
		if failure.String("source") != "bundle@bundle@old" {
			t.Fatal("original error source changed", failure)
		}
		types[failure.String("type")] = true
	}
	if !reflect.DeepEqual(types, map[string]bool{"mcpb-download-failed": true, "mcpb-invalid-manifest": true, "mcpb-extract-failed": true}) || len(failures) != 3 || len(calls) != 6 {
		t.Fatal(types, failures, calls)
	}
}
func TestSharedStorageSaveFailureKeepsPlaintextAndPartialChannelFields(t *testing.T) {
	settings := `{"pluginConfigs":{"sample@mkt":{"mcpServers":{"chat":{"token":"old-plaintext","untouched":"keep"}}}}}`
	s, home, _ := newFixture(t, settings, `{}`)
	credentials := filepath.Join(home, ".claude", ".credentials.json")
	if e := os.Rename(credentials, credentials+".backup"); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(credentials, 0700); e != nil {
		t.Fatal(e)
	}
	err := s.SaveServerUserConfig(context.Background(), "sample@mkt", "chat", parse(t, `{"token":"new-secret"}`), parse(t, `{"token":{"sensitive":true}}`))
	if err == nil {
		t.Fatal("failed credential write reported success")
	}
	value, e := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if e != nil || string(value) != settings {
		t.Fatal("secret failure erased plaintext fallback", string(value), e)
	}
}
func TestExplicitDependenciesCancellationAndDataDirectoryPlatformContract(t *testing.T) {
	if _, e := New(Options{}); e == nil {
		t.Fatal("missing native dependencies accepted")
	}
	s, home, env := newFixture(t, `{}`, `{}`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := s.LoadServerUserConfig(ctx, "id", "server"); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	env["CLAUDE_CODE_PLUGIN_CACHE_DIR"] = "~/owned-plugins"
	path, e := s.DataDirectory("id@mkt/name")
	if e != nil || path != filepath.Join(home, "owned-plugins", "data", "id-mkt-name") {
		t.Fatal(path, e)
	}
	delete(env, "CLAUDE_CODE_PLUGIN_CACHE_DIR")
	s.options.Cowork = true
	path, e = s.DataDirectory("id@mkt")
	if e != nil || path != filepath.Join(home, ".claude", "cowork_plugins", "data", "id-mkt") {
		t.Fatal(path, e)
	}
	s.options.Platform = "win32"
	plugin := parse(t, `{"path":"C:\\owned\\plugin","source":"win@mkt"}`)
	resolved, e := s.Resolve(context.Background(), parse(t, `{"type":"http","url":"https://owned.example/${CLAUDE_PLUGIN_ROOT}"}`), plugin, nil, nil, "win", "server")
	if e != nil || resolved.String("url") != "https://owned.example/C:/owned/plugin" {
		t.Fatal(resolved, e)
	}
}
