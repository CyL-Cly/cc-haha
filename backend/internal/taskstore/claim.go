package taskstore

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"path/filepath"
)

type ClaimOptions struct{ CheckAgentBusy, MarkInProgress bool }

// Claim uses the same lifecycle/list/task leases as the existing tool writes.
// Claiming is a lower-level operation than TaskUpdate: it has no model hook,
// metadata merge or reciprocal edge mutation.
func (s *Service) Claim(list, id, claimant string, options ClaimOptions) (result protocol.JSONObject, err error) {
	err = s.withLifecycle(list, true, func(lifecycle) error {
		before, failure := s.Get(list, quote(id))
		if failure != nil || before == nil {
			result = claimFailure("task_not_found", nil)
			return nil
		}
		var err error
		result, err = s.claimWhileLifecycle(list, id, claimant, options)
		return err
	})
	return result, err
}
func claimFailure(reason string, task *protocol.JSONObject) protocol.JSONObject {
	result := protocol.NewJSONObject(protocol.JSONField{Name: "success", Value: raw(false)}, protocol.JSONField{Name: "reason", Value: quote(reason)})
	if task != nil {
		result.Set("task", raw(*task))
	}
	return result
}
func (s *Service) claimWhileLifecycle(list, id, claimant string, options ClaimOptions) (result protocol.JSONObject, returned error) {
	// Source creates the lock target before entering its catch boundary.
	target := filepath.Join(s.tasksDir(list), ".lock")
	if err := ensureLockTarget(target); err != nil {
		return result, err
	}
	var releaseList, releaseTask func() error
	defer func() {
		if releaseTask != nil {
			if err := releaseTask(); err != nil {
				returned = err
				return
			}
		}
		if releaseList != nil {
			if err := releaseList(); err != nil {
				returned = err
			}
		}
	}()
	failure := func() error {
		var err error
		releaseList, err = s.lock(target)
		if err != nil {
			return err
		}
		if !options.CheckAgentBusy {
			path, err := s.taskPath(list, quote(id))
			if err != nil {
				return err
			}
			releaseTask, err = s.lock(path)
			if err != nil {
				return err
			}
		}
		all, err := s.List(list)
		if err != nil {
			return err
		}
		var task *protocol.JSONObject
		if options.CheckAgentBusy {
			for _, row := range all {
				if row.String("id") == id {
					value := row.Clone()
					task = &value
					break
				}
			}
		} else {
			task, err = s.Get(list, quote(id))
			if err != nil {
				return err
			}
		}
		if task == nil {
			result = claimFailure("task_not_found", nil)
			return nil
		}
		if desktopTruthy(task.Get("owner")) && task.String("owner") != claimant {
			result = claimFailure("already_claimed", task)
			return nil
		}
		if task.String("status") == "completed" {
			result = claimFailure("already_resolved", task)
			return nil
		}
		unresolved := map[string]bool{}
		for _, row := range all {
			if row.String("status") != "completed" {
				unresolved[string(row.Get("id"))] = true
			}
		}
		blocked := []json.RawMessage{}
		for _, value := range stringsFromJSON(task.Get("blockedBy")) {
			if unresolved[string(value)] {
				blocked = append(blocked, value)
			}
		}
		if len(blocked) > 0 {
			result = claimFailure("blocked", task)
			result.Set("blockedByTasks", raw(blocked))
			return nil
		}
		if options.CheckAgentBusy {
			busy := []json.RawMessage{}
			for _, row := range all {
				if row.String("status") != "completed" && row.String("owner") == claimant && row.String("id") != id {
					busy = append(busy, row.Get("id"))
				}
			}
			if len(busy) > 0 {
				result = claimFailure("agent_busy", task)
				result.Set("busyWithTasks", raw(busy))
				return nil
			}
		}
		if task.String("owner") != claimant || options.MarkInProgress && task.String("status") != "in_progress" {
			if _, err = s.bumpRevision(list); err != nil {
				return err
			}
			patch := protocol.NewJSONObject(protocol.JSONField{Name: "owner", Value: quote(claimant)})
			if options.MarkInProgress {
				patch.Set("status", quote("in_progress"))
			}
			if options.CheckAgentBusy {
				if err = s.updateRelated(list, quote(id), patch); err != nil {
					return err
				}
				task, err = s.Get(list, quote(id))
			} else {
				task, err = s.updateUnsafe(list, quote(id), patch)
			}
			if err != nil {
				return err
			}
		}
		if options.CheckAgentBusy && task == nil {
			result = claimFailure("task_not_found", nil)
			return nil
		}
		result = protocol.NewJSONObject(protocol.JSONField{Name: "success", Value: raw(true)}, protocol.JSONField{Name: "task"})
		if task != nil {
			result.Set("task", raw(*task))
		}
		return nil
	}()
	if failure != nil {
		result = claimFailure("task_not_found", nil)
	}
	return result, nil
}
