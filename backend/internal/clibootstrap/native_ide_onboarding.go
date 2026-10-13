package clibootstrap

import (
	"context"
	"errors"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ideintegration"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

type nativeIDEUI struct {
	next      uint64
	pending   *tui.IDEPicker
	status    *ideintegration.ExtensionStatus
	listeners map[uint64]func(*tui.IDEPicker)
}

func (m *MainPrint) nativeIDEUIOnOwner() *nativeIDEUI {
	if m.ideUI == nil {
		m.ideUI = &nativeIDEUI{listeners: map[uint64]func(*tui.IDEPicker){}}
	}
	return m.ideUI
}

func (m *MainPrint) runNativeIDEExtension(ctx context.Context, state *nativeIDERuntime, kind string) {
	wasInstalled := state.extensions.Installed(ctx, kind)
	var status *ideintegration.ExtensionStatus
	if ideintegration.VSCodeIDE(kind) {
		value := state.extensions.Install(ctx, kind)
		status = &value
	}
	if ctx.Err() != nil || !state.active() {
		return
	}
	if status != nil && status.Installed {
		m.startNativeIDESearch(state)
	}
	receipt, e := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if ctx.Err() != nil || !state.active() || m.closed || m.shuttingDown {
			return nil
		}
		ui := m.nativeIDEUIOnOwner()
		ui.status = status
		global, e := m.Global.Get()
		if e != nil {
			return e
		}
		if status != nil && status.Installed && !planRawTruthy(global.Get("diffTool")) {
			if e = m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
				next := current.Clone()
				next.Set("diffTool", quoteMain("auto"))
				return &next, nil
			}); e != nil {
				return e
			}
		}
		shouldShow := ideintegration.VSCodeIDE(kind) && !wasInstalled && status != nil && status.Installed || ideintegration.JetBrainsIDE(kind) && wasInstalled
		if !shouldShow {
			return nil
		}
		env := m.nativeOpenAIEnvironment()
		terminal := ideintegration.Terminal(env)
		if terminal == "" {
			terminal = "unknown"
		}
		global, e = m.Global.Get()
		if e != nil {
			return e
		}
		shown := nativeInProcessObject(global.Get("hasIdeOnboardingBeenShown"))
		if string(shown.Get(terminal)) == "true" {
			return nil
		}
		id := uuidMain()
		m.ideOnboarding = &nativeIDEPending{id: id, mode: "onboarding", currentName: terminal}
		props := tui.IDEPicker{ID: id, Mode: "onboarding", Title: "Welcome to Claude Code for " + ideintegration.IDEName(kind), Description: "Claude has context of open files and selected lines. Review Claude Code's changes in your IDE. Press Enter to continue."}
		if status != nil && status.Version != nil {
			props.Description = "Installed extension v" + *status.Version + "\n" + props.Description
		}
		ui.pending = &props
		for _, listener := range ui.listeners {
			copy := props
			listener(&copy)
		}
		return nil
	})
	if e != nil && ctx.Err() == nil {
		m.reportNativeIDEFailure(e)
	} else if e == nil {
		go func() {
			if e := receipt.Wait(); e != nil && state.active() && ctx.Err() == nil {
				m.reportNativeIDEFailure(e)
			}
		}()
	}
}
func (m *MainPrint) SubscribeIDEOnboarding(ctx context.Context, listener func(*tui.IDEPicker)) (*tui.IDEPicker, func(), error) {
	var initial *tui.IDEPicker
	var id uint64
	receipt, e := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(*cooperative.Scope) error {
		ui := m.nativeIDEUIOnOwner()
		if ui.pending != nil {
			copy := *ui.pending
			initial = &copy
		}
		if listener != nil {
			ui.next++
			id = ui.next
			ui.listeners[id] = listener
		}
		return nil
	})
	if e == nil {
		e = receipt.Wait()
	}
	if e != nil {
		return nil, nil, e
	}
	var once sync.Once
	return initial, func() {
		once.Do(func() {
			receipt, e := m.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(*cooperative.Scope) error {
				if m.ideUI != nil {
					delete(m.ideUI.listeners, id)
				}
				return nil
			})
			if e == nil {
				_ = receipt.Wait()
			}
		})
	}, nil
}
func (m *MainPrint) applyNativeIDEOnboarding(id string, result tui.IDEPickerResult) (bool, error) {
	pending := m.ideOnboarding
	if pending == nil || pending.id != id {
		return false, nil
	}
	if result.Presented {
		return true, m.Global.Save(func(global *protocol.JSONObject) (*protocol.JSONObject, error) {
			next := global.Clone()
			shown := nativeInProcessObject(next.Get("hasIdeOnboardingBeenShown"))
			if !shown.IsObject() {
				shown = protocol.NewJSONObject()
			}
			shown.Set(pending.currentName, rawMain(true))
			next.Set("hasIdeOnboardingBeenShown", rawMain(shown))
			return &next, nil
		})
	}
	if m.ideUI == nil {
		return true, errors.New("IDE onboarding state was retired")
	}
	m.ideUI.pending = nil
	m.ideOnboarding = nil
	return true, nil
}
