package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/analytics"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// ToggleTerminalVim is the interactive local /vim command's existing global
// configuration consumer. Config and the next terminal input read this store.
func (m *MainPrint) ToggleTerminalVim(ctx context.Context) (string, error) {
	text := ""
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		var err error
		text, err = m.nativeVimCommand(scope)
		return err
	})
	if err == nil {
		err = receipt.Wait()
	}
	return text, err
}

func (m *MainPrint) nativeVimCommand(scope *cooperative.Scope) (string, error) {
	if scope == nil || !scope.BelongsTo(m.Owner) {
		return "", cooperative.ErrRestricted
	}
	global, err := m.Global.Get()
	if err != nil {
		return "", err
	}
	mode := global.String("editorMode")
	if !nativeSettingsTruthy(global.Get("editorMode")) || mode == "emacs" {
		mode = "normal"
	}
	if mode == "normal" {
		mode = "vim"
	} else {
		mode = "normal"
	}
	if err := m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
		next := current.Clone()
		next.Set("editorMode", quoteMain(mode))
		return &next, nil
	}); err != nil {
		return "", err
	}
	text := "Editor mode set to " + mode + ". "
	if mode == "vim" {
		text += "Use Escape key to toggle between INSERT and NORMAL modes."
	} else {
		text += "Using standard (readline) keyboard bindings."
	}
	if report := m.nativeFileReporter(); report != nil {
		if err := report(scope, analytics.Event{Name: "tengu_editor_mode_changed", Metadata: protocol.NewJSONObject(protocol.JSONField{Name: "mode", Value: quoteMain(mode)}, protocol.JSONField{Name: "source", Value: quoteMain("command")})}); err != nil {
			return "", err
		}
	}
	if m.options.OnTerminalEditorModeChanged != nil {
		if err := m.options.OnTerminalEditorModeChanged(mode); err != nil {
			return "", err
		}
	}
	return text, nil
}
