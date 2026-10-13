package cliinstallation

import (
	"os"
	"path/filepath"
	"strings"
)

type Health struct {
	InvokedBinary, Executable, Error string
	PathInShell                      bool
}

func (s *Installer) Health() Health {
	current, _ := os.Executable()
	health := Health{InvokedBinary: current, Executable: s.dirs.Executable}
	if resolved, err := filepath.EvalSymlinks(current); err == nil {
		health.InvokedBinary = resolved
	}
	if err := s.validateBinary(s.dirs.Executable); err != nil {
		health.Error = err.Error()
	}
	separator := ":"
	if s.options.Platform == "windows" || s.options.Platform == "win32" {
		separator = ";"
	}
	bin := filepath.Dir(s.dirs.Executable)
	for _, entry := range strings.Split(s.options.Environment["PATH"], separator) {
		if entry == "~/.local/bin" || entry == "$HOME/.local/bin" {
			health.PathInShell = true
			break
		}
		entry = filepath.Clean(entry)
		if entry == bin || separator == ";" && strings.EqualFold(entry, bin) {
			health.PathInShell = true
			break
		}
	}
	return health
}
