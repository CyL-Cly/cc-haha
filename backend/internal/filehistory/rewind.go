package filehistory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/checkpointfiles"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io/fs"
	"path/filepath"
)

type RestorePreview struct {
	Available bool
	Files     []string
}

type restoreFiles interface {
	Files
	AssertWritable(context.Context, string, checkpointfiles.FileState, checkpointfiles.FileState) error
	WriteState(context.Context, string, checkpointfiles.FileState) error
}

func (s *Service) PreviewRestore(scope *cooperative.Scope, id string) (RestorePreview, error) {
	enabled, e := s.enabled(scope)
	if e != nil || !enabled {
		return RestorePreview{}, e
	}
	state, e := s.o.GetState(scope)
	if e != nil {
		return RestorePreview{}, e
	}
	for _, snapshot := range objects(state.Get("snapshots")) {
		if snapshot.String("messageId") == id {
			return RestorePreview{Available: true}, nil
		}
	}
	return RestorePreview{}, nil
}

func (s *Service) ChangedFiles(ctx context.Context, scope *cooperative.Scope, id string) ([]string, error) {
	preview, e := s.PreviewRestore(scope, id)
	if e != nil || !preview.Available {
		return nil, e
	}
	state, e := s.o.GetState(scope)
	if e != nil {
		return nil, e
	}
	sid, e := s.o.SessionID(scope)
	if e != nil {
		return nil, e
	}
	snapshots := objects(state.Get("snapshots"))
	var target protocol.JSONObject
	for _, snapshot := range snapshots {
		if snapshot.String("messageId") == id {
			target = snapshot
		}
	}
	tracked := stringsValue(state.Get("trackedFiles"))
	pending, e := native(s, scope, func() ([]string, error) {
		changed := []string{}
		for _, tracking := range tracked {
			if ctx.Err() != nil {
				return nil, context.Cause(ctx)
			}
			name := restoreBackupName(snapshots, target, tracking)
			if name == nil {
				continue
			}
			current, e := s.o.Files.ReadCurrentState(ctx, s.expand(tracking))
			if e != nil {
				return nil, e
			}
			target := checkpointfiles.FileState{}
			if string(name) != "null" {
				var backup string
				_ = json.Unmarshal(name, &backup)
				target, e = s.o.Files.ReadBackupState(ctx, sid, backup)
				if e != nil {
					return nil, e
				}
			}
			if current.Exists != target.Exists || !bytes.Equal(current.Content, target.Content) {
				changed = append(changed, tracking)
			}
		}
		return changed, nil
	})
	if e != nil {
		return nil, e
	}
	return pending.Await(scope)
}

func restoreBackupName(snapshots []protocol.JSONObject, target protocol.JSONObject, tracking string) json.RawMessage {
	name := obj(obj(target.Get("trackedFileBackups")).Get(tracking)).Get("backupFileName")
	if name == nil {
		for _, snapshot := range snapshots {
			candidate := obj(obj(snapshot.Get("trackedFileBackups")).Get(tracking))
			if string(candidate.Get("version")) == "1" {
				return candidate.Get("backupFileName")
			}
		}
	}
	return name
}

// Rewind restores the same tracked files from the live canonical checkpoint
// store. NativeFiles carries the existing symlink/hardlink and rollback checks.
func (s *Service) Rewind(ctx context.Context, scope *cooperative.Scope, id string) error {
	preview, e := s.PreviewRestore(scope, id)
	if e != nil {
		return e
	}
	if !preview.Available {
		return errors.New("The selected snapshot was not found")
	}
	state, e := s.o.GetState(scope)
	if e != nil {
		return e
	}
	sid, e := s.o.SessionID(scope)
	if e != nil {
		return e
	}
	snapshots := objects(state.Get("snapshots"))
	target := obj(nil)
	for _, snapshot := range snapshots {
		if snapshot.String("messageId") == id {
			target = snapshot
		}
	}
	var tracked []string
	_ = json.Unmarshal(state.Get("trackedFiles"), &tracked)
	files, ok := s.o.Files.(restoreFiles)
	if !ok {
		return errors.New("file checkpoint restore requires existing native writable files")
	}
	pending, e := native(s, scope, func() ([]string, error) {
		missing := []string{}
		type change struct {
			path          string
			before, after checkpointfiles.FileState
		}
		changes := []change{}
		for _, tracking := range tracked {
			if ctx.Err() != nil {
				return nil, context.Cause(ctx)
			}
			name := restoreBackupName(snapshots, target, tracking)
			if name == nil {
				continue
			}
			path := tracking
			if !filepath.IsAbs(path) {
				path = filepath.Join(s.o.OriginalCWD, path)
			}
			before, e := files.ReadCurrentState(ctx, path)
			if e != nil {
				return nil, e
			}
			after := checkpointfiles.FileState{}
			if string(name) != "null" {
				var backup string
				_ = json.Unmarshal(name, &backup)
				after, e = files.ReadBackupState(ctx, sid, backup)
				if errors.Is(e, fs.ErrNotExist) {
					missing = append(missing, filepath.Join(s.o.ConfigHome, "file-history", sid, backup))
					continue
				}
				if e != nil {
					return nil, e
				}
			}
			if before.Exists == after.Exists && bytes.Equal(before.Content, after.Content) {
				continue
			}
			if e = files.AssertWritable(ctx, path, before, after); e != nil {
				return nil, e
			}
			changes = append(changes, change{path, before, after})
		}
		for i, c := range changes {
			if e := files.WriteState(ctx, c.path, c.after); e != nil {
				// WriteState can fail after truncating/writing part of the
				// current file, so that file participates in rollback too.
				for j := i; j >= 0; j-- {
					_ = files.WriteState(context.WithoutCancel(ctx), changes[j].path, changes[j].before)
				}
				return nil, e
			}
		}
		return missing, nil
	})
	if e != nil {
		return e
	}
	missing, e := pending.Await(scope)
	if e == nil {
		for _, path := range missing {
			s.o.OnError(fmt.Errorf("FileHistory: [Rewind] Backup file not found: %s", path))
		}
	}
	return e
}

func (s *Service) Enabled(scope *cooperative.Scope) (bool, error) { return s.enabled(scope) }
