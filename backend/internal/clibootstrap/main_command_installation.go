package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliinstallation"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/webfetch"
)

func (m *MainCommand) Installation(ctx context.Context, scope *cooperative.Scope, target string, force bool, repository *cliinstallation.RepositoryClient) (cliinstallation.Result, error) {
	if m == nil || !scope.BelongsTo(m.Owner) || !m.Native.NativeProcess {
		return cliinstallation.Result{}, cooperative.ErrRestricted
	}
	settings, err := m.Startup.GetInitialSettings()
	if err != nil {
		return cliinstallation.Result{}, err
	}
	if target == "" {
		target = "latest"
		if settings != nil && settings.String("autoUpdatesChannel") != "" {
			target = settings.String("autoUpdatesChannel")
		}
	}
	if _, err = cliinstallation.ValidateTarget(target); err != nil {
		return cliinstallation.Result{}, err
	}
	tls, err := tlsconfig.NewState(m.Native.readFile)
	if err != nil {
		return cliinstallation.Result{}, err
	}
	client, err := webfetch.NewHTTPClient(m.Native.Environment, tls)
	if err != nil {
		return cliinstallation.Result{}, err
	}
	peer := cliinstallation.RepositoryClient{HTTP: client, APIBase: m.Native.Environment["CC_HAHA_CLI_RELEASE_API"]}
	if repository != nil {
		peer = *repository
	}
	minimum := ""
	if settings != nil {
		minimum = settings.String("minimumVersion")
	}
	installer, err := cliinstallation.New(cliinstallation.Options{Home: m.Native.Home, Environment: m.Native.Environment, Platform: m.credentialPlatform, Architecture: runtime.GOARCH, CurrentVersion: m.version, MinimumVersion: minimum, Repository: peer, Now: m.Native.Now})
	if err != nil {
		return cliinstallation.Result{}, err
	}
	payload, err := scope.AwaitBytes(cooperative.NetworkOpen, func(worker context.Context) ([]byte, error) {
		result, err := installer.Install(worker, target, force)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	})
	if err != nil {
		return cliinstallation.Result{}, err
	}
	var result cliinstallation.Result
	if err = json.Unmarshal(payload, &result); err != nil || result.LockFailed {
		return result, err
	}
	if err = m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
		next := current.Clone()
		if next.String("installMethod") != "native" {
			next.Set("installMethod", quoteMain("native"))
			next.Set("autoUpdates", json.RawMessage(`false`))
			next.Set("autoUpdatesProtectedForNative", json.RawMessage(`true`))
		}
		return &next, nil
	}); err != nil {
		return result, err
	}
	_, err = scope.AwaitBytes(cooperative.TranscriptAppend, func(worker context.Context) ([]byte, error) {
		installer.CleanupOldVersions(worker)
		return nil, nil
	})
	return result, err
}

func (m *MainCommand) SaveInstallationChannel(scope *cooperative.Scope, target string) error {
	if m == nil || !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	if target != "stable" && target != "latest" {
		return nil
	}
	if err := m.Settings.ensureSettings(); err != nil {
		return err
	}
	patch := protocol.NewJSONObject(protocol.JSONField{Name: "autoUpdatesChannel", Value: quoteMain(target)})
	result, err := m.Settings.settings.UpdateEditableSource("userSettings", "", clisettings.Patch{Object: patch})
	if err == nil {
		err = result.Error
	}
	if err == nil {
		m.Settings.settingsState.ResetSettingsCache()
	}
	return err
}

func (m *MainCommand) InstallationChannel(scope *cooperative.Scope) (string, error) {
	if m == nil || !scope.BelongsTo(m.Owner) {
		return "", cooperative.ErrRestricted
	}
	settings, err := m.Startup.GetInitialSettings()
	channel := "latest"
	if settings != nil && settings.String("autoUpdatesChannel") != "" {
		channel = settings.String("autoUpdatesChannel")
	}
	return channel, err
}

func (m *MainCommand) InstallationSummary(scope *cooperative.Scope) (string, error) {
	if m == nil || !scope.BelongsTo(m.Owner) || !m.Native.NativeProcess {
		return "", cooperative.ErrRestricted
	}
	global, err := m.Global.Get()
	if err != nil {
		return "", err
	}
	installer, err := cliinstallation.New(cliinstallation.Options{Home: m.Native.Home, Environment: m.Native.Environment, Platform: m.credentialPlatform, CurrentVersion: m.version})
	if err != nil {
		return "", err
	}
	method := "not set"
	if global != nil && global.String("installMethod") != "" {
		method = global.String("installMethod")
	}
	channel, err := m.InstallationChannel(scope)
	if err != nil {
		return "", err
	}
	health := installer.Health()
	text := fmt.Sprintf("Diagnostics\n\nCurrently running: Go native (%s)\nInstallation path: %s\nConfig install method: %s\nUpdates channel: %s\nNative executable: %s\n", m.version, health.InvokedBinary, method, channel, health.Executable)
	if health.Error != "" {
		text += "Warning: " + health.Error + "\nFix: Run claude install\n"
	}
	if !health.PathInShell {
		text += "Warning: Native installation exists but ~/.local/bin is not in your PATH\nFix: Add ~/.local/bin to your PATH and restart your terminal\n"
	}
	return text, err
}
