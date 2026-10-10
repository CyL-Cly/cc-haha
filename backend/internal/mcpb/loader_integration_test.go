package mcpb_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpb"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginmcp"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginoptions"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func object(t *testing.T, data string) protocol.JSONObject {
	t.Helper()
	var o protocol.JSONObject
	if err := o.UnmarshalJSON([]byte(data)); err != nil {
		t.Fatal(err)
	}
	return o
}

const manifest = `{"manifest_version":"0.3","name":"peer","version":"1","description":"test","author":{"name":"Test"},"server":{"type":"binary","entry_point":"bin/server","mcp_config":{"command":"${__dirname}/bin/server","args":["${user_config.roots}"],"env":{"TOKEN":"${user_config.token}","HOME":"${HOME}"}}},"user_config":{"token":{"type":"string","title":"Token","description":"token","required":true,"sensitive":true},"roots":{"type":"string","title":"Roots","description":"paths","multiple":true,"default":["default"]}}}`

func bundle(t *testing.T, text string, extras bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	entries := []struct {
		name string
		data []byte
		mode os.FileMode
	}{{"manifest.json", []byte(text), 0644}, {"bin/", nil, os.ModeDir | 0755}, {"bin/server", []byte{0, 1, 2, 255}, 0751}, {"README.md", []byte{0xef, 0xbb, 0xbf, 'x', 0xff}, 0644}}
	if extras {
		for i := 0; i < 8; i++ {
			entries = append(entries, struct {
				name string
				data []byte
				mode os.FileMode
			}{fmt.Sprintf("file%d.txt", i), []byte("text"), 0644})
		}
	}
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Store}
		h.SetMode(e.mode)
		f, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write(e.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
func buildNative(t *testing.T, home, settings, credentials string) (*mcpb.Service, *pluginmcp.Service) {
	t.Helper()
	config := filepath.Join(home, ".claude")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string]string{filepath.Join(config, "settings.json"): settings, filepath.Join(config, ".credentials.json"): credentials} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	clock := func() float64 { return 1700000000000 }
	state, err := clisettings.NewState(clock)
	if err != nil {
		t.Fatal(err)
	}
	settingsService, err := clisettings.NewService(clisettings.Options{State: state, ConfigHome: config, OriginalCWD: home, Profile: clisettings.NativeProcessProfile, Now: clock, PID: 12345})
	if err != nil {
		t.Fatal(err)
	}
	read := func(ctx context.Context) (protocol.JSONObject, error) {
		if err := ctx.Err(); err != nil {
			return protocol.JSONObject{}, err
		}
		v, err := settingsService.GetEditableSource("userSettings", "")
		if v == nil {
			return protocol.NewJSONObject(), err
		}
		return v.Clone(), err
	}
	write := func(ctx context.Context, p clisettings.Patch) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		r, err := settingsService.UpdateEditableSource("userSettings", "", p)
		if err != nil {
			return err
		}
		return r.Error
	}
	env := map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": config}
	secure, err := securestorage.New(securestorage.Options{Home: home, ConfigDir: config, Platform: "linux", Environment: env})
	if err != nil {
		t.Fatal(err)
	}
	opts, err := pluginoptions.New(pluginoptions.Options{Settings: read, WriteSettings: write, Secure: secure})
	if err != nil {
		t.Fatal(err)
	}
	var peers *pluginmcp.Service
	loader, err := mcpb.New(mcpb.Options{Home: home, Platform: runtime.GOOS, Environment: func(k string) (string, bool) { v, ok := env[k]; return v, ok }, Now: func() time.Time { return time.UnixMilli(1700000000000) }, LoadUserConfig: func(ctx context.Context, id, name string) (*protocol.JSONObject, error) {
		return peers.LoadServerUserConfig(ctx, id, name)
	}, SaveUserConfig: func(ctx context.Context, id, name string, v, s protocol.JSONObject) error {
		return peers.SaveServerUserConfig(ctx, id, name, v, s)
	}})
	if err != nil {
		t.Fatal(err)
	}
	peers, err = pluginmcp.New(pluginmcp.Options{Home: home, ConfigDir: config, CWD: home, Platform: runtime.GOOS, Environment: func(k string) (string, bool) { v, ok := env[k]; return v, ok }, Settings: read, WriteSettings: write, Secure: secure, PluginOptions: opts, LoadMCPB: func(ctx context.Context, source, path, id string, onProgress func(string)) (pluginmcp.MCPBResult, error) {
		r, err := loader.Load(ctx, source, path, id, onProgress, nil, false)
		return pluginmcp.MCPBResult{NeedsConfig: r.NeedsConfig, Manifest: r.Manifest, Config: r.Config, ExtractedPath: r.ExtractedPath, ContentHash: r.ContentHash}, err
	}})
	if err != nil {
		t.Fatal(err)
	}
	return loader, peers
}
func cacheFiles(t *testing.T, path string) map[string]any {
	t.Helper()
	result := map[string]any{}
	err := filepath.WalkDir(path, func(full string, entry os.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(path, full)
		result[filepath.ToSlash(rel)] = map[string]any{"bytes": base64.StdEncoding.EncodeToString(data), "executable": info.Mode()&0111 != 0}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func jsonFile(t *testing.T, path string) any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err = json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	return v
}
func TestActualBundleLoaderConfigurationStorageAndCacheAgainstSource(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "plugin")
	if err = os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	settings := `{"theme":"keep","pluginConfigs":{"fixture@market":{"other":"preserved","mcpServers":{"peer":{"token":"old-plaintext","roots":["old-root"]}}}}}`
	credentials := `{"other":"preserved"}`
	loader, _ := buildNative(t, home, settings, credentials)
	data := bundle(t, manifest, true)
	file := filepath.Join(path, "local.mcpb")
	steps := []map[string]any{{"action": "load", "files": map[string]string{file: base64.StdEncoding.EncodeToString(data)}, "provided": object(t, `{"token":"new-secret","roots":["one","two"]}`), "force": true}, {"action": "changed"}, {"action": "load"}, {"action": "load", "force": true}, {"action": "load", "provided": object(t, `{}`)}, {"action": "load", "provided": object(t, `{"token":"rotated-secret"}`)}, {"action": "load", "files": map[string]string{file: base64.StdEncoding.EncodeToString(bundle(t, strings.Replace(manifest, `"version":"1"`, `"version":"2"`, 1), false))}, "mtime": float64(1700000001)}, {"action": "changed"}, {"action": "load", "source": "missing.mcpb"}, {"action": "load", "source": "bad.dxt", "files": map[string]string{filepath.Join(path, "bad.dxt"): base64.StdEncoding.EncodeToString(bundle(t, `{"manifest_version":"0.3"}`, false))}}}
	observations := []any{}
	for _, step := range steps {
		source := "local.mcpb"
		if v, ok := step["source"].(string); ok {
			source = v
		}
		step["source"] = source
		step["pluginPath"] = path
		step["pluginID"] = "fixture@market"
		for full, data := range stepFiles(step) {
			bytes, err := base64.StdEncoding.DecodeString(data)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(full, bytes, 0600); err != nil {
				t.Fatal(err)
			}
			mtime := time.Unix(1, 0)
			if v, ok := step["mtime"].(float64); ok {
				mtime = time.Unix(int64(v), 0)
			}
			if err = os.Chtimes(full, mtime, mtime); err != nil {
				t.Fatal(err)
			}
		}
		progress := []string{}
		var value any
		var failure any
		if step["action"] == "changed" {
			v, err := loader.CheckChanged(context.Background(), source, path)
			value = v
			if err != nil {
				failure = err.Error()
			}
		} else {
			var provided *protocol.JSONObject
			if v, ok := step["provided"].(protocol.JSONObject); ok {
				provided = &v
			}
			force, _ := step["force"].(bool)
			r, err := loader.Load(context.Background(), source, path, "fixture@market", func(s string) { progress = append(progress, s) }, provided, force)
			if err != nil {
				failure = err.Error()
			} else {
				value = r.JSONObject()
			}
		}
		observations = append(observations, map[string]any{"value": value, "error": failure, "progress": progress, "files": cacheFiles(t, filepath.Join(path, ".mcpb-cache")), "settings": jsonFile(t, filepath.Join(home, ".claude/settings.json")), "credentials": jsonFile(t, filepath.Join(home, ".claude/.credentials.json"))})
	}
	// Reset only this test's extraction tree; Source receives identical archives,
	// settings, fake credentials and clock and runs its actual production graph.
	if err = os.RemoveAll(filepath.Join(path, ".mcpb-cache")); err != nil {
		t.Fatal(err)
	}
	bun, sdk := os.Getenv("CC_HAHA_SOURCE_BUN"), os.Getenv("CC_HAHA_MCPB_SOURCE_SDK")
	if bun == "" || sdk == "" {
		t.Skip("Source bundle comparison requires verified private SDK and Bun")
	}
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "../../.."))
	cmd := exec.Command(bun, "run", filepath.Join(root, "scripts/quality-gate/go-parity/native-mcpb.fixture.ts"))
	cmd.Dir = home
	cmd.Env = []string{"PATH=" + filepath.Dir(bun) + ":/usr/bin:/bin", "HOME=" + home, "USERPROFILE=" + home, "TMPDIR=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + os.TempDir(), "CC_HAHA_MCPB_FIXTURE=1", "CC_HAHA_MCPB_SOURCE_SDK=" + sdk}
	input, _ := json.Marshal(map[string]any{"cases": steps, "loadFixture": map[string]any{"settings": settings, "credentials": credentials, "clock": 1700000000000}})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source bundle: %v\n%s", err, stderr.String())
	}
	want, _ := json.Marshal(observations)
	var actual, expected []any
	if json.Unmarshal(output, &actual) != nil || json.Unmarshal(want, &expected) != nil {
		t.Fatalf("invalid observations %s", output)
	}
	if !reflect.DeepEqual(actual, expected) {
		for i := range actual {
			if !reflect.DeepEqual(actual[i], expected[i]) {
				a, _ := json.Marshal(actual[i])
				b, _ := json.Marshal(expected[i])
				t.Fatalf("step %d\nSource %s\nGo %s", i, a, b)
			}
		}
		t.Fatal("observation mismatch")
	}
}
func stepFiles(step map[string]any) map[string]string {
	v, _ := step["files"].(map[string]string)
	return v
}

func TestBundleDownloadRedirectCachingAndActualPluginProducer(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "plugin")
	if err = os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	loader, peers := buildNative(t, home, `{}`, `{}`)
	plain := object(t, manifest)
	plain.Delete("user_config")
	manifestData, _ := plain.MarshalJSON()
	data := bundle(t, string(manifestData), false)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path == "/redirect.mcpb" {
			http.Redirect(w, r, "/bundle.mcpb", http.StatusFound)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/loop") {
			http.Redirect(w, r, r.URL.Path, http.StatusFound)
			return
		}
		if r.URL.Path == "/failure.mcpb" {
			w.WriteHeader(403)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(data)
	}))
	defer server.Close()
	plugin := object(t, `{"name":"fixture","path":"`+path+`","source":"fixture@market","repository":"fixture@market","manifest":{"mcpServers":"`+server.URL+`/redirect.mcpb"}}`)
	failures := []protocol.JSONObject{}
	config, err := peers.LoadPluginMCPServers(context.Background(), plugin, &failures)
	if err != nil || len(failures) != 0 || config == nil || !bytes.Contains(config.Get("peer"), []byte("/bin/server")) {
		data, _ := json.Marshal(failures)
		t.Fatal(config, string(data), err)
	}
	if requests != 2 {
		t.Fatal("redirect count", requests)
	}
	_, err = loader.Load(context.Background(), server.URL+"/redirect.mcpb", path, "fixture@market", nil, nil, false)
	if err != nil || requests != 2 {
		t.Fatal("URL cache did not reuse", requests, err)
	}
	_, err = loader.Load(context.Background(), server.URL+"/failure.mcpb", path, "fixture@market", nil, nil, false)
	if err == nil || err.Error() != "Failed to download MCPB file from "+server.URL+"/failure.mcpb: Request failed with status code 403" {
		t.Fatal(err)
	}
	_, err = loader.Load(context.Background(), server.URL+"/loop.mcpb", path, "fixture@market", nil, nil, false)
	if err == nil || !strings.Contains(err.Error(), "Maximum number of redirects exceeded") {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = loader.Load(ctx, "missing.mcpb", path, "fixture@market", nil, nil, false); err != context.Canceled {
		t.Fatal(err)
	}
}
