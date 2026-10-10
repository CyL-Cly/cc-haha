package clibootstrap

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
)

// Called only by the admitted MainCommand stack. OAuth and API-key cache slots
// stay separate, including a cached empty result after a drained/invalid FD.
func (m *MainCommand) credentialFromDescriptor(key, filename string) string {
	if value, loaded := m.credentialCache[key]; loaded {
		return value
	}
	n := m.Native
	value := desktopoauth.ReadCredentialDescriptor(desktopoauth.CredentialDescriptorOptions{
		Descriptor: n.Environment[key], Platform: m.credentialPlatform,
		FallbackPath: filepath.Join(m.credentialDirectory, filename), Remote: envTruthyNative(n.Environment["CLAUDE_CODE_REMOTE"]),
		ReadFile: n.readFile,
		MkdirAll: func(path string, mode fs.FileMode) error {
			if err := n.check("command CCR credential mkdir", path); err != nil {
				return err
			}
			return os.MkdirAll(path, mode)
		},
		WriteFile: func(path string, data []byte, mode fs.FileMode) error {
			if err := n.check("command CCR credential write", path); err != nil {
				return err
			}
			return os.WriteFile(path, data, mode)
		},
	})
	m.credentialCache[key] = value
	return value
}
