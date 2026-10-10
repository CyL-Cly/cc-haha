package clibootstrap

import (
	"context"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
	"github.com/NanmiCoder/cc-haha/backend/internal/settingswatch"
)

func (m *MainPrint) startNativeSettingsWatcher(scope *cooperative.Scope) error {
	if !m.options.Native.NativeProcess || m.agentChild || m.settingsWatcher != nil {
		return nil
	}
	if m.options.Native.mdm == nil {
		return mainCap("settings notifier shared native MDM cache")
	}
	files := []settingswatch.SourceFile{}
	for _, source := range []string{"userSettings", "projectSettings", "localSettings"} {
		path, err := m.settings.Path(source, "")
		if err != nil {
			return err
		}
		files = append(files, settingswatch.SourceFile{Source: source, Path: path})
	}
	files = append(files, settingswatch.SourceFile{Source: "policySettings", Path: filepath.Join(m.options.Native.ManagedRoot, "managed-settings.json")})
	future, err := nativeSkillWork(m, scope, func() (*settingswatch.Watcher, error) {
		return settingswatch.Start(settingswatch.Options{Files: files, DropInDirectory: filepath.Join(m.options.Native.ManagedRoot, "managed-settings.d"), Change: m.nativeSettingsFileChanged, ConsumeInternalWrite: func(path string) bool { return m.settingState.ConsumeInternalWrite(path, 5000) }, ClearInternalWrites: m.settingState.ClearInternalWrites, MDM: m.options.Native.settingsMDMSnapshot, InstallMDM: m.options.Native.installSettingsMDMSnapshot, MDMChanged: func(ctx context.Context) error { return m.nativeSettingsFileChanged(ctx, "policySettings", "") }, Error: func(err error) { m.environmentHookNotice(err.Error(), true) }})
	})
	if err != nil {
		return err
	}
	m.settingsWatcher, err = future.Await(scope)
	return err
}

func (m *MainPrint) nativeSettingsFileChanged(ctx context.Context, source, path string) error {
	receipt, err := m.Owner.Submit(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if m.closed || m.shuttingDown {
			return nil
		}
		previous, _, err := m.Controller.SDKSettingsOnOwner(scope)
		if err != nil {
			return err
		}
		if path != "" {
			results, err := m.hook.ExecuteConfigChange(ctx, settingswatch.ConfigSource(source), quoteMain(path), queryhooks.Context{Scope: scope})
			if err != nil {
				return err
			}
			if queryhooks.HasBlockingOutsideResult(results) {
				return nil
			}
		}
		// fanOut is the single producer of cache reset. MDM notifications skip
		// file ConfigChange just as the original process polling branch does.
		m.settingState.ResetSettingsCache()
		if err := m.refreshNativeSandbox(scope); err != nil {
			return err
		}
		if err := m.hook.UpdateSnapshot(); err != nil {
			return err
		}
		rules, err := m.nativePermissionRules()
		if err != nil {
			return err
		}
		if err := m.Host.ApplyNativeSettingsChange(scope, rules); err != nil {
			return err
		}
		next, _, err := m.Controller.SDKSettingsOnOwner(scope)
		if err != nil {
			return err
		}
		// Reparsed JSON env objects have a new JS identity even when their
		// bytes are equal. Two absent env properties remain equal undefined.
		if m.apiKeyHelper != nil {
			m.apiKeyHelper.Clear()
		}
		m.clearNativeCloudCredentialCaches()
		if settingsEnvironmentReferenceChanged(previous, next) {
			if err := m.nativeSettingsEnvironmentChanged(scope); err != nil {
				m.environmentHookNotice(err.Error(), true)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	select {
	case <-receipt.Done():
		return receipt.Wait()
	case <-ctx.Done():
		return ctx.Err()
	}
}
