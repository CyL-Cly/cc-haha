package pluginmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginoptions"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func parse(t *testing.T, text string) protocol.JSONObject {
	t.Helper()
	var o protocol.JSONObject
	if e := o.UnmarshalJSON([]byte(text)); e != nil {
		t.Fatal(e)
	}
	return o
}
func newFixture(t *testing.T, settings, credentials string) (*Service, string, map[string]string) {
	t.Helper()
	home := t.TempDir()
	home, e := filepath.EvalSymlinks(home)
	if e != nil {
		t.Fatal(e)
	}
	config := filepath.Join(home, ".claude")
	for _, path := range []string{config, filepath.Join(home, "tmp"), filepath.Join(home, "managed")} {
		if e = os.MkdirAll(path, 0700); e != nil {
			t.Fatal(e)
		}
	}
	for path, content := range map[string]string{filepath.Join(config, "settings.json"): settings, filepath.Join(config, ".credentials.json"): credentials} {
		if e = os.WriteFile(path, []byte(content), 0600); e != nil {
			t.Fatal(e)
		}
	}
	clock := func() float64 { return 1700000000000 }
	state, e := clisettings.NewState(clock)
	if e != nil {
		t.Fatal(e)
	}
	cli, e := clisettings.NewService(clisettings.Options{State: state, ConfigHome: config, OriginalCWD: home, Profile: clisettings.NativeProcessProfile, Now: clock, PID: 12345})
	if e != nil {
		t.Fatal(e)
	}
	read := func(ctx context.Context) (protocol.JSONObject, error) {
		if e := ctx.Err(); e != nil {
			return protocol.JSONObject{}, e
		}
		value, e := cli.GetEditableSource("userSettings", "")
		if value == nil {
			return protocol.NewJSONObject(), e
		}
		return value.Clone(), e
	}
	write := func(ctx context.Context, patch clisettings.Patch) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		result, e := cli.UpdateEditableSource("userSettings", "", patch)
		if e != nil {
			return e
		}
		return result.Error
	}
	env := map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": config}
	store, e := securestorage.New(securestorage.Options{Home: home, ConfigDir: config, Platform: "linux", Environment: env})
	if e != nil {
		t.Fatal(e)
	}
	opts, e := pluginoptions.New(pluginoptions.Options{Settings: read, WriteSettings: write, Secure: store})
	if e != nil {
		t.Fatal(e)
	}
	service, e := New(Options{Home: home, ConfigDir: config, CWD: home, Platform: runtime.GOOS, Environment: func(key string) (string, bool) { value, exists := env[key]; return value, exists }, PluginOptions: opts, Secure: store, Settings: read, WriteSettings: write, LoadMCPB: func(context.Context, string, string, string, func(string)) (MCPBResult, error) {
		return MCPBResult{}, fmt.Errorf("undeclared MCPB fixture invocation")
	}})
	if e != nil {
		t.Fatal(e)
	}
	return service, home, env
}
func compareSource(t *testing.T, s *Service, home string, plugins []protocol.JSONObject, settings, credentials string, files map[string]string, initialEnv map[string]string, steps []map[string]any) {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	originals := []protocol.JSONObject{}
	for _, plugin := range plugins {
		originals = append(originals, plugin.Clone())
	}
	for path, content := range files {
		if e := os.WriteFile(path, []byte(content), 0600); e != nil {
			t.Fatal(e)
		}
	}
	failures := []protocol.JSONObject{}
	observations := []any{}
	for _, step := range steps {
		index := 0
		if n, ok := step["plugin"].(int); ok {
			index = n
		}
		plugin := plugins[index]
		var value any
		var err error
		switch step["action"] {
		case "load":
			value, err = s.LoadPluginMCPServers(context.Background(), plugin, &failures)
		case "get":
			value, err = s.Get(context.Background(), plugin, &failures)
		case "extract":
			value, err = s.Extract(context.Background(), plugins, &failures)
		case "channels":
			value, err = s.UnconfiguredChannels(context.Background(), plugin)
		case "scope":
			value = AddPluginScope(step["servers"].(protocol.JSONObject), plugin.String("name"), plugin.String("source"))
		case "resolve":
			var values *protocol.JSONObject
			if o, ok := step["userConfig"].(protocol.JSONObject); ok {
				values = &o
			}
			value, err = s.Resolve(context.Background(), step["config"].(protocol.JSONObject), plugin, values, &failures, plugin.String("name"), step["server"].(string))
		case "load-user":
			value, err = s.LoadServerUserConfig(context.Background(), step["id"].(string), step["server"].(string))
		case "save-user":
			err = s.SaveServerUserConfig(context.Background(), step["id"].(string), step["server"].(string), step["values"].(protocol.JSONObject), step["schema"].(protocol.JSONObject))
		case "clear-options":
			s.options.PluginOptions.Clear()
		case "files":
			for path, content := range step["files"].(map[string]string) {
				if e := os.WriteFile(path, []byte(content), 0600); e != nil {
					t.Fatal(e)
				}
			}
		case "env": // The caller supplies an actual mutable environment producer.
			if mutate, ok := step["apply"].(func()); ok {
				mutate()
			}
		}
		if err != nil {
			t.Fatal(step["action"], err)
		}
		settingsData, e := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
		if e != nil {
			t.Fatal(e)
		}
		credentialsData, e := os.ReadFile(filepath.Join(home, ".claude", ".credentials.json"))
		if e != nil {
			t.Fatal(e)
		}
		path := filepath.Join(s.environment("CLAUDE_CONFIG_DIR"), "plugins", "data", invalidID.ReplaceAllString(plugin.String("source"), "-"))
		_, e = os.Stat(path)
		snapshot := []protocol.JSONObject{}
		for _, plugin := range plugins {
			snapshot = append(snapshot, plugin.Clone())
		}
		observations = append(observations, map[string]any{"value": value, "errors": append([]protocol.JSONObject{}, failures...), "plugins": snapshot, "dataExists": e == nil, "settings": parse(t, string(settingsData)), "credentials": parse(t, string(credentialsData))})
	}
	if bun == "" {
		return
	}
	if e := os.RemoveAll(filepath.Join(home, ".claude", "plugins", "data")); e != nil {
		t.Fatal(e)
	}
	operations := []map[string]any{}
	for _, step := range steps {
		copy := map[string]any{}
		for key, value := range step {
			if key != "apply" {
				copy[key] = value
			}
		}
		operations = append(operations, copy)
	}
	payload, _ := json.Marshal(map[string]any{"plugins": originals, "settings": settings, "credentials": credentials, "files": files, "environment": initialEnv, "operations": operations})
	_, file, _, _ := runtime.Caller(0)
	fixture := filepath.Join(filepath.Dir(file), "../../../scripts/quality-gate/go-parity/native-plugin-mcp.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Stdin = bytes.NewReader(payload)
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_PLUGIN_MCP_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	expected, e := command.Output()
	if e != nil {
		t.Fatalf("actual plugin MCP Source: %v %s", e, stderr.Bytes())
	}
	actual, _ := json.Marshal(observations)
	var left, right any
	if json.Unmarshal(actual, &left) != nil || json.Unmarshal(expected, &right) != nil {
		t.Fatalf("invalid Source observations %s", expected)
	}
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("plugin MCP full observations differ\nGo %s\nSource %s", actual, expected)
	}
}
