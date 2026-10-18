package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestDesktopServerDefaultConfigLazyMCPAndConnectorsRoutes(t *testing.T) {
	for _, name := range []string{"absent", "absolute_override"} {
		t.Run(name, func(t *testing.T) {
			o := nativeSessionOptions(t, "http://127.0.0.1:1")
			home := o.Environment["HOME"]
			defaultConfig, overrideConfig := filepath.Join(home, ".claude"), filepath.Join(home, "explicit-config")
			config, expectedMCP, expectedVersion := defaultConfig, "default-config-only", "1.0.1"
			if name == "absolute_override" {
				config, expectedMCP, expectedVersion = overrideConfig, "override-config-only", "2.0.2"
				o.Environment["CLAUDE_CONFIG_DIR"] = config
			} else {
				delete(o.Environment, "CLAUDE_CONFIG_DIR")
			}
			o.Environment["ENABLE_CLAUDEAI_MCP_SERVERS"] = "0"
			o.Environment["CC_HAHA_DISABLE_TERMINAL_SHELL_ENV"] = "1"
			originalEnvironment := make(map[string]string, len(o.Environment))
			for key, value := range o.Environment {
				originalEnvironment[key] = value
			}
			write := func(path string, value any) []byte {
				t.Helper()
				data, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				return data
			}
			for _, root := range []string{defaultConfig, overrideConfig} {
				write(filepath.Join(root, "settings.json"), map[string]any{"enabledPlugins": map[string]any{}, "disableAgentSkillsDirectory": true})
			}
			// Source's default global config is HOME/.claude.json. An explicit
			// override uses CONFIG/.claude.json; resolving the physical default
			// directory must not silently change that original presence decision.
			globalFiles := map[string]string{
				filepath.Join(home, ".claude.json"):           "default-config-only",
				filepath.Join(overrideConfig, ".claude.json"): "override-config-only",
				filepath.Join(defaultConfig, ".claude.json"):  "wrong-default-explicitness",
			}
			for path, server := range globalFiles {
				write(path, map[string]any{"futureLazyConfigProbe": server, "mcpServers": map[string]any{server: map[string]any{"command": "unused-list-only-command"}}})
			}
			stateFiles := map[string][]byte{}
			for root, version := range map[string]string{defaultConfig: "1.0.1", overrideConfig: "2.0.2"} {
				path := filepath.Join(root, "connectors", "state.json")
				stateFiles[path] = write(path, map[string]any{"schemaVersion": 1, "futureLazyConfigProbe": version, "connectors": map[string]any{"wecom": map[string]any{"installed": true, "enabled": false, "installedVersion": version}}})
			}
			var mu sync.Mutex
			var boundaries []*clibootstrap.NativeBoundary
			var securityCalls atomic.Int32
			service, err := NewDesktopServer(ServerOptions{
				Environment: o.Environment, HomeDir: home, ConfigDir: config,
				WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments,
				RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) {
					securityCalls.Add(1)
					return nil, os.ErrPermission
				},
				NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
					native, err := newIsolatedProcessNativeBoundary(clibootstrap.NativeProcessOptions{
						Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot,
						ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil },
					})
					if err == nil {
						mu.Lock()
						boundaries = append(boundaries, native)
						mu.Unlock()
					}
					return native, err
				},
			})
			if err != nil {
				t.Fatalf("NewDesktopServer before lazy routes (%s): %v", name, err)
			}
			t.Cleanup(func() {
				if err := service.Close(context.Background()); err != nil {
					t.Error("Close actual lazy server:", err)
				}
			})
			host := httptest.NewServer(service)
			t.Cleanup(host.Close)
			get := func(t *testing.T, path string) protocol.JSONObject {
				t.Helper()
				response, err := host.Client().Get(host.URL + path)
				if err != nil {
					t.Fatal(err)
				}
				body, readErr := io.ReadAll(response.Body)
				closeErr := response.Body.Close()
				if readErr != nil || closeErr != nil || response.StatusCode != http.StatusOK {
					t.Fatalf("actual lazy GET %s (%s): status=%d body=%s read=%v close=%v", path, name, response.StatusCode, body, readErr, closeErr)
				}
				var value protocol.JSONObject
				if err := value.UnmarshalJSON(body); err != nil {
					t.Fatal("actual lazy route JSON:", err)
				}
				return value
			}
			t.Run("mcp", func(t *testing.T) {
				value := get(t, "/api/mcp?cwd="+url.QueryEscape(o.WorkDir))
				var servers []protocol.JSONObject
				if err := json.Unmarshal(value.Get("servers"), &servers); err != nil || len(servers) != 1 || servers[0].String("name") != expectedMCP {
					t.Fatalf("MCP mixed default/override/global-presence roots: %s err=%v", raw(value), err)
				}
			})
			t.Run("connectors", func(t *testing.T) {
				value := get(t, "/api/connectors")
				var items []protocol.JSONObject
				if err := json.Unmarshal(value.Get("items"), &items); err != nil {
					t.Fatal(err)
				}
				found := false
				for _, item := range items {
					if item.String("id") == "wecom" {
						found = true
						if item.String("installedVersion") != expectedVersion || string(item.Get("installed")) != "true" || string(item.Get("enabled")) != "false" {
							t.Fatalf("Connectors mixed default/override state: %s", raw(item))
						}
					}
				}
				if !found {
					t.Fatal("actual Connectors catalog omitted wecom")
				}
			})
			if securityCalls.Load() != 0 {
				t.Fatal("read-only lazy catalogs invoked OS credential primitive")
			}
			mu.Lock()
			observed := append([]*clibootstrap.NativeBoundary(nil), boundaries...)
			mu.Unlock()
			if len(observed) < 2 || !reflect.DeepEqual(o.Environment, originalEnvironment) {
				t.Fatal("lazy routes did not retain distinct actual boundaries and the raw launch environment")
			}
			for _, native := range observed {
				if native.ConfigHomeDirectory() != config || native.ConfigDirExplicit != (name == "absolute_override") || !reflect.DeepEqual(native.Environment, originalEnvironment) {
					t.Fatalf("lazy route changed physical/default authority or raw config presence: config=%q explicit=%v", native.ConfigHomeDirectory(), native.ConfigDirExplicit)
				}
			}
			for path, expected := range stateFiles {
				actual, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(actual, expected) {
					t.Fatalf("catalog changed retained Connector state %s: %v", path, err)
				}
			}
			for path, marker := range globalFiles {
				data, err := os.ReadFile(path)
				if err != nil || obj(data).String("futureLazyConfigProbe") != marker {
					t.Fatalf("catalog lost unrelated global state %s: %v", path, err)
				}
			}
		})
	}
}
