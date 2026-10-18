package clibootstrap

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func (m *MainPrint) NativeKeybindingsEnabled() bool {
	return m != nil && m.options.Interactive && m.options.KeybindingCustomizationEnabled != nil && m.options.KeybindingCustomizationEnabled()
}

func (m *MainPrint) nativeKeybindingsCommand(ctx context.Context, scope *cooperative.Scope) (string, error) {
	if m == nil || !scope.BelongsTo(m.Owner) || m.options.Native == nil || !m.options.Native.NativeProcess || !m.options.Interactive || m.agentChild || m.teamWorker != nil {
		return "", cooperative.ErrRestricted
	}
	if !m.NativeKeybindingsEnabled() {
		return "Keybinding customization is not enabled. This feature is currently in preview.", nil
	}
	native := m.options.Native
	cwd := m.state.Snapshot().CWD
	path, physical := native.TerminalKeybindingsPaths(cwd)
	platform := m.options.Platform
	if platform == "darwin" {
		platform = "macos"
	} else if platform == "win32" {
		platform = "windows"
	}
	directory := filepath.Dir(physical)
	making, err := nativeSkillWork(m, scope, func() (struct{}, error) {
		if err := native.check("mkdirAll", directory); err != nil {
			return struct{}{}, err
		}
		if err := os.MkdirAll(directory, 0777); err != nil {
			return struct{}{}, terminalKeybindingsFileError(err, directory, filepath.Dir(path))
		}
		return struct{}{}, nil
	})
	if err != nil {
		return "", err
	}
	if _, err := making.Await(scope); err != nil {
		return "", err
	}
	// Source has a distinct writeFile await after the mkdir reaction, and creates
	// its template at this stage. Keep both original native readiness boundaries.
	template, err := tui.KeybindingsTemplate(platform, true, tui.KeybindingFeatures{})
	if err != nil {
		return "", err
	}
	future, err := nativeSkillWork(m, scope, func() (bool, error) {
		if err := native.check("writeFile:wx", physical); err != nil {
			return false, err
		}
		fileExists := false
		file, err := os.OpenFile(physical, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0666)
		if errors.Is(err, os.ErrExist) {
			fileExists = true
		} else if err != nil {
			return false, terminalKeybindingsFileError(err, physical, path)
		} else {
			_, err = file.WriteString(template)
			closeErr := file.Close()
			if err != nil {
				return false, terminalKeybindingsFileError(err, physical, path)
			}
			if closeErr != nil {
				return false, terminalKeybindingsFileError(closeErr, physical, path)
			}
		}
		return fileExists, nil
	})
	if err != nil {
		return "", err
	}
	fileExists, err := future.Await(scope)
	if err != nil {
		return "", err
	}
	// Original editFileInEditor selects its memoized editor only after both
	// filesystem awaits; later environment updates must be visible here.
	editor := m.terminalExternalEditor()
	editorError := ""
	if editor != "" {
		if m.options.EditTerminalFile == nil {
			return "", mainCap("terminal file editor handoff")
		}
		environment := native.nativeEnvironmentSnapshot()
		cwd = m.state.Snapshot().CWD
		editing, err := nativeSkillWork(m, scope, func() (string, error) { return m.options.EditTerminalFile(ctx, path, editor, cwd, environment) })
		if err != nil {
			return "", err
		}
		editorError, err = editing.Await(scope)
		if err != nil {
			return "", err
		}
	}
	if editorError != "" {
		verb := "Created"
		if fileExists {
			verb = "Opened"
		}
		return verb + " " + path + ". Could not open in editor: " + editorError, nil
	}
	if fileExists {
		return "Opened " + path + " in your editor.", nil
	}
	return "Created " + path + " with template. Opened in your editor.", nil
}
