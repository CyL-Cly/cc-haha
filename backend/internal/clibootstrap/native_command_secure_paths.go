package clibootstrap

import (
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"golang.org/x/text/unicode/norm"
)

// These are the command's same live process operands, not a second Store or a
// caller-supplied cache prime. The prefetch ticket captures its two identities
// before Safe settings can change the later synchronous credentials context.
func (m *MainCommand) nativeCommandPlaintextLocation() (directory, path string) {
	env := m.nativeCommandEnvironment()
	directory, exists := env["CLAUDE_CONFIG_DIR"]
	if !exists {
		directory = filepath.Join(m.Native.Home, ".claude")
	}
	directory = norm.NFC.String(directory)
	path = filepath.Join(directory, ".credentials.json")
	if !filepath.IsAbs(path) {
		path = filepath.Join(m.Native.CWD, path)
	}
	if directory != "" && !filepath.IsAbs(directory) {
		directory = filepath.Join(m.Native.CWD, directory)
	}
	return directory, path
}

func (m *MainCommand) nativeCommandKeychainServiceName() string {
	env := m.nativeCommandEnvironment()
	config, exists := env["CLAUDE_CONFIG_DIR"]
	if !exists {
		config = filepath.Join(m.Native.Home, ".claude")
	}
	return securestorage.ServiceName(norm.NFC.String(config), exists && config != "", env)
}

func (m *MainCommand) nativeCommandKeychainUser() (string, bool) {
	user, exists := m.nativeCommandEnvironment()["USER"]
	return user, exists
}

func (m *MainCommand) nativeCommandSecurityContext() (string, map[string]string) {
	return m.Native.CWD, m.nativeCommandEnvironment()
}
