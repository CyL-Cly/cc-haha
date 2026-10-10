package clibootstrap

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

var errAgentPromotedToBackground = errors.New("Agent foreground iteration promoted to independent background")

type nativeAgentBackgroundRestart struct {
	abortContext context.Context
	worktree     *nativeAgentWorktree
	prompt       *sdkhost.NativeAgentRestartPrompt
	messages     []queryloop.Message
	start        float64
}

func (m *MainPrint) nativeAgentRestartPrompt() *sdkhost.NativeAgentRestartPrompt {
	if d := m.options.DetachedAgent; d != nil {
		return d.restartPrompt
	}
	return nil
}

func (m *MainPrint) HasForegroundTasks(ctx context.Context) (bool, error) {
	var has bool
	receipt, e := m.Owner.Submit(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		var e error
		has, e = m.Controller.HasForegroundTasksOnOwner(scope)
		return e
	})
	if e == nil {
		e = receipt.Wait()
	}
	return has, e
}
func (m *MainPrint) BackgroundForegroundAgents(ctx context.Context) (bool, error) {
	var changed bool
	receipt, e := m.Owner.Submit(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if skillTruthy(m.options.Native.Environment["CLAUDE_CODE_DISABLE_BACKGROUND_TASKS"]) {
			return nil
		}
		var e error
		changed, e = m.Controller.BackgroundForegroundAgentsOnOwner(scope)
		return e
	})
	if e == nil {
		e = receipt.Wait()
	}
	return changed, e
}

// Callbacks must be nonblocking, matching the terminal's immutable-state
// channel. They receive no Scope or mutable task data.
func (m *MainPrint) SubscribeForegroundTasks(ctx context.Context, callback func(bool)) (bool, func(), error) {
	var current bool
	var remove func(*cooperative.Scope) error
	receipt, e := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		var e error
		current, remove, e = m.Controller.SubscribeForegroundTasksOnOwner(scope, callback)
		return e
	})
	if e == nil {
		e = receipt.Wait()
	}
	if e != nil {
		return false, nil, e
	}
	var once sync.Once
	return current, func() {
		once.Do(func() {
			receipt, e := m.Owner.SubmitIndependent(context.Background(), cooperative.Read, remove)
			if e == nil {
				_ = receipt.Wait()
			}
		})
	}, nil
}

func (m *MainPrint) nativeAgentAutoBackgroundDelay() time.Duration {
	if m.options.Native == nil || !skillTruthy(m.options.Native.Environment["CLAUDE_AUTO_BACKGROUND_TASKS"]) {
		return 0
	}
	return 120 * time.Second
}

func (m *MainPrint) bindNativeForegroundPromotion(scope *cooperative.Scope, id string, cancel context.CancelCauseFunc) (func(), error) {
	return m.bindNativeForegroundPromotionAction(scope, id, func(*cooperative.Scope) error { cancel(errAgentPromotedToBackground); return nil })
}

func (m *MainPrint) bindNativeForegroundPromotionAction(scope *cooperative.Scope, id string, signal func(*cooperative.Scope) error) (func(), error) {
	if !scope.BelongsTo(m.Owner) {
		return nil, cooperative.ErrRestricted
	}
	promote := func(scope *cooperative.Scope) error {
		if !scope.BelongsTo(m.Owner) {
			return cooperative.ErrRestricted
		}
		changed := false
		if e := m.nativeBackgroundBindings().Update(scope, id, func(task protocol.JSONObject) (protocol.JSONObject, error) {
			if task.String("type") != "local_agent" || string(task.Get("isBackgrounded")) == "true" {
				return task, nil
			}
			changed = true
			return sessioncontroller.MarkForegroundAgentBackground(task), nil
		}); e != nil {
			return e
		}
		if changed {
			return signal(scope)
		}
		return nil
	}
	b := m.nativeBackgroundBindings()
	entry, e := b.Lookup(scope, id)
	if e != nil {
		return nil, e
	}
	if entry == nil {
		return nil, cooperative.ErrRestricted
	}
	entry.Promote = promote
	// Rebind the actual runtime entry without resetting its public Source data.
	if e = b.Register(scope, entry); e != nil {
		return nil, e
	}
	delay := m.nativeAgentAutoBackgroundDelay()
	if delay == 0 {
		return func() {}, nil
	}
	fire := func() {
		receipt, e := m.Owner.SubmitIndependent(context.Background(), cooperative.Read, promote)
		if e == nil {
			_ = receipt.Wait()
		}
	}
	if primitive := m.options.Native.Timer; primitive != nil {
		return primitive(delay, fire), nil
	}
	timer := time.AfterFunc(delay, fire)
	return func() { timer.Stop() }, nil
}
