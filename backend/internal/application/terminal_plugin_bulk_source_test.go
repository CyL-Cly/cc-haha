package application

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func prepareTerminalBulkFixture(t *testing.T, work, config string) (map[string]string, []protocol.JSONObject, []protocol.JSONObject) {
	t.Helper()
	files := map[string]string{}
	write := func(path string, data []byte) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		files[path] = base64.StdEncoding.EncodeToString(data)
	}
	market := filepath.Join(work, "market")
	entries, available := []protocol.JSONObject{}, []protocol.JSONObject{}
	for _, name := range []string{"zeta", "bad-a", "alpha", "bad-b", "bad-c"} {
		entry := protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: raw(name)}, protocol.JSONField{Name: "source", Value: raw("./plugins/" + name)})
		if strings.HasPrefix(name, "bad-") {
			entry.Set("dependencies", raw([]string{"absent@mkt"}))
		}
		entries = append(entries, entry)
		available = append(available, obj(raw(map[string]any{"pluginId": name + "@mkt", "marketplaceName": "mkt", "name": name, "entry": entry})))
		write(filepath.Join(market, "plugins", name, ".claude-plugin/plugin.json"), raw(map[string]any{"name": name, "version": "1.0.0", "userConfig": map[string]any{"region": map[string]any{"type": "string", "title": "Region", "description": "Required only in the single-install options chain", "required": true}}}))
		write(filepath.Join(market, "plugins", name, "skills/run/SKILL.md"), []byte("---\ndescription: Actual bulk Skill\n---\nBULK_ACTUAL_"+strings.ToUpper(name)))
	}
	counts := []protocol.JSONObject{obj([]byte(`{"plugin":"zeta@mkt","unique_installs":10}`)), obj([]byte(`{"plugin":"alpha@mkt","unique_installs":1}`))}
	write(filepath.Join(market, ".claude-plugin/marketplace.json"), raw(map[string]any{"name": "mkt", "owner": map[string]string{"name": "Owner"}, "plugins": entries}))
	write(filepath.Join(config, "settings.json"), []byte(`{"futureUser":{"keep":true}}`))
	write(filepath.Join(config, "plugins/known_marketplaces.json"), raw(map[string]any{"mkt": map[string]any{"source": map[string]string{"source": "directory", "path": market}, "installLocation": market, "lastUpdated": "2026-10-01T00:00:00.000Z"}}))
	write(filepath.Join(config, "plugins/install-counts-cache.json"), raw(map[string]any{"version": 1, "fetchedAt": time.Now().UTC().Format(time.RFC3339Nano), "counts": counts}))
	return files, available, counts
}

func TestTerminalPluginBulkActualDiscoverCallerOrderSuccessMixedFailuresAndNextSkill(t *testing.T) {
	bun, sdk := os.Getenv("CC_HAHA_SOURCE_BUN"), os.Getenv("CC_HAHA_MCPB_SOURCE_SDK")
	if bun == "" || sdk == "" {
		t.Skip("pinned original Source and verified compatible MCPB reference required")
	}
	for _, selected := range [][]string{{}, {"alpha@mkt", "zeta@mkt"}, {"bad-c@mkt", "alpha@mkt", "bad-a@mkt", "bad-b@mkt", "zeta@mkt"}, {"bad-c@mkt", "bad-a@mkt", "bad-b@mkt"}} {
		t.Run(strings.Join(selected, "+"), func(t *testing.T) {
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			work := filepath.Join(home, "work")
			files, available, counts := prepareTerminalBulkFixture(t, work, filepath.Join(home, ".claude"))
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-plugin-bulk.fixture.ts")
			cmd := exec.Command(bun, "--no-env-file", fixture)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_PLUGIN_BULK_SOURCE=1", "CC_HAHA_MCPB_SOURCE_SDK=" + sdk, "BUN_OPTIONS=--no-env-file"}
			cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "workDir": work, "files": files, "available": available, "selected": selected, "counts": counts}))
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("actual Source bulk %v %.6000s", err, output)
			}
			var original struct {
				Available           []string
				Result, Error       *string
				Settings, Installed protocol.JSONObject
				Events              []protocol.JSONObject
			}
			if err = json.Unmarshal(output, &original); err != nil {
				t.Fatal(err, string(output))
			}
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				response(w, "bulk-next", "", nil, "BULK_NEXT_DONE")
			}))
			defer server.Close()
			o := nativeSessionOptions(t, server.URL)
			o.Interactive = true
			prepareTerminalBulkFixture(t, o.WorkDir, o.Environment["CLAUDE_CONFIG_DIR"])
			s, err := NewSession(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			var panel *tui.LocalPanel
			emit := func(event tui.Event) error {
				if event.LocalPanel != nil {
					copy := *event.LocalPanel
					panel = &copy
				}
				return nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if _, err = s.InvokeTerminalCommand(ctx, "plugin", "", emit); err != nil {
				t.Fatal(err)
			}
			actualOrder := []string{}
			rows := map[string]string{}
			for _, row := range panel.Rows {
				id := strings.Split(row.Label, " · ")[0]
				actualOrder = append(actualOrder, id)
				rows[id] = row.ID
			}
			if !reflect.DeepEqual(actualOrder, original.Available) {
				t.Fatal("Source Discover order", actualOrder, original.Available)
			}
			for _, id := range selected {
				if err = s.ApplyLocalPanel(ctx, tui.LocalPanelResult{ID: panel.ID, Action: "plugin-select", Row: rows[id]}, emit); err != nil {
					t.Fatal(err)
				}
			}
			if err = s.ApplyLocalPanel(ctx, tui.LocalPanelResult{ID: panel.ID, Action: "plugin-install-selected"}, emit); err != nil {
				t.Fatal(err)
			}
			message := ""
			if original.Result != nil {
				message = *original.Result
			}
			if original.Error != nil {
				message = *original.Error
			}
			if panel.Notice != message || len(panel.Fields) > 0 {
				t.Fatalf("Source bulk result/options boundary got %q fields%d Source%q", panel.Notice, len(panel.Fields), message)
			}
			data, err := os.ReadFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "settings.json"))
			if err != nil {
				t.Fatal(err)
			}
			var actual, expected any
			_ = json.Unmarshal(obj(data).Get("enabledPlugins"), &actual)
			_ = json.Unmarshal(original.Settings.Get("enabledPlugins"), &expected)
			if !reflect.DeepEqual(actual, expected) || string(obj(obj(data).Get("futureUser")).Get("keep")) != "true" {
				t.Fatal("actual Source committed intent/unknown", string(data), raw(original.Settings))
			}
			installed, _ := os.ReadFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "plugins/installed_plugins.json"))
			actualIDs, sourceIDs := []string{}, []string{}
			for _, p := range obj(obj(installed).Get("plugins")).OwnProperties() {
				var id string
				_ = json.Unmarshal(p.NameJSON, &id)
				actualIDs = append(actualIDs, id)
			}
			for _, p := range obj(original.Installed.Get("plugins")).OwnProperties() {
				var id string
				_ = json.Unmarshal(p.NameJSON, &id)
				sourceIDs = append(sourceIDs, id)
			}
			if !reflect.DeepEqual(actualIDs, sourceIDs) {
				t.Fatal("actual installation order", actualIDs, sourceIDs)
			}
			if _, err = s.InvokeTerminalCommand(ctx, "reload-plugins", "", emit); err != nil {
				t.Fatal(err)
			}
			text := "next human"
			if len(actualIDs) > 0 {
				text = "/zeta:run"
			}
			if err = s.Submit(ctx, tui.Input{Text: text}, nil); err != nil || requests.Load() != 1 {
				t.Fatal("same Harness next actual Skill/human", err, requests.Load())
			}
		})
	}
}

func TestTerminalPluginDiscoverActualSourceGlobalScopeAndSettingsDivergence(t *testing.T) {
	bun, sdk := os.Getenv("CC_HAHA_SOURCE_BUN"), os.Getenv("CC_HAHA_MCPB_SOURCE_SDK")
	if bun == "" || sdk == "" {
		t.Skip("pinned Source and verified compatible MCPB reference required")
	}
	for _, profile := range []struct {
		scope   string
		enabled json.RawMessage
	}{{"user", raw(true)}, {"user", raw(false)}, {"user", nil}, {"project", raw(true)}, {"local", raw(true)}, {"managed", raw(true)}} {
		t.Run(profile.scope+"-"+string(profile.enabled), func(t *testing.T) {
			seed := func(work, config string) (map[string]string, []protocol.JSONObject, []protocol.JSONObject) {
				files, available, counts := prepareTerminalBulkFixture(t, work, config)
				settings := obj([]byte(`{"futureUser":{"keep":true}}`))
				if profile.enabled != nil {
					settings.Set("enabledPlugins", raw(map[string]json.RawMessage{"zeta@mkt": profile.enabled}))
				}
				entry := map[string]any{"scope": profile.scope, "installPath": filepath.Join(work, "market/plugins/zeta"), "version": "1.0.0", "installedAt": "2026-10-01T00:00:00.000Z", "lastUpdated": "2026-10-01T00:00:00.000Z"}
				if profile.scope == "project" || profile.scope == "local" {
					entry["projectPath"] = work
				}
				for path, value := range map[string][]byte{filepath.Join(config, "settings.json"): raw(settings), filepath.Join(config, "plugins/installed_plugins.json"): raw(map[string]any{"version": 2, "plugins": map[string]any{"zeta@mkt": []any{entry}}})} {
					if err := os.WriteFile(path, value, 0600); err != nil {
						t.Fatal(err)
					}
					files[path] = base64.StdEncoding.EncodeToString(value)
				}
				return files, available, counts
			}
			home, _ := filepath.EvalSymlinks(t.TempDir())
			work := filepath.Join(home, "work")
			files, available, counts := seed(work, filepath.Join(home, ".claude"))
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-plugin-bulk.fixture.ts")
			cmd := exec.Command(bun, "--no-env-file", fixture)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_PLUGIN_BULK_SOURCE=1", "CC_HAHA_MCPB_SOURCE_SDK=" + sdk, "BUN_OPTIONS=--no-env-file"}
			cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "workDir": work, "files": files, "available": available, "selected": []string{}, "counts": counts}))
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("actual Source global gate %v %.5000s", err, output)
			}
			var original struct{ Available []string }
			if err = json.Unmarshal(output, &original); err != nil {
				t.Fatal(err, string(output))
			}
			o := nativeSessionOptions(t, "http://127.0.0.1:1")
			o.Interactive = true
			seed(o.WorkDir, o.Environment["CLAUDE_CONFIG_DIR"])
			s, err := NewSession(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			var panel *tui.LocalPanel
			emit := func(event tui.Event) error {
				if event.LocalPanel != nil {
					copy := *event.LocalPanel
					panel = &copy
				}
				return nil
			}
			if _, err = s.InvokeTerminalCommand(context.Background(), "plugin", "", emit); err != nil {
				t.Fatal(err)
			}
			ids := []string{}
			for _, row := range panel.Rows {
				ids = append(ids, strings.Split(row.Label, " · ")[0])
			}
			if !reflect.DeepEqual(ids, original.Available) {
				t.Fatal("original global scope/settings-divergence gate", ids, original.Available)
			}
		})
	}
}
