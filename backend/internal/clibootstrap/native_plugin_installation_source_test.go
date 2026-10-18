package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func sourceNativePluginStartup(t *testing.T, f nativePluginFixture, operation string, additional ...[]string) protocol.JSONObject {
	t.Helper()
	input := map[string]any{"operation": operation}
	if len(additional) > 0 {
		input["additional"] = additional[0]
	}
	return sourceNativePluginStartupInput(t, f, input)
}

func sourceNativePluginStartupInput(t *testing.T, f nativePluginFixture, input map[string]any) protocol.JSONObject {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun unavailable")
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-plugin-reconciliation.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", "run", fixture)
	command.Dir = f.home
	command.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "PATH=" + filepath.Join(f.home, "bin"), "NODE_ENV=production", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_PLUGIN_RECONCILIATION_SOURCE=1"}
	input["home"], input["cwd"] = f.home, f.n.CWD
	command.Stdin = bytes.NewReader(rawMain(input))
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("actual Source plugin startup %v\n%s\n%s", err, output, stderr.String())
	}
	var result protocol.JSONObject
	if err = result.UnmarshalJSON(output); err != nil {
		t.Fatal(err, string(output))
	}
	return result
}

func startupPluginFixture(t *testing.T, profile string) nativePluginFixture {
	t.Helper()
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.plugin, ".claude-plugin", "plugin.json"), `{"name":"sample"}`)
	f.n.Now = func() time.Time { return time.UnixMilli(1791244800000) }
	f.n.NowMillis = func() float64 { return 1791244800000 }
	f.n.Environment["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
	global := map[string]any{"futureGlobal": map[string]bool{"keep": true}, "projects": map[string]any{f.n.CWD: map[string]bool{"hasTrustDialogAccepted": profile != "untrusted"}}}
	f.write(t, filepath.Join(f.config, ".claude.json"), string(rawMain(global)))
	source := map[string]any{"source": "directory", "path": f.market}
	settings := map[string]any{"futureSettings": map[string]bool{"keep": true}, "extraKnownMarketplaces": map[string]any{"mkt": map[string]any{"source": source}}, "enabledPlugins": map[string]bool{"sample@mkt": true}}
	switch profile {
	case "new-install", "untrusted":
		_ = os.Remove(filepath.Join(f.config, "plugins", "known_marketplaces.json"))
	case "missing":
		settings["extraKnownMarketplaces"] = map[string]any{"ghost": map[string]any{"source": map[string]string{"source": "directory", "path": filepath.Join(f.home, "missing")}}}
	case "source-update":
		old := filepath.Join(f.home, "old-market")
		f.write(t, filepath.Join(old, ".claude-plugin", "marketplace.json"), `{"name":"mkt","owner":{"name":"Owner"},"plugins":[]}`)
		f.write(t, filepath.Join(f.config, "plugins", "known_marketplaces.json"), string(rawMain(map[string]any{"mkt": map[string]any{"source": map[string]string{"source": "directory", "path": old}, "installLocation": old, "lastUpdated": "2026-10-01T00:00:00.000Z"}})))
	}
	f.write(t, filepath.Join(f.config, "settings.json"), string(rawMain(settings)))
	return f
}

func TestNativePluginInstallationActualSourceStartupTrustStatusAndComponents(t *testing.T) {
	for _, profile := range []string{"up-to-date", "new-install", "source-update", "missing", "untrusted"} {
		t.Run(profile, func(t *testing.T) {
			source := startupPluginFixture(t, profile)
			expected := sourceNativePluginStartup(t, source, "startup")
			f := startupPluginFixture(t, profile)
			before, _ := os.ReadFile(filepath.Join(f.config, "settings.json"))
			m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{Sources: DefaultState(), Tools: []string{"Read"}, Model: "fixture-model", MaxTurns: 1, SystemPrompt: "owned plugin startup"}, Native: f.n, PID: os.Getpid(), Platform: runtime.GOOS, Version: "999.0.0-plugin-reconciliation", Interactive: true, Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err = m.StartSession(ctx); err != nil {
				t.Fatal(err)
			}
			if m.nativePluginInstallation == nil || m.nativePluginInstallation.marketplaceEnabled != (profile != "untrusted") {
				t.Fatal("startup trust did not gate the marketplace job independently of the REPL mount")
			}
			select {
			case <-m.nativePluginInstallation.done:
			case <-ctx.Done():
				t.Fatal("real install/mount did not join", ctx.Err())
			}
			actual, err := m.NativePluginInstallationStatus(ctx)
			if err != nil {
				t.Fatal(err)
			}
			final := mainObj(expected.Get("final"))
			installation := mainObj(final.Get("installationStatus"))
			for key, value := range map[string]json.RawMessage{"marketplaces": installation.Get("marketplaces"), "plugins": installation.Get("plugins"), "needsRefresh": final.Get("needsRefresh")} {
				var left, right any
				_ = json.Unmarshal(actual.Get(key), &left)
				_ = json.Unmarshal(value, &right)
				// Both processes use independently allocated physical homes.
				left = normalizePluginStartupValue(left, f.home)
				right = normalizePluginStartupValue(right, source.home)
				if !reflect.DeepEqual(left, right) {
					t.Fatalf("%s Source mismatch\nGo=%s\nSource=%s", key, rawMain(left), rawMain(right))
				}
			}
			if profile == "new-install" {
				found := false
				for _, command := range m.Registry.ControlCommands() {
					if command.Name == "sample:sample" {
						found = true
					}
				}
				if !found {
					t.Fatal("new marketplace did not refresh actual plugin Skill command", m.Registry.ControlCommands())
				}
			}
			persisted, _ := os.ReadFile(filepath.Join(f.config, "settings.json"))
			if !bytes.Equal(before, persisted) {
				t.Fatal("startup rewrote declared settings", string(persisted))
			}
			if err = m.ShutdownSession(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func normalizePluginStartupValue(value any, home string) any {
	data := bytes.ReplaceAll(rawMain(value), []byte(home), []byte("$HOME"))
	canonical, _ := filepath.EvalSymlinks(home)
	data = bytes.ReplaceAll(data, []byte(canonical), []byte("$HOME"))
	var result any
	_ = json.Unmarshal(data, &result)
	return result
}

func TestNativePluginInstallationHeldPhysicalDownloadCannotBlockStartModelOrClose(t *testing.T) {
	started, closed := make(chan struct{}), make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(closed) }))
	defer peer.Close()
	var models atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		models.Add(1)
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"held-plugin-next\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"fixture-model\",\"stop_reason\":null,\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"HELD_PLUGIN_NEXT\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer model.Close()
	f := startupPluginFixture(t, "up-to-date")
	f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
	f.n.Environment["ANTHROPIC_API_KEY"] = "fake-plugin-owned"
	f.write(t, filepath.Join(f.config, "settings.json"), string(rawMain(map[string]any{"extraKnownMarketplaces": map[string]any{"held": map[string]any{"source": map[string]string{"source": "url", "url": peer.URL}}}})))
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{Sources: DefaultState(), Tools: []string{"Read"}, Model: "fixture-model", MaxTurns: 1, SystemPrompt: "owned held plugin"}, Native: f.n, PID: os.Getpid(), Platform: runtime.GOOS, Version: "999.0.0-plugin-reconciliation", Interactive: true, Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err = m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("physical marketplace GET never started")
	}
	input := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "ordinary human while marketplace GET suspended"})})
	if err = m.RunTurn(ctx, input); err != nil {
		t.Fatal(err)
	}
	if models.Load() != 1 {
		t.Fatal("background install prevented same Harness model", models.Load())
	}
	if err = m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("actual marketplace peer not canceled/joined")
	}
}

func TestNativePluginDeclaredIntentActualSourceAdditionalImplicitAndOverrides(t *testing.T) {
	for _, profile := range []string{"implicit-official", "explicit-official", "false-overrides-additional", "multiple-at-identifier"} {
		t.Run(profile, func(t *testing.T) {
			f := startupPluginFixture(t, "up-to-date")
			additional := filepath.Join(f.home, "additional")
			f.write(t, filepath.Join(additional, ".claude", "settings.json"), `{"enabledPlugins":{"x@claude-plugins-official":true},"extraKnownMarketplaces":{"same":{"source":{"source":"settings","name":"same","plugins":[]}},"from-additional":{"source":{"source":"settings","name":"from-additional","plugins":[]}}}}`)
			f.write(t, filepath.Join(additional, ".claude", "settings.local.json"), string(rawMain(map[string]any{"extraKnownMarketplaces": map[string]any{"same": map[string]any{"source": map[string]string{"source": "directory", "path": filepath.Join(f.home, "local-market")}}}})))
			settings := map[string]any{"extraKnownMarketplaces": map[string]any{"same": map[string]any{"source": map[string]any{"source": "directory", "path": filepath.Join(f.home, "user-market")}}}}
			if profile == "false-overrides-additional" {
				settings["enabledPlugins"] = map[string]bool{"x@claude-plugins-official": false}
			}
			if profile == "multiple-at-identifier" {
				settings["enabledPlugins"] = map[string]bool{"x@claude-plugins-official": false, "x@claude-plugins-official@ignored": true}
			}
			if profile == "explicit-official" {
				settings["extraKnownMarketplaces"].(map[string]any)["claude-plugins-official"] = map[string]any{"source": map[string]any{"source": "git", "url": "https://github.com/anthropics/private-mirror.git"}}
			}
			f.write(t, filepath.Join(f.config, "settings.json"), string(rawMain(settings)))
			expected := sourceNativePluginStartup(t, f, "diff", []string{additional})
			m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{Sources: DefaultState(), Tools: []string{"Read"}, Model: "fixture-model", MaxTurns: 1, AdditionalDirectories: []string{additional}}, Native: f.n, PID: os.Getpid(), Platform: runtime.GOOS, Version: "999.0.0-plugin-reconciliation", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			actual, err := m.nativeSettings.declaredPluginMarketplaces(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(normalizePluginStartupValue(actual, f.home), normalizePluginStartupValue(mainObj(expected.Get("declared")), f.home)) {
				t.Fatal("actual intent producer", string(rawMain(actual)), string(expected.Get("declared")))
			}
		})
	}
}

func TestNativePluginLoaderActualSourceCachedFullMemoAndClear(t *testing.T) {
	create := func() nativePluginFixture {
		f := startupPluginFixture(t, "up-to-date")
		f.write(t, filepath.Join(f.plugin, ".claude-plugin", "plugin.json"), `{"name":"sample","version":"1.0.0","description":"OLD_PHYSICAL_MANIFEST"}`)
		return f
	}
	source := create()
	expected := sourceNativePluginStartup(t, source, "loader-lifetime")
	f := create()
	settings, err := NewNativeProcessSettings(f.n, runtime.GOOS, DefaultState())
	if err != nil {
		t.Fatal(err)
	}
	defer settings.Close(context.Background())
	rows := []any{}
	project := func(result pluginregistry.Result) any {
		enabled := []any{}
		errors := []string{}
		for _, plugin := range result.Enabled {
			enabled = append(enabled, map[string]string{"name": plugin.Name, "description": plugin.Manifest.String("description")})
		}
		for _, error := range result.Errors {
			errors = append(errors, error.String("type"))
		}
		return map[string]any{"enabled": enabled, "errors": errors}
	}
	load := func(full bool) pluginregistry.Result {
		t.Helper()
		result, err := settings.LoadPlugins(context.Background(), "", full)
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, project(result))
		return result
	}
	load(false)
	full := load(true)
	if len(full.Enabled) != 1 {
		t.Fatal("actual full load failed", full)
	}
	path := filepath.Join(full.Enabled[0].Path, ".claude-plugin", "plugin.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	manifest := mainObj(data)
	manifest.Set("description", quoteMain("NEW_PHYSICAL_MANIFEST"))
	if err := os.WriteFile(path, rawMain(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	load(false)
	load(true)
	settings.plugins.loader.Clear()
	load(false)
	load(true)
	var sourceRows any
	_ = json.Unmarshal(expected.Get("observations"), &sourceRows)
	var nativeRows any
	_ = json.Unmarshal(rawMain(rows), &nativeRows)
	if !reflect.DeepEqual(nativeRows, sourceRows) {
		t.Fatalf("cached/full lifetime differs\nGo=%s\nSource=%s", rawMain(rows), expected.Get("observations"))
	}
}

func TestNativeInteractivePluginMountActualSourceUntrustedFlaggedAndDelisting(t *testing.T) {
	for _, profile := range []string{"untrusted", "previously-flagged", "newly-delisted", "lsp-components", "invalid-lsp", "memoized-commands"} {
		t.Run(profile, func(t *testing.T) {
			seed := func() nativePluginFixture {
				f := startupPluginFixture(t, "up-to-date")
				f.write(t, filepath.Join(f.config, ".claude.json"), string(rawMain(map[string]any{"projects": map[string]any{f.n.CWD: map[string]bool{"hasTrustDialogAccepted": false}}})))
				f.write(t, filepath.Join(f.plugin, "commands", "legacy.md"), "---\ndescription: Actual mount command\n---\nACTUAL_PLUGIN_MOUNT_COMMAND")
				if profile == "previously-flagged" {
					f.write(t, filepath.Join(f.config, "plugins", "flagged-plugins.json"), `{"plugins":{"old@mkt":{"flaggedAt":"2026-10-01T00:00:00.000Z"}}}`)
				}
				if profile == "lsp-components" {
					f.write(t, filepath.Join(f.plugin, ".lsp.json"), `{"owned":{"command":"owned-fixture-lsp-never-spawned","extensionToLanguage":{".fixture":"fixture"}}}`)
				}
				if profile == "invalid-lsp" {
					f.write(t, filepath.Join(f.plugin, ".lsp.json"), `{"bad":{"command":3}}`)
				}
				if profile == "newly-delisted" {
					f.write(t, filepath.Join(f.market, ".claude-plugin", "marketplace.json"), `{"name":"mkt","owner":{"name":"Owner"},"plugins":[],"forceRemoveDeletedPlugins":true}`)
					f.write(t, filepath.Join(f.config, "plugins", "installed_plugins.json"), string(rawMain(map[string]any{"version": 2, "plugins": map[string]any{"sample@mkt": []any{map[string]any{"scope": "user", "installPath": f.plugin, "version": "1.0.0", "installedAt": "2026-10-01T00:00:00.000Z", "lastUpdated": "2026-10-01T00:00:00.000Z"}}}})))
				}
				return f
			}
			source := seed()
			expected := sourceNativePluginStartupInput(t, source, map[string]any{"operation": "interactive-mount", "addBeforeMount": profile == "memoized-commands"})
			final := mainObj(expected.Get("final"))
			if final.String("reconnectKey") != "" || string(final.Get("reconnectKey")) != "19" || string(final.Get("agentsUnchanged")) != "true" {
				t.Fatal("actual hook changed unrelated MCP/agent state", string(expected.Get("final")))
			}
			f := seed()
			m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{Sources: DefaultState(), Tools: []string{"Read"}, Model: "fixture-model", MaxTurns: 1, SystemPrompt: "owned plugin mount"}, Native: f.n, PID: os.Getpid(), Platform: runtime.GOOS, Version: "999.0.0-plugin-reconciliation", Interactive: true, Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			if profile == "memoized-commands" {
				f.write(t, filepath.Join(f.plugin, "commands", "added.md"), "---\ndescription: Added after startup command memo\n---\nACTUAL_ADDED_COMMAND")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err = m.StartSession(ctx); err != nil {
				t.Fatal(err)
			}
			if m.nativePluginInstallation == nil || m.nativePluginInstallation.marketplaceEnabled {
				t.Fatal("untrusted ordinary REPL did not isolate mount from marketplace installation")
			}
			select {
			case <-m.nativePluginInstallation.done:
			case <-ctx.Done():
				t.Fatal("actual initial mount did not join", ctx.Err())
			}
			m.nativePluginInstallation.mu.Lock()
			mountError := m.nativePluginInstallation.err
			m.nativePluginInstallation.mu.Unlock()
			if mountError != nil {
				t.Fatal("actual initial mount failed", mountError)
			}
			if profile != "newly-delisted" {
				var commands []string
				_ = json.Unmarshal(final.Get("commands"), &commands)
				originalFound, nativeFound := false, false
				for _, command := range commands {
					originalFound = originalFound || command == "sample:legacy"
				}
				for _, command := range m.Registry.ControlCommands() {
					nativeFound = nativeFound || command.Name == "sample:legacy"
				}
				if !originalFound || !nativeFound {
					t.Fatal("real mount did not publish its original plugin command", commands, m.Registry.ControlCommands())
				}
				if profile == "memoized-commands" {
					originalAdded, nativeAdded := false, false
					for _, command := range commands {
						originalAdded = originalAdded || command == "sample:added"
					}
					for _, command := range m.Registry.ControlCommands() {
						nativeAdded = nativeAdded || command.Name == "sample:added"
					}
					if originalAdded || nativeAdded {
						t.Fatalf("initial mount refreshed the command memo without Source cache clear: Source=%v Native=%v", originalAdded, nativeAdded)
					}
					var afterReload []string
					_ = json.Unmarshal(final.Get("commandsAfterReload"), &afterReload)
					for _, command := range afterReload {
						originalAdded = originalAdded || command == "sample:added"
					}
					if _, err := m.ReloadComponents(ctx); err != nil {
						t.Fatal(err)
					}
					for _, command := range m.Registry.ControlCommands() {
						nativeAdded = nativeAdded || command.Name == "sample:added"
					}
					if !originalAdded || !nativeAdded {
						t.Fatal("original explicit reload did not activate actual new command", afterReload, m.Registry.ControlCommands())
					}
				}
			}
			status, err := m.NativePluginInstallationStatus(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var notifications []protocol.JSONObject
			_ = json.Unmarshal(final.Get("notifications"), &notifications)
			flaggedNotification := false
			for _, notification := range notifications {
				if notification.String("key") == "plugin-delisted-flagged" {
					flaggedNotification = notification.String("text") == "Plugins flagged. Check /plugins" && notification.String("color") == "warning" && notification.String("priority") == "high"
				}
			}
			var flagged []string
			_ = json.Unmarshal(status.Get("flaggedPlugins"), &flagged)
			if flaggedNotification != (len(flagged) > 0) || flaggedNotification != (profile == "previously-flagged" || profile == "newly-delisted") {
				t.Fatalf("Source mount notification has no actual native flagged producer: Source=%s Native=%s", final.Get("notifications"), status.Get("flaggedPlugins"))
			}
			if profile == "lsp-components" {
				var slots []protocol.JSONObject
				_ = json.Unmarshal(mainObj(final.Get("cachedAfter")).Get("slots"), &slots)
				cached, err := m.nativeSettings.LoadPlugins(ctx, "", false)
				if err != nil || len(slots) != 1 || mainObj(slots[0].Get("lspServers")).Get("owned") == nil || len(cached.Enabled) != 1 || cached.Enabled[0].LSPServers == nil || cached.Enabled[0].LSPServers.Get("owned") == nil {
					t.Fatalf("actual mount did not warm the original LSP cache slot: Native=%+v Source=%s error=%v", cached, final.Get("cachedAfter"), err)
				}
			}
			var sourceErrors, nativeErrors any
			_ = json.Unmarshal(final.Get("errors"), &sourceErrors)
			_ = json.Unmarshal(status.Get("initialErrors"), &nativeErrors)
			if !reflect.DeepEqual(normalizePluginStartupValue(sourceErrors, source.home), normalizePluginStartupValue(nativeErrors, f.home)) {
				t.Fatalf("initial full-load errors mismatch: Source=%s Native=%s", final.Get("errors"), status.Get("initialErrors"))
			}
			cached, err := m.nativeSettings.LoadPlugins(ctx, "", false)
			if err != nil {
				t.Fatal(err)
			}
			var cachedSourceErrors any
			_ = json.Unmarshal(mainObj(final.Get("cachedAfter")).Get("errors"), &cachedSourceErrors)
			if !reflect.DeepEqual(normalizePluginStartupValue(cachedSourceErrors, source.home), normalizePluginStartupValue(cached.Errors, f.home)) {
				t.Fatalf("component errors did not enter original cached promise: Source=%s Native=%s", mainObj(final.Get("cachedAfter")).Get("errors"), rawMain(cached.Errors))
			}
			for _, relative := range []string{"settings.json", "plugins/installed_plugins.json", "plugins/flagged-plugins.json"} {
				left, leftErr := os.ReadFile(filepath.Join(f.config, relative))
				right, rightErr := os.ReadFile(filepath.Join(source.config, relative))
				if os.IsNotExist(leftErr) && os.IsNotExist(rightErr) {
					continue
				}
				var a, b any
				_ = json.Unmarshal(left, &a)
				_ = json.Unmarshal(right, &b)
				if leftErr != nil || rightErr != nil || !reflect.DeepEqual(normalizePluginStartupValue(a, f.home), normalizePluginStartupValue(b, source.home)) {
					t.Fatalf("mount persisted effect %s differs: Native=%s Source=%s errors=%v/%v", relative, left, right, leftErr, rightErr)
				}
			}
		})
	}
}

func TestNativePluginLoaderActualSourceHeldFullKeepsReadyCache(t *testing.T) {
	source := startupPluginFixture(t, "up-to-date")
	expected := sourceNativePluginStartup(t, source, "loader-held-full")
	var observations []protocol.JSONObject
	_ = json.Unmarshal(expected.Get("observations"), &observations)
	if len(observations) != 2 || string(observations[0].Get("fullCompleted")) != "false" || string(observations[0].Get("sameCachedPromiseResult")) != "true" || string(observations[1].Get("fullCompleted")) != "true" {
		t.Fatal("actual Source held physical manifest consumer did not prove independent cached promise", string(expected.Get("observations")))
	}
	f := startupPluginFixture(t, "up-to-date")
	p, err := NewNativeProcessSettings(f.n, runtime.GOOS, DefaultState())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close(context.Background())
	if _, err = p.InitialSettings(); err != nil {
		t.Fatal(err)
	}
	cached, err := p.LoadPlugins(context.Background(), "", false)
	if err != nil {
		t.Fatal(err)
	}
	held, release := make(chan struct{}), make(chan struct{})
	// Build the same native loader around its real materializer; only delivery
	// of the completed, physical source preparation is controlled by the test.
	options := pluginregistry.Options{HomeDir: f.n.Home, ConfigDir: f.config, CWD: f.n.CWD, Environment: func(key string) string { return f.n.Environment[key] }, Builtins: p.plugins.builtins, Installed: p.plugins.installed, SettingsState: p.settingsState,
		Settings: func(ctx context.Context, _ string) (protocol.JSONObject, error) {
			value, err := p.InitialSettings()
			if value == nil {
				return protocol.JSONObject{}, err
			}
			return value.Clone(), err
		},
		Policy: func(context.Context) (protocol.JSONObject, error) { return protocol.NewJSONObject(), nil }, AddDirEnabled: func(context.Context) (protocol.JSONObject, error) { return protocol.NewJSONObject(), nil },
		Prepare: func(ctx context.Context, entry protocol.JSONObject, location, id string, installation protocol.JSONObject) (pluginregistry.Prepared, error) {
			value, err := p.plugins.materializer.Prepare(ctx, entry, location, id, installation)
			close(held)
			select {
			case <-release:
			case <-ctx.Done():
				return value, ctx.Err()
			}
			return value, err
		}, ExtractZip: p.plugins.archive.ExtractPlugin}
	loader, err := pluginregistry.New(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = loader.LoadCached(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	full := make(chan error, 1)
	go func() { _, err := loader.LoadFull(ctx); full <- err }()
	select {
	case <-held:
	case <-ctx.Done():
		t.Fatal("actual native materialization did not finish physical I/O", ctx.Err())
	}
	ready := make(chan pluginregistry.Result, 1)
	go func() { value, _ := loader.LoadCached(ctx); ready <- value }()
	var during pluginregistry.Result
	independent := false
	select {
	case during = <-ready:
		independent = true
	case <-time.After(2 * time.Second):
	}
	close(release)
	if err = <-full; err != nil {
		t.Fatal(err)
	}
	if !independent || len(during.Enabled) != len(cached.Enabled) || len(during.Enabled) != 1 || during.Enabled[0].Name != "sample" {
		t.Fatal("held full-load promise blocked or changed the already-ready native cached consumer")
	}
}
