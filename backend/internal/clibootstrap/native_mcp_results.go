package clibootstrap

import (
	"os"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpruntime"
)

func (m *MainPrint) nativeMCPResultOptions(scope *cooperative.Scope) (mcpruntime.RichResultOptions, error) {
	if !scope.BelongsTo(m.Owner) || m.options.Native == nil {
		return mcpruntime.RichResultOptions{}, cooperative.ErrRestricted
	}
	directory, e := m.Controller.ToolResultsDirectoryOnOwner(scope)
	if e != nil {
		return mcpruntime.RichResultOptions{}, e
	}
	n := m.options.Native
	// No Controller, session state or owner callbacks cross into the image/IO
	// worker. Only the original immutable directory and native primitives do.
	return mcpruntime.RichResultOptions{Directory: directory, ImageLibraryPath: n.ImageLibraryPath, NowMillis: n.NowMillis, Random: nativeMathRandom, MkdirAll: func(path string) error {
		if e := n.check("mkdir", path); e != nil {
			return e
		}
		return os.MkdirAll(path, 0777)
	}, WriteFile: func(path string, data []byte) error {
		if e := n.check("writeFile", path); e != nil {
			return e
		}
		return os.WriteFile(path, data, 0666)
	}}, nil
}
