package clibootstrap

import (
	"io/fs"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Source retains the requested user-root spelling in Agent metadata. Resolve
// it against the actual process directory only at the native filesystem call.
type nativeAgentDirectoryFiles struct {
	nativeAgentFiles
	process *NativeBoundary
}

func (f nativeAgentDirectoryFiles) Stat(path string) (fs.FileInfo, error) {
	return f.nativeAgentFiles.Stat(f.process.nativePhysicalPath(path))
}
func (f nativeAgentDirectoryFiles) Lstat(path string) (fs.FileInfo, error) {
	return f.nativeAgentFiles.Lstat(f.process.nativePhysicalPath(path))
}
func (f nativeAgentDirectoryFiles) ReadFile(path string) ([]byte, error) {
	return f.nativeAgentFiles.ReadFile(f.process.nativePhysicalPath(path))
}
func (f nativeAgentDirectoryFiles) ReadDir(path string) ([]fs.DirEntry, error) {
	return f.nativeAgentFiles.ReadDir(f.process.nativePhysicalPath(path))
}
func (f nativeAgentDirectoryFiles) Realpath(path string) (string, error) {
	return f.nativeAgentFiles.Realpath(f.process.nativePhysicalPath(path))
}

func (p *NativeProcessSettings) nativeAgentSettingsReader() (func(string) (*protocol.JSONObject, error), error) {
	// Bind the existing original source getter once, before discovery can yield.
	// Its parsed/source cache lifetime is independent of Agent cache clearing.
	return p.startup.NativeAgentSettingsReader(p.settings, p.settingsState, p.environment)
}

func (p *NativeProcessSettings) nativeAgentFindMarkdown(scoped *NativeBoundary, path string) ([]string, error) {
	physical := p.n.nativePhysicalPath(path)
	files, err := nativeAgentMarkdown(scoped, physical)
	if err != nil || physical == path {
		return files, err
	}
	for index, file := range files {
		relative, err := filepath.Rel(physical, file)
		if err != nil {
			return nil, err
		}
		files[index] = filepath.Join(path, relative)
	}
	return files, nil
}
