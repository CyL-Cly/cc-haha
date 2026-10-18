package usercontext

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const ExplicitUserMemoryProfile = "original-user-only-plain-claude-md-no-policy-rules-includes-automemory"
const memoryInstructionPrompt = "Codebase and user instructions are shown below. Be sure to adhere to these instructions. IMPORTANT: These instructions OVERRIDE any default behavior and you MUST follow them exactly as written."

// MemoryFiles is the native capability, not a formatted context callback.
// A fixture may delay a genuine ReadFile completion, never supply rendered text.
type MemoryFiles interface {
	ReadFile(string) ([]byte, error)
	Stat(string) (fs.FileInfo, error)
}
type OSMemoryFiles struct{}

func (OSMemoryFiles) ReadFile(path string) ([]byte, error)  { return os.ReadFile(path) }
func (OSMemoryFiles) Stat(path string) (fs.FileInfo, error) { return os.Stat(path) }

type ExplicitUserMemory struct {
	ConfigHome, Profile string
	Normal              *NormalMemoryDiscovery
	Files               MemoryFiles
}

// NativeMemoryPrimitives are per-continuation OS operations. The original
// discovery descriptor and its memo remain shared; only physical operations
// and observing an already-running memo use this continuation's Scope.
type NativeMemoryPrimitives interface {
	MemoryFiles
	ReadDir(string) ([]fs.DirEntry, error)
	Realpath(string) (string, error)
	MemoryDirectory() (string, error)
	WaitMemory(<-chan struct{}) error
	MemoryError() error
	SyncMemoryFiles() MemoryFiles
	SyncMemoryRealpath(string) (string, error)
}

func (m *ExplicitUserMemory) nativeReadDir(path string) ([]fs.DirEntry, error) {
	if files, ok := m.Files.(NativeMemoryPrimitives); ok {
		return files.ReadDir(path)
	}
	return m.Normal.ReadDir(path)
}
func (m *ExplicitUserMemory) nativeResolvePath(path string) (string, error) {
	if files, ok := m.Files.(NativeMemoryPrimitives); ok {
		return files.SyncMemoryRealpath(path)
	}
	if m.Normal.Realpath != nil {
		return m.Normal.Realpath(path)
	}
	return filepath.EvalSymlinks(path)
}
func (m *ExplicitUserMemory) nativeSynchronousFiles() MemoryFiles {
	if files, ok := m.Files.(NativeMemoryPrimitives); ok {
		return files.SyncMemoryFiles()
	}
	return m.Files
}
func (m *ExplicitUserMemory) waitMemory(done <-chan struct{}) error {
	if files, ok := m.Files.(NativeMemoryPrimitives); ok {
		return files.WaitMemory(done)
	}
	<-done
	return nil
}

func (m *ExplicitUserMemory) load(mode string, additional []string) (json.RawMessage, error) {
	if m.Profile == NormalEmptyMemoryProfile {
		return m.loadNormal(mode, additional)
	}
	if m.Profile != ExplicitUserMemoryProfile || m.Files == nil || !filepath.IsAbs(m.ConfigHome) || mode != "claude-md" || len(additional) == 0 {
		return nil, &CapabilityError{"explicit user memory host/mode"}
	}
	if _, err := m.Files.Stat(filepath.Join(m.ConfigHome, "rules")); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return nil, &CapabilityError{"user memory rules"}
	}
	path := filepath.Join(m.ConfigHome, "CLAUDE.md")
	content, err := m.Files.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, &CapabilityError{"memory native read error projection"}
	}
	if !utf8.Valid(content) {
		return nil, &CapabilityError{"memory invalid UTF-8 decoder"}
	}
	text := string(content)
	if strings.Contains(text, "@") || strings.Contains(text, "<!--") || strings.HasPrefix(text, "---") || strings.Contains(text, "\x00") {
		return nil, &CapabilityError{"memory markdown includes/frontmatter/comments"}
	}
	text = strings.TrimFunc(text, func(r rune) bool {
		return r >= 9 && r <= 13 || r == ' ' || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
	})
	if text == "" {
		return nil, nil
	}
	return quote(memoryInstructionPrompt + "\n\nContents of " + path + " (user's private global instructions for all projects):\n\n" + text), nil
}
