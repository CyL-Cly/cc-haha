package plugincli_test

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/plugincli"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type ownedPluginTransport struct {
	base *url.URL
	next http.RoundTripper
	mu   sync.Mutex
	urls []string
}

func (r *ownedPluginTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.urls = append(r.urls, request.URL.String())
	r.mu.Unlock()
	clone := request.Clone(request.Context())
	target := *request.URL
	target.Scheme, target.Host = r.base.Scheme, r.base.Host
	clone.URL = &target
	clone.Host = ""
	return r.next.RoundTrip(clone)
}
func TestOriginalCLIExternalCountsAndOfficialGCSActualTLS(t *testing.T) {
	for _, mode := range []string{"counts", "official-update", "official-noop", "official-all"} {
		t.Run(mode, func(t *testing.T) { pairedExternalMarketplace(t, mode) })
	}
}
func pairedExternalMarketplace(t *testing.T, mode string) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var zipWire bytes.Buffer
	archive := zip.NewWriter(&zipWire)
	for _, entry := range []struct {
		name, body string
		mode       os.FileMode
	}{{"marketplaces/claude-plugins-official/.claude-plugin/marketplace.json", `{"name":"claude-plugins-official","owner":{"name":"Fixture"},"plugins":[]}`, 0644}, {"marketplaces/claude-plugins-official/scripts/owned.sh", "#!/bin/sh\nprintf owned\n", 0755}, {"ignored-other-root/file.txt", "ignored", 0644}} {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		header.SetMode(entry.mode)
		writer, e := archive.CreateHeader(header)
		if e != nil {
			t.Fatal(e)
		}
		_, _ = writer.Write([]byte(entry.body))
	}
	_ = archive.Close()
	var headersMu sync.Mutex
	headers := []http.Header{}
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headersMu.Lock()
		headers = append(headers, r.Header.Clone())
		headersMu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "plugin-installs.json"):
			w.Header().Set("Content-Encoding", "x-gzip")
			var wire bytes.Buffer
			encoder := gzip.NewWriter(&wire)
			_, _ = encoder.Write([]byte(`{"plugins":[{"plugin":"beta@fixture","unique_installs":137}]}`))
			_ = encoder.Close()
			_, _ = w.Write(wire.Bytes())
		case strings.HasSuffix(r.URL.Path, "/latest"):
			_, _ = w.Write([]byte("  owned-content-sha\n"))
		case strings.HasSuffix(r.URL.Path, "owned-content-sha.zip"):
			_, _ = w.Write(zipWire.Bytes())
		default:
			w.WriteHeader(404)
		}
	}))
	defer peer.Close()
	ca := filepath.Join(root, "owned-ca.pem")
	if err = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	setup := func(name string) (string, map[string]string) {
		home := filepath.Join(root, name)
		config := filepath.Join(home, ".claude")
		plugins := filepath.Join(config, "plugins")
		market := filepath.Join(home, "market")
		location := filepath.Join(plugins, "marketplaces", "claude-plugins-official")
		for _, dir := range []string{config, plugins, filepath.Join(market, ".claude-plugin"), location} {
			if e := os.MkdirAll(dir, 0700); e != nil {
				t.Fatal(e)
			}
		}
		write := func(path, body string) {
			if e := os.WriteFile(path, []byte(body), 0600); e != nil {
				t.Fatal(e)
			}
		}
		write(filepath.Join(config, "settings.json"), `{"future":{"keep":true}}`)
		known := map[string]any{}
		if mode == "counts" {
			write(filepath.Join(market, ".claude-plugin", "marketplace.json"), `{"name":"fixture","owner":{"name":"Fixture"},"plugins":[{"name":"beta","source":"./beta","description":"TLS count consumer"}]}`)
			known["fixture"] = map[string]any{"source": map[string]string{"source": "directory", "path": market}, "installLocation": market, "lastUpdated": "2024-01-01T00:00:00Z"}
		} else {
			known["claude-plugins-official"] = map[string]any{"source": map[string]string{"source": "github", "repo": "anthropics/claude-plugins-official"}, "installLocation": location, "lastUpdated": "2024-01-01T00:00:00Z"}
			if mode == "official-noop" {
				write(filepath.Join(location, ".gcs-sha"), "owned-content-sha\n")
				write(filepath.Join(location, "keep.txt"), "sentinel no-op kept file\n")
			}
		}
		raw, _ := json.Marshal(known)
		write(filepath.Join(plugins, "known_marketplaces.json"), string(raw))
		env := map[string]string{"HOME": home, "USERPROFILE": home, "USER": "owned", "CLAUDE_CONFIG_DIR": config, "TMPDIR": home, "PATH": "/usr/bin:/bin", "NODE_EXTRA_CA_CERTS": ca, "BUN_OPTIONS": "--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT": root, "CC_HAHA_PLUGIN_CLI_FIXTURE": "1", "NODE_ENV": "production", "USER_TYPE": "external", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "DISABLE_TELEMETRY": "1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH": "1"}
		return home, env
	}
	sourceHome, sourceEnv := setup("source-home")
	goHome, goEnv := setup("go-home")
	input := map[string]any{"action": "marketplaceUpdate", "argument": "claude-plugins-official", "options": map[string]any{}}
	if mode == "counts" {
		input = map[string]any{"action": "list", "options": map[string]any{"json": true, "available": true}}
	}
	if mode == "official-all" {
		delete(input, "argument")
	}
	payload, _ := json.Marshal(input)
	sourceEnv["CC_HAHA_PLUGIN_CLI_INPUT"] = string(payload)
	mapping, _ := json.Marshal(map[string]string{"base": peer.URL})
	sourceEnv["CC_HAHA_PLUGIN_CLI_TRANSPORT_MAP"] = string(mapping)
	fixture, _ := filepath.Abs("../../testdata/native-cli-plugins.fixture.ts")
	profile, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	source := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", profile, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	source.Dir = sourceHome
	for k, v := range sourceEnv {
		source.Env = append(source.Env, k+"="+v)
	}
	var sourceErr bytes.Buffer
	source.Stderr = &sourceErr
	stdout, e := source.Output()
	if e != nil {
		t.Fatal("actual original external consumer", e, sourceErr.String())
	}
	native, e := newIsolatedProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: goEnv, CWD: goHome, Platform: "darwin", ManagedRoot: filepath.Join(root, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	if e != nil {
		t.Fatal(e)
	}
	main, e := clibootstrap.NewMainCommand(clibootstrap.MainCommandOptions{Arguments: []string{"plugin", "marketplace", "list"}, Native: native, Platform: "darwin", PID: os.Getpid(), Output: &bytes.Buffer{}, Error: &bytes.Buffer{}})
	if e != nil {
		t.Fatal(e)
	}
	defer main.Close()
	base, _ := url.Parse(peer.URL)
	transport := &ownedPluginTransport{base: base, next: peer.Client().Transport}
	resolver, e := pluginsapi.NewMarketplaceResolver(pluginsapi.MarketplaceOptions{Home: goHome, WorkDir: goHome, PluginsDir: filepath.Join(goHome, ".claude/plugins"), Environment: goEnv, Store: storage.New(storage.Options{}), Client: &http.Client{Transport: transport}, Now: time.Now})
	if e != nil {
		t.Fatal(e)
	}
	manager, e := pluginsapi.NewMarketplaceManager(pluginsapi.MarketplaceManagerOptions{Resolver: resolver, ReadSettings: func(ctx context.Context, scope, cwd string) (protocol.JSONObject, error) {
		value, e := main.Startup.GetSettingsForSource(map[string]string{"managed": "policySettings", "user": "userSettings", "project": "projectSettings", "local": "localSettings"}[scope])
		if value == nil {
			return protocol.NewJSONObject(), e
		}
		return value.Clone(), e
	}, WriteSettings: func(ctx context.Context, scope string, value protocol.JSONObject, undefined [][]string) error {
		if scope != "user" || len(undefined) > 0 {
			return fmt.Errorf("unexpected management source writer %s", scope)
		}
		return main.Settings.WriteUserSettings(ctx, clisettings.Patch{Object: value})
	}, ClearCaches: func(ctx context.Context) error { _, e := main.Settings.RefreshPlugins(ctx, false); return e }})
	if e != nil {
		t.Fatal(e)
	}
	var got plugincli.Result
	e = main.Run(ctx, func(scope *cooperative.Scope) error {
		raw, e := scope.AwaitBytes(cooperative.NetworkOpen, func(worker context.Context) ([]byte, error) {
			if mode == "counts" {
				registry, e := pluginregistry.NewInstalledRegistry(pluginregistry.InstalledOptions{PluginsDir: filepath.Join(goHome, ".claude/plugins"), Store: storage.New(storage.Options{})})
				if e != nil {
					return nil, e
				}
				installed, e := registry.Session(worker)
				if e != nil {
					return nil, e
				}
				available, e := manager.Available(worker, installed)
				if e != nil {
					return nil, e
				}
				return json.Marshal(plugincli.Result{Stdout: func() string {
					value := protocol.NewJSONObject(testField("installed", []any{}), testField("available", available))
					data, _ := value.MarshalJSON()
					var out bytes.Buffer
					_ = json.Indent(&out, data, "", "  ")
					return out.String() + "\n"
				}()})
			}
			command := plugincli.Command{Action: "marketplace-update", Argument: "claude-plugins-official"}
			if mode == "official-all" {
				command.Argument = ""
			}
			result, e := plugincli.RunMarketplace(worker, command, manager, goHome, goHome, "darwin")
			if e != nil {
				return nil, e
			}
			return json.Marshal(result)
		})
		if e != nil {
			return e
		}
		return json.Unmarshal(raw, &got)
	})
	if e != nil {
		t.Fatal(e)
	}
	if got.ExitCode != 0 || got.Stderr != sourceErr.String() || got.Stdout != string(stdout) {
		t.Fatalf("actual Source stdout%q stderr%q Go%+v", stdout, sourceErr.String(), got)
	}
	originalRequests, e := os.ReadFile(filepath.Join(sourceHome, "owned-original-plugin-transport.ndjson"))
	if e != nil {
		t.Fatal(e)
	}
	originalURLs := []string{}
	for _, line := range bytes.Split(bytes.TrimSpace(originalRequests), []byte("\n")) {
		var row map[string]string
		if e = json.Unmarshal(line, &row); e != nil {
			t.Fatal(e)
		}
		originalURLs = append(originalURLs, row["requested"])
	}
	transport.mu.Lock()
	urls := append([]string{}, transport.urls...)
	transport.mu.Unlock()
	if !reflect.DeepEqual(originalURLs, urls) {
		t.Fatalf("original producer URLs Source%v Go%v", originalURLs, urls)
	}
	headersMu.Lock()
	captured := append([]http.Header{}, headers...)
	headersMu.Unlock()
	if len(captured) != 2*len(originalURLs) {
		t.Fatal("actual TLS requests count", len(captured), len(originalURLs))
	}
	for i := range originalURLs {
		for _, key := range []string{"Accept", "Accept-Encoding", "User-Agent"} {
			if captured[i].Get(key) != captured[i+len(originalURLs)].Get(key) {
				t.Fatalf("actual original external %s headers Source%q Go%q", key, captured[i].Get(key), captured[i+len(originalURLs)].Get(key))
			}
		}
	}
	if mode == "counts" {
		for _, home := range []string{sourceHome, goHome} {
			raw, e := os.ReadFile(filepath.Join(home, ".claude/plugins/install-counts-cache.json"))
			if e != nil {
				t.Fatal(e)
			}
			var value map[string]any
			_ = json.Unmarshal(raw, &value)
			if value["version"] != float64(1) || len(value["counts"].([]any)) != 1 {
				t.Fatal("actual persisted count transport", value)
			}
		}
	} else {
		for _, relative := range func() []string {
			if mode == "official-noop" {
				return []string{".gcs-sha", "keep.txt"}
			}
			return []string{".gcs-sha", ".claude-plugin/marketplace.json", "scripts/owned.sh"}
		}() {
			a, e := os.ReadFile(filepath.Join(sourceHome, ".claude/plugins/marketplaces/claude-plugins-official", relative))
			if e != nil {
				t.Fatal(e)
			}
			b, e := os.ReadFile(filepath.Join(goHome, ".claude/plugins/marketplaces/claude-plugins-official", relative))
			if e != nil {
				t.Fatal(e)
			}
			if !bytes.Equal(a, b) {
				t.Fatalf("actual mirror physical %s Source%q Go%q", relative, a, b)
			}
			aInfo, _ := os.Stat(filepath.Join(sourceHome, ".claude/plugins/marketplaces/claude-plugins-official", relative))
			bInfo, _ := os.Stat(filepath.Join(goHome, ".claude/plugins/marketplaces/claude-plugins-official", relative))
			if aInfo.Mode().Perm() != bInfo.Mode().Perm() {
				t.Fatal("actual executable mode", relative, aInfo.Mode(), bInfo.Mode())
			}
		}
	}
}
