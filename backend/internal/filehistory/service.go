// Package filehistory owns the original checkpoint state transitions. The
// runtime supplies its existing AppState and pending Writer; there is no second
// transcript, history cache or native-job ledger.
package filehistory

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/checkpointfiles"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Sink interface {
	NativeCopyBinding() *ownerasync.NativeBinding
	BeginFileHistorySnapshot(*cooperative.Scope, json.RawMessage, *protocol.JSONObject, bool) (*ownerasync.OwnerFuture[struct{}], error)
}
type Files interface {
	CaptureBackup(context.Context, string, string, string, int, string, func() (string, error)) (checkpointfiles.CapturedBackup, error)
	ReadCurrentState(context.Context, string) (checkpointfiles.FileState, error)
	ReadBackupState(context.Context, string, string) (checkpointfiles.FileState, error)
}
type Options struct {
	Owner       *cooperative.Owner
	Binding     *ownerasync.NativeBinding
	Sink        Sink
	Files       Files
	ConfigHome  string
	OriginalCWD string
	SessionID   func(*cooperative.Scope) (string, error)
	Enabled     func(*cooperative.Scope) (bool, error)
	GetState    func(*cooperative.Scope) (protocol.JSONObject, error)
	SetState    func(*cooperative.Scope, protocol.JSONObject) error
	Now         func() time.Time
	UUID        func() (string, error)
	OnError     func(error)
}
type Service struct{ o Options }

func New(o Options) (*Service, error) {
	if o.Owner == nil || o.Files == nil || !o.Binding.Owns(o.Owner) || o.Sink == nil || !o.Binding.Same(o.Sink.NativeCopyBinding()) || !filepath.IsAbs(o.ConfigHome) || !filepath.IsAbs(o.OriginalCWD) || o.SessionID == nil || o.Enabled == nil || o.GetState == nil || o.SetState == nil || o.Now == nil || o.UUID == nil || o.OnError == nil {
		return nil, cooperative.ErrRestricted
	}
	return &Service{o}, nil
}
func (s *Service) Owns(owner *cooperative.Owner) bool { return s != nil && s.o.Owner == owner }
func (s *Service) enabled(scope *cooperative.Scope) (bool, error) {
	if s == nil || !scope.BelongsTo(s.o.Owner) {
		return false, cooperative.ErrRestricted
	}
	return s.o.Enabled(scope)
}
func quote(v string) json.RawMessage { raw, _ := json.Marshal(v); return raw }
func encoded(v any) json.RawMessage  { raw, _ := json.Marshal(v); return raw }
func obj(raw json.RawMessage) protocol.JSONObject {
	var v protocol.JSONObject
	_ = json.Unmarshal(raw, &v)
	return v
}
func objects(raw json.RawMessage) []protocol.JSONObject {
	var v []protocol.JSONObject
	_ = json.Unmarshal(raw, &v)
	return v
}
func stringsValue(raw json.RawMessage) []string {
	v := []string{}
	_ = json.Unmarshal(raw, &v)
	return v
}
func intValue(raw json.RawMessage) int { var v int; _ = json.Unmarshal(raw, &v); return v }
func null(raw json.RawMessage) bool    { return len(raw) == 0 || string(raw) == "null" }
func truthy(raw json.RawMessage) bool {
	return !null(raw) && string(raw) != "false" && string(raw) != "0" && string(raw) != `""`
}
func keys(v protocol.JSONObject) []string {
	result := []string{}
	for _, field := range v.OwnProperties() {
		var name string
		_ = json.Unmarshal(field.NameJSON, &name)
		result = append(result, name)
	}
	return result
}
func (s *Service) shorten(path string) string {
	if !filepath.IsAbs(path) {
		return path
	}
	rel, e := filepath.Rel(s.o.OriginalCWD, path)
	if e == nil && (rel == "." || !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)) {
		if rel == "." {
			return ""
		}
		return rel
	}
	return path
}
func (s *Service) expand(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(s.o.OriginalCWD, path)
}
func (s *Service) timeJSON() json.RawMessage {
	return quote(s.o.Now().UTC().Format("2006-01-02T15:04:05.000Z"))
}
func (s *Service) backup(result checkpointfiles.CapturedBackup, version int) protocol.JSONObject {
	raw := json.RawMessage("null")
	if result.Name != nil {
		raw = quote(*result.Name)
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "backupFileName", Value: raw}, protocol.JSONField{Name: "version", Value: encoded(version)}, protocol.JSONField{Name: "backupTime", Value: s.timeJSON()})
}

// record keeps the original fire-and-forget versus awaited distinction. The
// Sink starts the original outer tracked write before returning its future.
func (s *Service) record(scope *cooperative.Scope, id json.RawMessage, snapshot *protocol.JSONObject, update, await bool) error {
	future, e := s.o.Sink.BeginFileHistorySnapshot(scope, id, snapshot, update)
	if e != nil {
		return e
	}
	if await {
		_, e = future.Await(scope)
		return e
	}
	_, e = future.Then(scope, func(_ *cooperative.Scope, _ struct{}, failure error) error {
		if failure != nil {
			s.o.OnError(errors.New("FileHistory: Failed to record snapshot: " + failure.Error()))
		}
		return nil
	})
	return e
}

func native[T any](s *Service, scope *cooperative.Scope, work func() (T, error)) (*ownerasync.OwnerFuture[T], error) {
	if !scope.BelongsTo(s.o.Owner) {
		return nil, cooperative.ErrRestricted
	}
	ref, e := s.o.Binding.Acquire(scope)
	if e != nil {
		return nil, e
	}
	out, e := ownerasync.NewOwnerFuture[T](s.o.Owner)
	if e != nil {
		ref.DeliveryFinished()
		return nil, e
	}
	go func() {
		value, failure := work()
		receipt, e := s.o.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(next *cooperative.Scope) error {
			var e error
			if failure != nil {
				e = out.Reject(next, failure)
			} else {
				e = out.Resolve(next, value)
			}
			if e != nil {
				_ = out.RejectAdministrative(e)
			}
			release, e := s.o.Owner.Microtask(cooperative.Read, func(*cooperative.Scope) error { ref.DeliveryFinished(); return nil })
			if e != nil {
				ref.DeliveryFinished()
			} else {
				ref.ReclaimRejectedReceipt(release)
			}
			return nil
		})
		if e == nil {
			e = receipt.Wait()
		}
		if e != nil {
			_ = out.RejectAdministrative(e)
			ref.DeliveryFinished()
		}
	}()
	return out, nil
}

// Track captures before the tool enters its no-await read/validate/write
// section. Repeated edits must not overwrite the original deterministic v1.
func (s *Service) Track(scope *cooperative.Scope, path string, parentMessageID json.RawMessage) error {
	enabled, e := s.enabled(scope)
	if e != nil || !enabled {
		return e
	}
	state, e := s.o.GetState(scope)
	if e != nil {
		return e
	}
	snapshots := objects(state.Get("snapshots"))
	if len(snapshots) == 0 {
		s.o.OnError(errors.New("FileHistory: Missing most recent snapshot"))
		return nil
	}
	tracking := s.shorten(path)
	if truthy(obj(snapshots[len(snapshots)-1].Get("trackedFileBackups")).Get(tracking)) {
		return nil
	}
	sid, e := s.o.SessionID(scope)
	if e != nil {
		return e
	}
	future, e := native(s, scope, func() (checkpointfiles.CapturedBackup, error) {
		return s.o.Files.CaptureBackup(context.Background(), sid, s.o.OriginalCWD, path, 1, "", s.o.UUID)
	})
	if e != nil {
		return e
	}
	result, e := future.Await(scope)
	if e != nil {
		s.o.OnError(e)
		return nil
	}
	backup := s.backup(result, 1)
	state, e = s.o.GetState(scope)
	if e != nil {
		return e
	}
	snapshots = objects(state.Get("snapshots"))
	if len(snapshots) == 0 {
		return nil
	}
	last := snapshots[len(snapshots)-1].Clone()
	backups := obj(last.Get("trackedFileBackups"))
	if truthy(backups.Get(tracking)) {
		return nil
	}
	backups.Set(tracking, encoded(backup))
	last.Set("trackedFileBackups", encoded(backups))
	snapshots[len(snapshots)-1] = last
	tracked := stringsValue(state.Get("trackedFiles"))
	found := false
	for _, v := range tracked {
		found = found || v == tracking
	}
	if !found {
		tracked = append(tracked, tracking)
	}
	state.Set("snapshots", encoded(snapshots))
	state.Set("trackedFiles", encoded(tracked))
	if e = s.o.SetState(scope, state); e != nil {
		return e
	}
	return s.record(scope, parentMessageID, &last, true, false)
}

type backupDecision struct {
	Capture checkpointfiles.CapturedBackup
	Reuse   *protocol.JSONObject
	Version int
}

func (s *Service) beforeDecision(sid, path string, previous protocol.JSONObject) (backupDecision, error) {
	version := 1
	if previous.IsObject() {
		version = intValue(previous.Get("version")) + 1
	}
	info, e := os.Lstat(path)
	if os.IsNotExist(e) {
		return backupDecision{Version: version}, nil
	}
	if e != nil {
		return backupDecision{}, e
	}
	// ReadCurrentState enforces the same no-follow/regular/nlink boundary.
	current, e := s.o.Files.ReadCurrentState(context.Background(), path)
	if e != nil {
		return backupDecision{}, e
	}
	if previous.IsObject() && !null(previous.Get("backupFileName")) {
		name := previous.String("backupFileName")
		backup, e := s.o.Files.ReadBackupState(context.Background(), sid, name)
		if e == nil && backup.Exists && backup.Mode == current.Mode && len(backup.Content) == len(current.Content) {
			backupInfo, e := os.Stat(filepath.Join(s.o.ConfigHome, "file-history", sid, name))
			if e == nil && (info.ModTime().Before(backupInfo.ModTime()) || string(protocol.DecodeJavaScriptUTF8(backup.Content)) == string(protocol.DecodeJavaScriptUTF8(current.Content))) {
				v := previous.Clone()
				return backupDecision{Reuse: &v, Version: version}, nil
			}
		}
	}
	capture, e := s.o.Files.CaptureBackup(context.Background(), sid, s.o.OriginalCWD, path, version, "", s.o.UUID)
	return backupDecision{Capture: capture, Version: version}, e
}

// Make retains Source's parallel physical backups and fresh-state inheritance
// after they settle. Only this transition increments the monotonic sequence.
func (s *Service) Make(scope *cooperative.Scope, messageID json.RawMessage) error {
	enabled, e := s.enabled(scope)
	if e != nil || !enabled {
		return e
	}
	state, e := s.o.GetState(scope)
	if e != nil {
		return e
	}
	captured := objects(state.Get("snapshots"))
	backups := protocol.NewJSONObject()
	sid, e := s.o.SessionID(scope)
	if e != nil {
		return e
	}
	if len(captured) > 0 {
		last := obj(captured[len(captured)-1].Get("trackedFileBackups"))
		tracked := stringsValue(state.Get("trackedFiles"))
		settled, _ := ownerasync.NewOwnerFuture[struct{}](s.o.Owner)
		left := len(tracked)
		if left == 0 {
			if e = settled.Resolve(scope, struct{}{}); e != nil {
				return e
			}
		}
		for _, path := range tracked {
			path := path
			prior := obj(last.Get(path))
			future, e := native(s, scope, func() (backupDecision, error) { return s.beforeDecision(sid, s.expand(path), prior) })
			if e != nil {
				return e
			}
			_, e = future.Then(scope, func(next *cooperative.Scope, result backupDecision, failure error) error {
				if failure != nil {
					s.o.OnError(failure)
				} else if result.Reuse != nil {
					backups.Set(path, encoded(*result.Reuse))
				} else {
					backups.Set(path, encoded(s.backup(result.Capture, result.Version)))
				}
				left--
				if left == 0 {
					return settled.Resolve(next, struct{}{})
				}
				return nil
			})
			if e != nil {
				return e
			}
		}
		if _, e = settled.Await(scope); e != nil {
			return e
		}
	}
	state, e = s.o.GetState(scope)
	if e != nil {
		return e
	}
	snapshots := objects(state.Get("snapshots"))
	if len(snapshots) > 0 {
		last := obj(snapshots[len(snapshots)-1].Get("trackedFileBackups"))
		for _, path := range stringsValue(state.Get("trackedFiles")) {
			if backups.Get(path) == nil && truthy(last.Get(path)) {
				backups.Set(path, last.Get(path))
			}
		}
	}
	snapshot := protocol.NewJSONObject(protocol.JSONField{Name: "schemaVersion", Value: json.RawMessage("2")}, protocol.JSONField{Name: "messageId", Value: messageID}, protocol.JSONField{Name: "trackedFileBackups", Value: encoded(backups)}, protocol.JSONField{Name: "timestamp", Value: s.timeJSON()})
	snapshots = append(snapshots, snapshot)
	if len(snapshots) > 100 {
		snapshots = snapshots[len(snapshots)-100:]
	}
	state.Set("snapshots", encoded(snapshots))
	state.Set("snapshotSequence", json.RawMessage(strconv.Itoa(intValue(state.Get("snapshotSequence"))+1)))
	if e = s.o.SetState(scope, state); e != nil {
		return e
	}
	return s.record(scope, messageID, &snapshot, false, false)
}

// Complete captures immutable after files once, including on query cancellation
// and errors. Failed paths are omitted; they must never be called unchanged.
func (s *Service) Complete(scope *cooperative.Scope, messageID json.RawMessage) error {
	enabled, e := s.enabled(scope)
	if e != nil || !enabled {
		return e
	}
	state, e := s.o.GetState(scope)
	if e != nil {
		return e
	}
	snapshots := objects(state.Get("snapshots"))
	var captured *protocol.JSONObject
	for _, snapshot := range snapshots {
		if string(snapshot.Get("messageId")) == string(messageID) {
			v := snapshot
			captured = &v
			break
		}
	}
	if captured == nil || truthy(captured.Get("completedFileBackups")) {
		return nil
	}
	completed := protocol.NewJSONObject()
	sid, e := s.o.SessionID(scope)
	if e != nil {
		return e
	}
	before := obj(captured.Get("trackedFileBackups"))
	for _, path := range keys(before) {
		backup := obj(before.Get(path))
		version := intValue(backup.Get("version"))
		unique, e := s.o.UUID()
		if e != nil {
			return e
		}
		suffix := "completed-" + captured.String("messageId") + "-" + unique
		future, e := native(s, scope, func() (checkpointfiles.CapturedBackup, error) {
			return s.o.Files.CaptureBackup(context.Background(), sid, s.o.OriginalCWD, s.expand(path), version, suffix, s.o.UUID)
		})
		if e != nil {
			return e
		}
		result, e := future.Await(scope)
		if e != nil {
			s.o.OnError(e)
			continue
		}
		completed.Set(path, encoded(s.backup(result, version)))
	}
	state, e = s.o.GetState(scope)
	if e != nil {
		return e
	}
	snapshots = objects(state.Get("snapshots"))
	var updated *protocol.JSONObject
	for i, snapshot := range snapshots {
		if string(snapshot.Get("messageId")) != string(messageID) || truthy(snapshot.Get("completedFileBackups")) {
			continue
		}
		v := snapshot.Clone()
		if intValue(v.Get("schemaVersion")) < 2 {
			v.Set("schemaVersion", json.RawMessage("2"))
		}
		v.Set("completedFileBackups", encoded(completed))
		snapshots[i] = v
		updated = &v
	}
	state.Set("snapshots", encoded(snapshots))
	if e = s.o.SetState(scope, state); e != nil {
		return e
	}
	if updated != nil {
		return s.record(scope, messageID, updated, true, true)
	}
	return nil
}
