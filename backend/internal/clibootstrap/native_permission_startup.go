package clibootstrap

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
)

func (m *MainPrint) prepareNativePermissions(settings *protocol.JSONObject) error {
	n := m.options.Native
	if !n.NativeProcess {
		return nil
	}
	producer, e := sdkpermissions.NewNativeStartup(m.Owner, sdkpermissions.NativeStartupOptions{SettingsForSource: m.Startup.GetSettingsForSource, InitialSettings: m.Startup.GetInitialSettings, Home: n.Home, CWD: n.CWD, OriginalCWD: n.CWD, Platform: m.options.Platform, Environment: func(key string) string {
		var value string
		_ = json.Unmarshal(m.environment.Get(key), &value)
		return value
	}, Global: m.Global.Get, Policy: func() (*protocol.JSONObject, error) { return m.Startup.GetSettingsForSource("policySettings") }, Stat: n.stat, Lstat: n.lstat, Realpath: func(path string) (string, error) {
		if e := n.check("realpath", path); e != nil {
			return "", e
		}
		return filepath.EvalSymlinks(path)
	}})
	if e != nil {
		return e
	}
	var result sdkpermissions.NativeStartupResult
	e = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		result, e = producer.Prepare(scope, settings, sdkpermissions.NativeStartupRequest{Mode: m.options.Arguments.PermissionMode, DangerouslySkipPermissions: m.options.Arguments.DangerouslySkipPermissions, AllowDangerouslySkipPermissions: m.options.Arguments.AllowDangerouslySkipPermissions, AdditionalDirectories: m.options.Arguments.AdditionalDirectories})
		if e != nil {
			return e
		}
		if result.Notification != "" {
			_, _ = io.WriteString(m.options.Error, result.Notification+"\n")
		}
		for _, warning := range result.Warnings {
			_, _ = io.WriteString(m.options.Error, warning+"\n")
		}
		return nil
	})
	if e != nil {
		return e
	}
	m.nativePermissionStartup, m.nativePermissionInitial = producer, result
	return nil
}

// Source bootstrap stores only raw CLI --add-dir values for CLAUDE.md/Skill
// discovery; saved permission directories and PWD aliases do not enter it.
// Native FS has an explicit cwd, so anchor relative spellings without applying
// permission expandPath's trim/home expansion or existence filtering.
func (m *MainPrint) nativeInstructionDirectories() []string {
	paths := []string{}
	for _, path := range m.nativePermissionInitial.InstructionDirectories {
		if !filepath.IsAbs(path) {
			path = filepath.Join(m.options.Native.CWD, path)
		}
		paths = append(paths, path)
	}
	return paths
}
