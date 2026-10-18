package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func terminalExpandedView(global protocol.JSONObject) string {
	if nativeSettingsTruthy(global.Get("showSpinnerTree")) {
		return "teammates"
	}
	if nativeSettingsTruthy(global.Get("showExpandedTodos")) {
		return "tasks"
	}
	return "none"
}

func (m *MainPrint) TerminalExpandedView(ctx context.Context) (string, error) {
	if m == nil || m.Owner == nil {
		return "", cooperative.ErrRestricted
	}
	var value string
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if !m.terminalFullscreenBound(scope) {
			return cooperative.ErrRestricted
		}
		global, err := m.Global.Get()
		if err == nil {
			value = terminalExpandedView(*global)
		}
		return err
	})
	if err == nil {
		err = receipt.Wait()
	}
	return value, err
}

// onChangeAppState writes these existing backwards-compatible global fields
// synchronously on a committed expandedView transition. No settings or ASK
// cache is reloaded; other global fields retain their actual current values.
func (m *MainPrint) SetTerminalExpandedView(ctx context.Context, value string) error {
	receipt, err := m.EnqueueTerminalExpandedView(ctx, value)
	if err == nil {
		err = receipt.Wait()
	}
	return err
}

// EnqueueTerminalExpandedView admits the same concrete state/global write in
// caller order. The terminal may keep consuming an Owner's current output
// while waiting for this receipt; the synchronous API above remains unchanged.
func (m *MainPrint) EnqueueTerminalExpandedView(ctx context.Context, value string) (*cooperative.Receipt, error) {
	if m == nil || m.Owner == nil || value != "none" && value != "tasks" && value != "teammates" {
		return nil, cooperative.ErrRestricted
	}
	return m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if !m.terminalFullscreenBound(scope) {
			return cooperative.ErrRestricted
		}
		if err := m.Controller.SetTerminalExpandedViewOnOwner(scope, value); err != nil {
			return err
		}
		return m.saveNativeTerminalExpandedViewOnOwner(scope, value)
	})
}

func (m *MainPrint) saveNativeTerminalExpandedViewOnOwner(scope *cooperative.Scope, value string) error {
	if !m.terminalFullscreenBound(scope) {
		return cooperative.ErrRestricted
	}
	todos, tree := value == "tasks", value == "teammates"
	return m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
		if string(current.Get("showExpandedTodos")) == string(rawMain(todos)) && string(current.Get("showSpinnerTree")) == string(rawMain(tree)) {
			return current, nil
		}
		next := current.Clone()
		next.Set("showExpandedTodos", rawMain(todos))
		next.Set("showSpinnerTree", rawMain(tree))
		return &next, nil
	})
}
