package application

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/migrations"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/server"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestDesktopMemoryLaunchMetadataUpdatesOnlyAfterAcceptedHostControls(t *testing.T) {
	ctx := context.Background()
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		response(w, "memory-launch", "", nil, "MEMORY TURN OK")
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"cleanupPeriodDays":-0}`), 0600); err != nil {
		t.Fatal(err)
	}
	managed := o.Native.ManagedRoot
	o.NativeFactory = func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return newIsolatedProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: managed, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}
	store := storage.New(storage.Options{})
	upgrader, err := migrations.New(migrations.Options{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	settingsOptions := settings.Options{Store: store, HomeDir: o.Environment["HOME"], ConfigDir: config, ProjectRoot: o.WorkDir, Environment: func() map[string]string { return o.Environment }, EnsureMigrated: upgrader.SettingsCallback, GitExecutable: "git"}
	settingsOptions.ShellEnvironment = newTerminalEnvironment(o.Environment).Get
	service, err := settings.New(settingsOptions)
	if err != nil {
		t.Fatal(err)
	}
	managedSettings, err := settings.NewManaged(settingsOptions)
	if err != nil {
		t.Fatal(err)
	}
	o.ProviderOptions = &providerconfig.Options{Store: store, ConfigDir: config, Settings: service, Managed: managedSettings}
	providers := filepath.Join(config, "cc-haha", "providers.json")
	if err = os.MkdirAll(filepath.Dir(providers), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(providers, raw(map[string]any{"schemaVersion": 6, "activeId": "memory-provider", "providers": []any{map[string]any{"id": "memory-provider", "presetId": "custom", "name": "private loopback", "baseUrl": provider.URL, "apiKey": "fake-memory-key", "apiFormat": "anthropic", "authStrategy": "api_key", "models": map[string]string{"main": "fixture-model", "haiku": "", "sonnet": "", "opus": ""}}}}), 0600); err != nil {
		t.Fatal(err)
	}
	updates := []json.RawMessage{}
	live, err := DesktopFactory(o)(ctx, server.SessionOptions{ID: o.Arguments.SessionID, WorkDir: o.WorkDir, UpdateLaunchMetadata: func(ctx context.Context, data json.RawMessage) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		updates = append(updates, append(json.RawMessage(nil), data...))
		return nil
	}}, func(json.RawMessage) {})
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close(ctx)
	if err = live.Handle(ctx, raw(map[string]any{"type": "set_permission_mode", "mode": "unmigrated-mode"})); err == nil || len(updates) != 0 {
		t.Fatalf("rejected permission mode updated launch metadata: %v %+v", err, updates)
	}
	if err = live.Handle(ctx, raw(map[string]any{"type": "set_permission_mode", "mode": "plan"})); err != nil || len(updates) != 1 || obj(updates[0]).String("permissionMode") != "plan" {
		t.Fatalf("accepted permission mode lost memory launch state: %v %+v", err, updates)
	}
	if err = live.Handle(ctx, raw(map[string]any{"type": "set_runtime_config", "providerId": "memory-provider", "modelId": "fixture-model", "effortLevel": "invalid-effort"})); err != nil || len(updates) != 1 {
		t.Fatalf("rejected runtime selection updated launch state: %v %+v", err, updates)
	}
	if err = live.Handle(ctx, raw(map[string]any{"type": "set_runtime_config", "providerId": "memory-provider", "modelId": "fixture-model"})); err != nil || len(updates) != 2 {
		t.Fatalf("accepted runtime lost memory launch state: %v %+v", err, updates)
	}
	meta := obj(updates[1])
	if meta.String("runtimeProviderId") != "memory-provider" || meta.String("runtimeModelId") != "fixture-model" {
		t.Fatalf("wrong runtime launch metadata: %s", updates[1])
	}
	for _, key := range []string{"type", "isMeta", "workDir", "timestamp", "apiKey"} {
		if meta.Get(key) != nil {
			t.Fatalf("transcript or credential field leaked into launch metadata: %s", key)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("host controls made a model request")
	}
	if err = live.Handle(ctx, raw(map[string]any{"type": "user_message", "content": "Use the accepted memory runtime."})); err != nil || calls.Load() != 1 {
		t.Fatalf("accepted memory runtime did not execute actual shared Harness: %v %d", err, calls.Load())
	}
	if err = live.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(config, "projects")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cleanupPeriodDays -0 wrote a transcript directory: %v", err)
	}
}
