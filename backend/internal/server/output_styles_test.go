package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/systemprompt"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
)

func TestOutputStyleRoutesUseNativeMarkdownAndPreserveSettingsScope(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil })
	options.Store = storage.New(storage.Options{})
	var err error
	options.Settings, err = settings.New(settings.Options{Store: options.Store, HomeDir: options.HomeDir, ConfigDir: options.ConfigDir, Environment: func() map[string]string { return map[string]string{} }, EnsureMigrated: func(context.Context, string) error { return nil }, ShellEnvironment: func(context.Context) (map[string]string, error) { return map[string]string{}, nil }, GitExecutable: "git"})
	if err != nil {
		t.Fatal(err)
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(options.ConfigDir, "output-styles", "custom.md"), "---\nname: private-custom\ndescription: source custom description\n---\nActual custom prompt.")
	write(filepath.Join(options.ConfigDir, "settings.json"), `{"outputStyle":"Explanatory","futureUser":{"keep":true}}`)
	write(filepath.Join(options.WorkDir, ".claude", "settings.json"), `{"outputStyle":"Learning","futureProject":{"keep":true}}`)
	local := filepath.Join(options.WorkDir, ".claude", "settings.local.json")
	write(local, `{"outputStyle":"private-custom","futureLocal":{"keep":true}}`)
	environment, err := clistartupenv.NewEnvironment(protocol.NewJSONObject())
	if err != nil {
		t.Fatal(err)
	}
	options.OutputStyles, err = systemprompt.NewStyleLoader(systemprompt.StyleLoaderOptions{Environment: environment, Platform: runtime.GOOS, Home: options.HomeDir, ConfigHome: options.ConfigDir, ManagedRoot: filepath.Join(options.HomeDir, "managed"), Files: usercontext.OSMemoryFiles{}, ReadDir: usercontext.NativeReadDir, Realpath: filepath.EvalSymlinks, Lstat: os.Lstat, ProjectRoot: func() string { return options.WorkDir }, SourceEnabled: func(string) bool { return true }, Settings: func() (protocol.JSONObject, error) { return protocol.NewJSONObject(), nil }, PluginStyles: func(context.Context) ([]systemprompt.StyleConfig, error) { return nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	_, host := testHost(t, options)
	raw := requestJSON(t, host, "GET", "/api/settings/output-styles", "", 200)
	var listing struct {
		OutputStyle string
		Styles      []outputStyleItem
		Scope       string
		WorkDir     *string
	}
	if json.Unmarshal(raw, &listing) != nil || listing.OutputStyle != "Explanatory" || listing.Scope != "userSettings" || listing.WorkDir != nil || len(listing.Styles) != 4 || listing.Styles[0].Label != "Default" || listing.Styles[3].Description != "source custom description" || listing.Styles[3].Source != "userSettings" {
		t.Fatal(string(raw))
	}
	requestJSON(t, host, "PUT", "/api/settings/output-style", `{"outputStyle":" Explanatory "}`, 400)
	requestJSON(t, host, "PUT", "/api/settings/output-style", `{"outputStyle":"\ufeff"}`, 400)
	requestJSON(t, host, "PUT", "/api/settings/output-style", `{"outputStyle":false}`, 400)
	requestJSON(t, host, "PUT", "/api/settings/output-style", `{"outputStyle":"private-custom"}`, 200)
	body, _ := json.Marshal(map[string]string{"outputStyle": "Learning", "workDir": options.WorkDir})
	raw = requestJSON(t, host, "PUT", "/api/settings/output-style", string(body), 200)
	var saved struct {
		OK          bool
		OutputStyle string
		Scope       string
		WorkDir     string
	}
	if json.Unmarshal(raw, &saved) != nil || !saved.OK || saved.Scope != "localSettings" || saved.WorkDir != options.WorkDir || saved.OutputStyle != "Learning" {
		t.Fatal(string(raw))
	}
	options.Settings.DrainBackground()
	for path, expected := range map[string]string{filepath.Join(options.ConfigDir, "settings.json"): "futureUser", filepath.Join(options.WorkDir, ".claude", "settings.json"): "futureProject", local: "futureLocal"} {
		raw, err := os.ReadFile(path)
		var value storage.Object
		if err != nil || json.Unmarshal(raw, &value) != nil || len(value[expected]) == 0 {
			t.Fatal("unrelated existing settings lost", path, err)
		}
	}
	requestJSON(t, host, "POST", "/api/settings/output-styles", "{}", 405)
	requestJSON(t, host, "GET", "/api/settings/output-style", "", 405)
}
