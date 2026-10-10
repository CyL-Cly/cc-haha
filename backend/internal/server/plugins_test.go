package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPluginsHTTPInputErrorsAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned Source Bun unavailable")
	}
	allocation := t.TempDir()
	home := filepath.Join(allocation, "go-home")
	config := filepath.Join(home, ".claude")
	workspace := filepath.Join(home, "workspace")
	_ = os.MkdirAll(config, 0700)
	_ = os.MkdirAll(workspace, 0700)
	fail := func() error {
		t.Error("Invalid HTTP input reached management business before validation")
		return errors.New("invalid input reached producer")
	}
	installed, err := pluginregistry.NewInstalledRegistry(pluginregistry.InstalledOptions{PluginsDir: filepath.Join(config, "plugins"), Store: storage.New(storage.Options{})})
	if err != nil {
		t.Fatal(err)
	}
	materializer, err := pluginregistry.NewMaterializer(pluginregistry.MaterializerOptions{Directories: pluginregistry.Directories{Primary: filepath.Join(config, "plugins")}, CWD: workspace, Environment: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	service, err := pluginsapi.New(pluginsapi.Options{WorkDir: workspace, PluginsDir: filepath.Join(config, "plugins"), Installed: installed, Materializer: materializer, Load: func(context.Context, bool) (pluginregistry.Result, error) { return pluginregistry.Result{}, fail() }, Hydrate: func(context.Context, *pluginregistry.Result) error { return fail() }, ReadSettings: func(context.Context, string, string) (protocol.JSONObject, error) {
		return protocol.JSONObject{}, fail()
	}, WriteEnabled: func(context.Context, string, string, string, *bool) error { return fail() }, EditableEnabled: func(context.Context) (map[string]bool, error) { return nil, fail() }, ClearCaches: func(context.Context) error { return fail() }, KnownMarketplaces: func(context.Context) (protocol.JSONObject, error) { return protocol.JSONObject{}, fail() }, LookupPlugin: func(context.Context, string) (*pluginsapi.MarketplacePlugin, error) { return nil, fail() }, DeleteOptions: func(context.Context, string) error { return fail() }, SkillsCount: func(context.Context) (int, error) { return 0, fail() }, AgentsCount: func(context.Context, string) (int, error) { return 0, fail() }, LoadHooks: func(context.Context) error { return fail() }, ReloadSession: func(context.Context, string) (protocol.JSONObject, error) { return protocol.JSONObject{}, fail() }, IsBuiltin: func(string) bool { _ = fail(); return false }, BuiltinDetail: func(string) (protocol.JSONObject, error) { return protocol.JSONObject{}, fail() }})
	if err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{{"method": "GET", "path": "/api/plugins/detail"}, {"method": "PUT", "path": "/api/plugins/enable"}, {"method": "POST", "path": "/api/plugins/enable", "raw": "{"}, {"method": "POST", "path": "/api/plugins/enable", "raw": "null"}, {"method": "POST", "path": "/api/plugins/enable", "raw": "[]"}, {"method": "POST", "path": "/api/plugins/enable", "raw": "{}"}, {"method": "POST", "path": "/api/plugins/disable", "raw": `{"id":"a@b","scope":"managed"}`}, {"method": "POST", "path": "/api/plugins/disable", "raw": `{"id":"a@b","cwd":"relative"}`}, {"method": "POST", "path": "/api/plugins/unknown", "raw": `{"id":"a@b"}`}, {"method": "POST", "path": "/api/plugins/uninstall", "raw": `{"id":"a@b","keepData":null}`}, {"method": "POST", "path": "/api/plugins/update", "raw": `{"id":"a@b"}`}}
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := (&Server{}).pluginsHTTP(w, r, segments(r.URL.Path), service); err != nil {
			writeError(w, err)
		}
	}))
	defer host.Close()
	observed := []map[string]any{}
	for _, row := range rows {
		body, _ := row["raw"].(string)
		request, _ := http.NewRequest(row["method"].(string), host.URL+row["path"].(string), strings.NewReader(body))
		response, err := host.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		var value any
		_ = json.NewDecoder(response.Body).Decode(&value)
		_ = response.Body.Close()
		observed = append(observed, map[string]any{"status": float64(response.StatusCode), "body": value})
	}
	sourceHome := filepath.Join(allocation, "source-home")
	_ = os.MkdirAll(filepath.Join(sourceHome, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-plugins-api.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", "run", fixture)
	cmd.Dir = sourceHome
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + sourceHome, "USERPROFILE=" + sourceHome, "CLAUDE_CONFIG_DIR=" + filepath.Join(sourceHome, ".claude"), "TMPDIR=" + filepath.Join(sourceHome, "tmp"), "NODE_ENV=test", "USER_TYPE=external", "CLAUDE_CODE_ENTRYPOINT=claude-desktop", "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_PLUGINS_FIXTURE=1"}
	input, _ := json.Marshal(map[string]any{"cases": rows})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Actual Source plugin HTTP %v %s", err, stderr.String())
	}
	var expected []map[string]any
	_ = json.Unmarshal(output, &expected)
	for i, row := range expected {
		if !reflect.DeepEqual(observed[i], map[string]any{"status": row["status"], "body": row["body"]}) {
			t.Fatalf("HTTP case %d Go=%v Source=%v", i, observed[i], row)
		}
	}
}
