package clibootstrap

import (
	"context"
	"encoding/json"
	"sync/atomic"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

// The caller's native reference holds its lifetime while immutable data is
// delivered to the target's existing Owner. No target state is read on the
// worker, and neither owner receives the other owner's Scope.
func nativeAgentOwnerCall[T any](caller *MainPrint, scope *cooperative.Scope, target *MainPrint, run func(*cooperative.Scope) (T, error)) (T, error) {
	var zero T
	if !scope.BelongsTo(caller.Owner) || caller.Owner == target.Owner {
		return zero, cooperative.ErrRestricted
	}
	binding, e := caller.Controller.BorrowNativeCopyBinding(caller.Owner)
	if e != nil {
		return zero, e
	}
	ref, e := binding.Acquire(scope)
	if e != nil {
		return zero, e
	}
	future, e := ownerasync.NewOwnerFuture[T](caller.Owner)
	if e != nil {
		ref.DeliveryFinished()
		return zero, e
	}
	// Admit on the caller's turn before handing the receipt to a waiter. The
	// goroutine only joins the target; it must not reorder Source Promise.all's
	// sequential starts when several calls are submitted on the same owner.
	var value T
	receipt, failure := target.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(targetScope *cooperative.Scope) error { var e error; value, e = run(targetScope); return e })
	go func() {
		if failure == nil {
			failure = receipt.Wait()
		}
		delivery, e := caller.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(callerScope *cooperative.Scope) error {
			var e error
			if failure != nil {
				e = future.Reject(callerScope, failure)
			} else {
				e = future.Resolve(callerScope, value)
			}
			if e != nil {
				_ = future.RejectAdministrative(e)
			}
			release, e := caller.Owner.Microtask(cooperative.Read, func(*cooperative.Scope) error { ref.DeliveryFinished(); return nil })
			if e != nil {
				ref.DeliveryFinished()
			} else {
				ref.ReclaimRejectedReceipt(release)
			}
			return nil
		})
		if e == nil {
			e = delivery.Wait()
		}
		if e != nil {
			_ = future.RejectAdministrative(e)
			ref.DeliveryFinished()
		}
	}()
	return future.Await(scope)
}

func bridgeAgentVoid(child, parent *MainPrint, scope *cooperative.Scope, run func(*cooperative.Scope) error) error {
	_, e := nativeAgentOwnerCall(child, scope, parent, func(s *cooperative.Scope) (struct{}, error) { return struct{}{}, run(s) })
	return e
}

func (m *MainPrint) detachedTaskBindings(child *MainPrint, directory string) *builtintools.BackgroundTaskBindings {
	root := m.nativeBackgroundBindings()
	return &builtintools.BackgroundTaskBindings{OutputDir: directory, AgentID: child.options.AgentID, CheckPath: root.CheckPath,
		RegisterName: func(scope *cooperative.Scope, name, id string) error {
			return bridgeAgentVoid(child, m, scope, func(s *cooperative.Scope) error { return root.RegisterName(s, name, id) })
		},
		ResolveName: func(scope *cooperative.Scope, name string) (string, error) {
			return nativeAgentOwnerCall(child, scope, m, func(s *cooperative.Scope) (string, error) { return root.ResolveName(s, name) })
		},
		NotifyForAgent: func(scope *cooperative.Scope, id, message string) error {
			return bridgeAgentVoid(child, m, scope, func(s *cooperative.Scope) error { return root.NotifyForAgent(s, id, message) })
		},
		Register: func(scope *cooperative.Scope, entry *sessioncontroller.BackgroundTask) error {
			copy := *entry
			copy.Data = entry.Data.Clone()
			if entry.Promote != nil {
				promote := entry.Promote
				copy.Promote = func(parentScope *cooperative.Scope) error { return bridgeAgentVoid(m, child, parentScope, promote) }
			}
			return bridgeAgentVoid(child, m, scope, func(s *cooperative.Scope) error { return root.Register(s, &copy) })
		},
		RegisterAgent: func(scope *cooperative.Scope, entry *sessioncontroller.BackgroundTask, name string) (*sessioncontroller.BackgroundTaskRegistration, error) {
			copy := *entry
			copy.Data = entry.Data.Clone()
			if entry.Promote != nil {
				promote := entry.Promote
				copy.Promote = func(parentScope *cooperative.Scope) error { return bridgeAgentVoid(m, child, parentScope, promote) }
			}
			return registerDetachedAgentTask(child, m, scope, root, &copy, name)
		},
		Lookup: func(scope *cooperative.Scope, id string) (*sessioncontroller.BackgroundTask, error) {
			return nativeAgentOwnerCall(child, scope, m, func(s *cooperative.Scope) (*sessioncontroller.BackgroundTask, error) { return root.Lookup(s, id) })
		},
		List: func(scope *cooperative.Scope) ([]protocol.JSONObject, error) {
			return nativeAgentOwnerCall(child, scope, m, root.List)
		},
		Update: func(scope *cooperative.Scope, id string, update func(protocol.JSONObject) (protocol.JSONObject, error)) error {
			return bridgeAgentVoid(child, m, scope, func(s *cooperative.Scope) error { return root.Update(s, id, update) })
		},
		MarkNotified: func(scope *cooperative.Scope, id string) error {
			return bridgeAgentVoid(child, m, scope, func(s *cooperative.Scope) error { return root.MarkNotified(s, id) })
		},
		Stop: func(scope *cooperative.Scope, id string, now float64) (protocol.JSONObject, error) {
			return nativeAgentOwnerCall(child, scope, m, func(s *cooperative.Scope) (protocol.JSONObject, error) { return root.Stop(s, id, now) })
		},
		Delete: func(scope *cooperative.Scope, id string) error {
			return bridgeAgentVoid(child, m, scope, func(s *cooperative.Scope) error { return root.Delete(s, id) })
		},
		Emit: func(scope *cooperative.Scope, event protocol.JSONObject) error {
			copy := event.Clone()
			return bridgeAgentVoid(child, m, scope, func(s *cooperative.Scope) error { return root.Emit(s, copy) })
		},
		Notify: func(scope *cooperative.Scope, id, message string) error {
			return bridgeAgentVoid(child, m, scope, func(s *cooperative.Scope) error {
				return m.Controller.EnqueueTaskNotificationOnOwner(s, child.options.AgentID, message)
			})
		},
	}
}

// Registration owns its actual target completion and native reference until the
// caller commits transfer or the target Owner finishes rollback. In particular,
// cancelling Await cannot discard a registration already made on another Owner.
func registerDetachedAgentTask(child, parent *MainPrint, scope *cooperative.Scope, bindings *builtintools.BackgroundTaskBindings, entry *sessioncontroller.BackgroundTask, name string) (*sessioncontroller.BackgroundTaskRegistration, error) {
	if !scope.BelongsTo(child.Owner) || child.Owner == parent.Owner || bindings.RegisterAgent == nil {
		return nil, cooperative.ErrRestricted
	}
	binding, e := child.Controller.BorrowNativeCopyBinding(child.Owner)
	if e != nil {
		return nil, e
	}
	ref, e := binding.Acquire(scope)
	if e != nil {
		return nil, e
	}
	future, e := ownerasync.NewOwnerFuture[*sessioncontroller.BackgroundTaskRegistration](child.Owner)
	if e != nil {
		ref.DeliveryFinished()
		return nil, e
	}
	var decision atomic.Uint32 // 0 pending, 1 committed, 2 abandoned
	chosen := make(chan uint32, 1)
	choose := func(value uint32) {
		if decision.CompareAndSwap(0, value) {
			chosen <- value
		}
	}
	done := make(chan struct{})
	var completionError error // published by close(done), read only after join
	var actual *sessioncontroller.BackgroundTaskRegistration
	receipt, failure := parent.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(targetScope *cooperative.Scope) error {
		var e error
		actual, e = bindings.RegisterAgent(targetScope, entry, name)
		return e
	})
	go func() {
		defer func() { ref.DeliveryFinished(); close(done) }()
		if failure == nil {
			failure = receipt.Wait()
		}
		delivery, e := child.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(callerScope *cooperative.Scope) error {
			if failure != nil {
				return future.Reject(callerScope, failure)
			}
			returned := &sessioncontroller.BackgroundTaskRegistration{Replaced: actual.Replaced, Commit: func() {
				if decision.CompareAndSwap(0, 1) {
					// Commit is custody-only, so forward synchronously through all
					// layers before this layer may release its native reference.
					actual.Commit()
					chosen <- 1
				}
			}, Rollback: func(scope *cooperative.Scope) error {
				if !scope.BelongsTo(child.Owner) {
					return cooperative.ErrRestricted
				}
				choose(2)
				if decision.Load() == 1 {
					return nil
				}
				// The physical rollback still runs if this observer is cancelled.
				if e := scope.AwaitPromise(done); e != nil {
					return e
				}
				return completionError
			}}
			return future.Resolve(callerScope, returned)
		})
		if e == nil {
			e = delivery.Wait()
		}
		if e != nil {
			_ = future.RejectAdministrative(e)
		}
		selected := <-chosen
		if failure != nil {
			completionError = failure
			return
		}
		if selected == 1 {
			return
		}
		rollback, e := parent.Owner.SubmitIndependent(context.Background(), cooperative.Read, actual.Rollback)
		if e == nil {
			e = rollback.Wait()
		}
		completionError = e
	}()
	registration, e := future.Await(scope)
	if e != nil {
		choose(2)
	}
	return registration, e
}

func (m *MainPrint) nativeAgentQueuedAttachments(scope *cooperative.Scope) ([]protocol.JSONObject, error) {
	if d := m.options.DetachedAgent; d != nil {
		return d.takeQueued(m, scope)
	}
	return m.Controller.TakeNativeQueuedAttachmentsOnOwner(scope, m.options.AgentID)
}

func cloneAgentRawRows(rows []json.RawMessage) []json.RawMessage {
	out := make([]json.RawMessage, len(rows))
	for i, v := range rows {
		out[i] = append(json.RawMessage(nil), v...)
	}
	return out
}
