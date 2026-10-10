package filehistorycopy

import (
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io/fs"
	"path/filepath"
)

type copyJob struct {
	service           *Service
	future            *ownerasync.OwnerFuture[struct{}]
	snapshots         *SnapshotArray
	previous          json.RawMessage
	sid, directory    string
	remaining, failed int
}

func (job *copyJob) finish(scope *cooperative.Scope) error {
	if job.failed > 0 {
		job.service.options.Event("tengu_file_history_resume_copy_failed", protocol.NewJSONObject(protocol.JSONField{Name: "numSnapshots", Value: json.RawMessage(number(len(job.snapshots.Items)))}, protocol.JSONField{Name: "failedSnapshots", Value: json.RawMessage(number(job.failed))}))
	}
	return job.future.Resolve(scope, struct{}{})
}
func number(value int) string { v, _ := json.Marshal(value); return string(v) }
func (job *copyJob) start(scope *cooperative.Scope) error {
	files, path := job.service.options.Files, job.directory
	mkdir, e := job.service.dispatch(scope, func() error { return files.MkdirAll(path) })
	if e != nil {
		return e
	}
	_, e = mkdir.Then(scope, func(next *cooperative.Scope, _ struct{}, failure error) error {
		if failure != nil {
			job.service.options.OnError(failure)
			return job.future.Resolve(next, struct{}{})
		}

		// Source maps async snapshot functions synchronously, awaits their outer
		// allSettled, then runs the copy function's own continuation. Each return is
		// a concrete function/Promise boundary, never a preset microtask loop.
		items := job.snapshots.Items
		job.remaining = len(items)
		settled, _ := ownerasync.NewOwnerFuture[struct{}](job.service.options.Owner)
		if len(items) == 0 {
			_ = settled.Resolve(next, struct{}{})
		}
		for _, snapshot := range items {
			if snapshot == nil || !snapshot.IsObject() {
				return job.future.Reject(next, &CapabilityError{"snapshot object after mkdir"})
			}
			returned, e := job.snapshot(next, snapshot)
			if e != nil {
				return job.future.Reject(next, e)
			}
			_, e = returned.Then(next, func(scope *cooperative.Scope, _ struct{}, failure error) error {
				if failure != nil {
					return job.future.Reject(scope, failure)
				}
				job.remaining--
				if job.remaining == 0 {
					return settled.Resolve(scope, struct{}{})
				}
				return nil
			})
			if e != nil {
				return job.future.Reject(next, e)
			}
		}
		_, e := settled.Then(next, func(scope *cooperative.Scope, _ struct{}, failure error) error {
			if failure != nil {
				return job.future.Reject(scope, failure)
			}
			return job.finish(scope)
		})
		return e
	})
	return e
}
func (job *copyJob) snapshot(scope *cooperative.Scope, snapshot *protocol.JSONObject) (*ownerasync.OwnerFuture[struct{}], error) {
	tracked := protocol.JSONObject{}
	if e := json.Unmarshal(snapshot.Get("trackedFileBackups"), &tracked); e != nil || !tracked.IsObject() {
		return nil, &CapabilityError{"tracked Object.values original object"}
	}
	var completed protocol.JSONObject
	if raw := snapshot.Get("completedFileBackups"); len(raw) > 0 && string(raw) != "null" {
		if e := json.Unmarshal(raw, &completed); e != nil || !completed.IsObject() {
			return nil, &CapabilityError{"completed Object.values original object"}
		}
	}
	values := append(tracked.OwnProperties(), completed.OwnProperties()...)
	backups := []json.RawMessage{}
	for _, field := range values {
		var backup protocol.JSONObject
		if e := json.Unmarshal(field.Value, &backup); e != nil || !backup.IsObject() {
			return nil, &CapabilityError{"backup original object"}
		}
		name := backup.Get("backupFileName")
		if string(name) != "null" {
			backups = append(backups, name)
		}
	}
	returned, _ := ownerasync.NewOwnerFuture[struct{}](job.service.options.Owner)
	aggregate, _ := ownerasync.NewOwnerFuture[struct{}](job.service.options.Owner)
	remaining := len(backups)
	failed := false
	if remaining == 0 {
		_ = aggregate.Resolve(scope, struct{}{})
	}
	for _, name := range backups {
		item, e := job.backup(scope, name)
		if e != nil {
			return nil, e
		}
		_, e = item.Then(scope, func(next *cooperative.Scope, _ struct{}, failure error) error {
			remaining--
			failed = failed || failure != nil
			if remaining == 0 {
				return aggregate.Resolve(next, struct{}{})
			}
			return nil
		})
		if e != nil {
			return nil, e
		}
	}
	_, e := aggregate.Then(scope, func(next *cooperative.Scope, _ struct{}, failure error) error {
		if failure != nil {
			return returned.Reject(next, failure)
		}
		if failed {
			job.failed++
		} else {
			record, e := job.service.options.Sink.BeginFileHistorySnapshot(next, snapshot.Get("messageId"), snapshot, false)
			if e != nil {
				job.service.options.OnError(errors.New("FileHistory: Failed to record copy backup snapshot"))
			} else {
				_, e = record.Then(next, func(_ *cooperative.Scope, _ struct{}, failure error) error {
					if failure != nil {
						job.service.options.OnError(errors.New("FileHistory: Failed to record copy backup snapshot"))
					}
					return nil
				})
				if e != nil {
					return e
				}
			}
		}
		// Resolve the actual async snapshot return; the outer allSettled collector
		// and copy's post-await continuation run from their own Promise reactions.
		return returned.Resolve(next, struct{}{})
	})
	return returned, e
}
func (job *copyJob) backup(scope *cooperative.Scope, name json.RawMessage) (*ownerasync.OwnerFuture[struct{}], error) {
	result, _ := ownerasync.NewOwnerFuture[struct{}](job.service.options.Owner)
	part, e := pathString(name, "backup file name")
	if e == nil {
		_, e = pathString(job.previous, "session ID")
	}
	if e != nil {
		var cap *CapabilityError
		if errors.As(e, &cap) {
			return nil, e
		}
		_ = result.Reject(scope, e)
		return result, nil
	}
	sid, _ := pathString(job.previous, "session ID")
	old, new := filepath.Join(job.service.options.ConfigHome(), "file-history", sid, part), filepath.Join(job.directory, part)
	files := job.service.options.Files
	link, e := job.service.dispatch(scope, func() error { return files.Link(old, new) })
	if e != nil {
		return nil, e
	}
	_, e = link.Then(scope, func(next *cooperative.Scope, _ struct{}, failure error) error {
		if errors.Is(failure, fs.ErrExist) {
			return result.Resolve(next, struct{}{})
		}
		if errors.Is(failure, fs.ErrNotExist) {
			job.service.options.OnError(errors.New("FileHistory: Failed to copy backup " + part + " on restore (backup file does not exist in " + sid + ")"))
			return result.Reject(next, failure)
		}
		success := func(next *cooperative.Scope) error {
			job.service.options.Debug("FileHistory: Copied backup " + part + " from session " + sid + " to " + job.sid)
			return result.Resolve(next, struct{}{})
		}
		if failure == nil {
			return success(next)
		}
		job.service.options.OnError(errors.New("FileHistory: Error hard linking backup file from previous session"))
		copy, e := job.service.dispatch(next, func() error { return files.CopyFile(old, new) })
		if e != nil {
			return e
		}
		_, e = copy.Then(next, func(next *cooperative.Scope, _ struct{}, failure error) error {
			if failure != nil {
				job.service.options.OnError(errors.New("FileHistory: Error copying over backup from previous session"))
				return result.Reject(next, failure)
			}
			return success(next)
		})
		return e
	})
	return result, e
}
