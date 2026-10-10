package clibootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/connectors"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/migrations"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestNativeConnectorLifecyclePublishesActualInstalledPluginAndSharedSkill(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("owned POSIX command primitive")
	}
	f := newNativePluginFixture(t)
	f.n.Environment["PATH"] = "/usr/bin:/bin"
	f.write(t, filepath.Join(f.config, "settings.json"), `{"future":{"keep":true}}`)
	store := storage.New(storage.Options{})
	p, err := NewNativeProcessSettings(f.n, "darwin", DefaultState(), store)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close(context.Background())
	upgrader, err := migrations.New(migrations.Options{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	settingsService, err := settings.New(settings.Options{Store: store, GitExecutable: "git", EnsureMigrated: upgrader.SettingsCallback, HomeDir: f.home, ConfigDir: f.config, ProjectRoot: f.cwd, Environment: func() map[string]string { return f.n.Environment }, ShellEnvironment: func(context.Context) (map[string]string, error) { return f.n.Environment, nil }, InvalidateCache: p.InvalidateSettingsCache})
	if err != nil {
		t.Fatal(err)
	}
	secure, err := securestorage.New(securestorage.Options{Home: f.home, ConfigDir: f.config, Platform: "linux", Environment: f.n.Environment})
	if err != nil {
		t.Fatal(err)
	}
	options, err := p.PluginOptions(secure)
	if err != nil {
		t.Fatal(err)
	}
	unexpected := func() error { return errors.New("native CLI connector fixture must not invoke remote MCP/OAuth") }
	peers, err := mcpruntime.New(mcpruntime.Options{CWD: f.cwd, Environment: f.n.Environment, Auth: func(context.Context, string, protocol.JSONObject) (mcpclient.AuthProvider, error) {
		return nil, unexpected()
	}, Enabled: func(context.Context, string, protocol.JSONObject) (bool, error) { return false, unexpected() }, Authenticate: func(context.Context, string, protocol.JSONObject, func(string) error) error { return unexpected() }, ClearOAuth: func(context.Context, string, protocol.JSONObject) error { return unexpected() }})
	if err != nil {
		t.Fatal(err)
	}
	defer peers.Close(context.Background())
	catalog, err := connectors.DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	var def protocol.JSONObject
	for _, d := range catalog.Definitions {
		if d.String("id") == "wecom" {
			def = d
		}
	}
	marker := filepath.Join(f.home, "native-invocations.txt")
	binary := []byte("#!/bin/sh\nif [ \"$1\" = --version ]; then printf '%s\\n' '" + def.String("version") + "'; else printf '%s\\n' \"$*\" >> '" + marker + "'; printf 'authorized\\n'; fi\n")
	sum := sha256.Sum256(binary)
	pin := "sha256-" + hex.EncodeToString(sum[:])
	arch := nativeConnectorArch()
	native := connectors.DefaultRuntime("darwin", arch, f.n.Environment)
	native.Pins = func(protocol.JSONObject, string) (connectors.ArtifactPins, error) {
		return connectors.ArtifactPins{Archive: pin, Binary: pin}, nil
	}
	native.Download = func(context.Context, []string, string) ([]byte, error) {
		return nil, errors.New("preseeded runtime must not download")
	}
	root := filepath.Join(f.config, "connectors")
	installationPath := filepath.Join(root, "runtime", "wecom", def.String("version")+"-darwin-"+arch, "wecom-cli")
	f.write(t, installationPath, string(binary))
	if err := os.Chmod(installationPath, 0700); err != nil {
		t.Fatal(err)
	}
	reloads := 0
	service, err := p.Connectors(NativeConnectorOptions{MCP: peers, PluginOptions: options, Settings: settingsService, Runtime: &native, ReloadSessions: func(ctx context.Context, _ string, required *protocol.JSONObject) error {
		reloads++
		if required != nil {
			loaded, err := p.RefreshPlugins(ctx, true)
			if err != nil {
				return err
			}
			found := false
			for _, plugin := range loaded.Enabled {
				found = found || plugin.Source == required.String("pluginId")
			}
			if !found {
				return errors.New("required actual installation is not loaded")
			}
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close(context.Background())
	wait := func() {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := service.Wait(ctx, "wecom"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.Action("wecom", "prepare", connectors.ActionOptions{}); err != nil {
		t.Fatal(err)
	}
	wait()
	state, err := service.Get("wecom")
	if err != nil || state.String("status") != "needs-auth" || string(state.Get("installed")) != "true" {
		t.Fatal("actual installation failed", state, err)
	}
	if _, err := service.Action("wecom", "authenticate", connectors.ActionOptions{}); err == nil {
		t.Fatal("shared credential acknowledgement was bypassed")
	}
	if _, err := service.Action("wecom", "authenticate", connectors.ActionOptions{AcknowledgeSharedCredentials: true}); err != nil {
		t.Fatal(err)
	}
	wait()
	state, err = service.Get("wecom")
	if err != nil || state.String("status") != "configured" || state.String("runtime") != "ready" {
		t.Fatal("actual CLI/plugin publication failed", state, err)
	}
	skills, err := p.SkillCommands(NativeSkillOptions{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := p.LoadPlugins(context.Background(), "", false)
	if err != nil {
		t.Fatal(err)
	}
	commands := skills.PluginSkills(context.Background(), result.Enabled)
	found := false
	for _, command := range commands {
		found = found || command.Name() == "office-wecom:office-wecom"
	}
	if !found {
		t.Fatal("connector did not enter actual shared Skill registry")
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "auth show --status\n" {
		t.Fatal("binding reran login instead of inspecting existing account", string(data), err)
	}
	if _, err := service.Action("wecom", "remove", connectors.ActionOptions{}); err != nil {
		t.Fatal(err)
	}
	wait()
	state, err = service.Get("wecom")
	if err != nil || state.String("status") != "not-installed" || string(state.Get("installed")) != "false" {
		t.Fatal(state, err)
	}
	settingsData, err := os.ReadFile(filepath.Join(f.config, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]json.RawMessage
	_ = json.Unmarshal(settingsData, &saved)
	if !strings.Contains(string(saved["future"]), "true") || strings.Contains(string(saved["enabledPlugins"]), "office-wecom") {
		t.Fatal("uninstall lost unknown settings or retained enabled identity", string(settingsData))
	}
	if reloads < 4 {
		t.Fatal("lifecycle skipped actual reload bindings", reloads)
	}
}
