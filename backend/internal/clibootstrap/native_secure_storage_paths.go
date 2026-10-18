package clibootstrap

import (
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"golang.org/x/text/unicode/norm"
)

func (m *MainPrint) nativePlaintextLocation() (directory, path string) {
	n := m.options.Native
	directory, exists := n.nativeEnvironmentValue("CLAUDE_CONFIG_DIR")
	if !exists {
		directory = filepath.Join(n.Home, ".claude")
	}
	directory = norm.NFC.String(directory)
	path = filepath.Join(directory, ".credentials.json")
	if !filepath.IsAbs(path) {
		path = filepath.Join(n.CWD, path)
	}
	if directory != "" && !filepath.IsAbs(directory) {
		directory = filepath.Join(n.CWD, directory)
	}
	return directory, path
}

func (m *MainPrint) nativeKeychainServiceName() string {
	n := m.options.Native
	environment := n.nativeEnvironmentSnapshot()
	config, exists := environment["CLAUDE_CONFIG_DIR"]
	if !exists {
		config = filepath.Join(n.Home, ".claude")
	}
	return securestorage.ServiceName(norm.NFC.String(config), exists && config != "", environment)
}

func (m *MainPrint) nativeKeychainUser() (string, bool) {
	return m.options.Native.nativeEnvironmentValue("USER")
}

func (m *MainPrint) nativeSecurityContext() (string, map[string]string) {
	n := m.options.Native
	return n.CWD, n.nativeEnvironmentSnapshot()
}
