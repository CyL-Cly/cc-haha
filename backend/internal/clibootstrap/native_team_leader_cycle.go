package clibootstrap

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
)

// RunLeaderInboxCycle is the original durable headless run continuation. The
// Session serializes it with human turns, and the same Host admits its already
// queued original prompt. It cannot create a worker or manufacture a team role.
func (m *MainPrint) RunLeaderInboxCycle(ctx context.Context) (bool, error) {
	if m.options.Interactive {
		pending := false
		receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
			if !m.initialized || m.closed || m.shuttingDown {
				return cooperative.ErrClosed
			}
			rows, err := m.Controller.LeaderPendingTeamInboxOnOwner(scope)
			if err != nil {
				return err
			}
			pending = len(rows) > 0
			if !pending {
				if err = m.Controller.FinishTeamInboxOnOwner(scope, nil); err != nil {
					return err
				}
				pending, err = m.Controller.HasQueuedTaskNotificationsOnOwner(scope)
				return err
			}
			return nil
		})
		if err != nil {
			return false, err
		}
		if err = receipt.Wait(); err != nil {
			return false, err
		}
		if !pending {
			return false, nil
		}
	}
	active := false
	err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if !m.initialized || m.closed || m.shuttingDown {
			return cooperative.ErrClosed
		}
		var err error
		if m.options.Interactive {
			active, err = m.enqueueInteractiveLeaderInbox(scope)
			if err == nil && !active {
				active, err = m.Controller.HasQueuedTaskNotificationsOnOwner(scope)
			}
		} else {
			active, err = m.nativeLeaderInboxOnOwner(ctx, scope, false)
		}
		if err != nil || !active {
			return err
		}
		queued, err := m.Controller.HasMainThreadQueuedCommandOnOwner(scope)
		if err != nil || !queued {
			return err
		}
		if m.options.Interactive && m.options.OnInteractiveTeamTurn != nil {
			if err := m.options.OnInteractiveTeamTurn(true); err != nil {
				return err
			}
			defer m.options.OnInteractiveTeamTurn(false)
		}
		if err = m.markNativePaneActive(scope); err != nil {
			return err
		}
		if err = m.nativeRunState(scope, "running"); err != nil {
			return err
		}
		_, err = m.Host.RunDequeued(scope, ctx)
		if idleErr := m.nativeRunState(scope, "idle"); err == nil {
			err = idleErr
		}
		return err
	})
	return active, err
}

func (m *MainPrint) notifyDurableLeaderInbox(scope *cooperative.Scope) (bool, error) {
	if m.options.OnLeaderInboxReady == nil {
		return false, nil
	}
	if !scope.BelongsTo(m.Owner) {
		return false, cooperative.ErrRestricted
	}
	if m.teamState == nil || m.teamWorker != nil || m.options.DetachedAgent != nil || m.options.Interactive {
		return true, nil
	}
	tasks, err := m.Controller.BackgroundTasksOnOwner(scope)
	if err != nil {
		return true, err
	}
	if teamspawn.HasTeammatesRequiringShutdown(m.teamState.snapshot, tasks) {
		return true, m.options.OnLeaderInboxReady()
	}
	return true, nil
}
