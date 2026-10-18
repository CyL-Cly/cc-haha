package clibootstrap

import (
	"context"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/mcpauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func newNativeMCPAuthenticationCache(n *NativeBoundary) (*mcpruntime.AuthenticationCache, error) {
	path := func() string {
		directory := n.ConfigHomeDirectory()
		if directory == "" {
			directory = filepath.Join(n.Home, ".claude")
		}
		if !filepath.IsAbs(directory) {
			directory = filepath.Join(n.CWD, directory)
		}
		return filepath.Join(directory, "mcp-needs-auth-cache.json")
	}
	files := mainResumeFiles{n: n}
	options := mcpruntime.AuthenticationCacheOptions{Path: path(), ReadFile: n.readFile, WriteFile: files.WriteFile, MkdirAll: files.MkdirAll, Remove: (mainSettingsFiles{n: n}).Remove, NowMillis: n.NowMillis}
	if n.NativeProcess {
		options.NativePath = path
	}
	return mcpruntime.NewAuthenticationCache(options)
}

func (m *MainPrint) nativeMCPAuthOptions(platform string) mcpauth.Options {
	n := m.options.Native
	var currentDirectory func() string
	if n.NativeProcess {
		currentDirectory = n.ConfigHomeDirectory
	}
	return mcpauth.Options{NativeConfigDirectory: currentDirectory, OnDebug: m.reportNativeMCPDebug, Secure: m.secure, Home: n.Home, ConfigDir: n.ConfigHomeDirectory(), CWD: n.CWD, Platform: platform, Environment: n.Environment, Now: n.Now, Settings: func(ctx context.Context) (protocol.JSONObject, error) {
		if err := ctx.Err(); err != nil {
			return protocol.JSONObject{}, err
		}
		value, err := m.nativeSettings.InitialSettings()
		if value == nil {
			return protocol.NewJSONObject(), err
		}
		return value.Clone(), err
	}}
}

func (m *MainPrint) newNativeMCPAuth(platform string) (*mcpauth.Service, error) {
	return mcpauth.New(m.nativeMCPAuthOptions(platform))
}
