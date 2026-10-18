// Package filehistorycopy ports the original async backup-copy producer. It
// borrows the process's existing Owner, native ledger and real Project sink.
package filehistorycopy

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/filehistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"math"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

type CapabilityError struct{ Boundary string }

func (e *CapabilityError) Error() string { return "file-history copy capability: " + e.Boundary }

// SnapshotArray is the original array object, not a pre-rendered history. Begin
// captures this pointer before mkdir. Replacing Log.Snapshots cannot retarget it;
// owner mutations to its Items and original snapshot records remain observable.
type SnapshotArray struct{ Items []*protocol.JSONObject }
type Log struct {
	Messages  []*protocol.JSONObject
	Snapshots *SnapshotArray
}

func DecodeLog(record protocol.JSONObject) (*Log, error) {
	var messages []json.RawMessage
	if err := json.Unmarshal(record.Get("messages"), &messages); err != nil {
		return nil, &CapabilityError{"original messages array"}
	}
	log := &Log{}
	for _, raw := range messages {
		var item protocol.JSONObject
		if e := json.Unmarshal(raw, &item); e != nil || !item.IsObject() {
			return nil, &CapabilityError{"original message object"}
		}
		log.Messages = append(log.Messages, &item)
	}
	if raw := record.Get("fileHistorySnapshots"); raw != nil && string(raw) != "null" {
		var snapshots []json.RawMessage
		if e := json.Unmarshal(raw, &snapshots); e != nil {
			return nil, &CapabilityError{"original snapshot array"}
		}
		log.Snapshots = &SnapshotArray{}
		for _, raw := range snapshots {
			var item protocol.JSONObject
			if e := json.Unmarshal(raw, &item); e != nil || !item.IsObject() {
				return nil, &CapabilityError{"original snapshot object"}
			}
			log.Snapshots.Items = append(log.Snapshots.Items, &item)
		}
	}
	return log, nil
}

// Sink is the existing Project, not a Source-resolved entry or transcript. New
// checks the supplied binding's owner and identity. The real Controller supplies
// its existing Writer factory; this interface does not certify arbitrary caller
// provenance, and matching same-owner test doubles remain admissible.
type Sink interface {
	NativeCopyBinding() *ownerasync.NativeBinding
	BeginFileHistorySnapshot(*cooperative.Scope, json.RawMessage, *protocol.JSONObject, bool) (*ownerasync.OwnerFuture[struct{}], error)
}
type Files interface {
	MkdirAll(string) error
	Link(string, string) error
	CopyFile(string, string) error
}
type Options struct {
	Owner          *cooperative.Owner
	Binding        *ownerasync.NativeBinding
	Sink           Sink
	Files          Files
	Environment    func(string) string
	NonInteractive func() bool
	ConfigHome     func() string
	SessionID      func() string
	OnError        func(error)
	Debug          func(string)
	Event          func(string, protocol.JSONObject)
}
type Service struct{ options Options }

func New(options Options) (*Service, error) {
	if options.Owner == nil || options.Binding == nil || !options.Binding.Owns(options.Owner) || options.Sink == nil || !options.Binding.Same(options.Sink.NativeCopyBinding()) {
		return nil, cooperative.ErrRestricted
	}
	if options.Files == nil || options.Environment == nil || options.NonInteractive == nil || options.ConfigHome == nil || options.SessionID == nil || options.OnError == nil || options.Debug == nil || options.Event == nil {
		return nil, errors.New("file history copy requires original host getters, native files and diagnostics")
	}
	return &Service{options}, nil
}
func truthy(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" || s == "false" {
		return false
	}
	if s == "true" || strings.HasPrefix(s, "[") || strings.HasPrefix(s, "{") {
		return true
	}
	if strings.HasPrefix(s, `"`) {
		units, e := protocol.DecodeJavaScriptJSONString(raw)
		return e == nil && len(units) > 0
	}
	n, e := strconv.ParseFloat(s, 64)
	return (e == nil || math.IsInf(n, 0)) && n != 0 && !math.IsNaN(n)
}

func envTruthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
func quoted(value string) json.RawMessage { v, _ := json.Marshal(value); return v }
func pathString(raw json.RawMessage, label string) (string, error) {
	units, e := protocol.DecodeJavaScriptJSONString(raw)
	if e != nil {
		return "", &CapabilityError{"non-string " + label}
	}
	value := string(utf16.Decode(units))
	for i, c := range units {
		if c >= 0xD800 && c <= 0xDBFF {
			if i+1 >= len(units) || units[i+1] < 0xDC00 || units[i+1] > 0xDFFF {
				return "", &CapabilityError{"native path lone UTF-16 " + label}
			}
		}
		if c >= 0xDC00 && c <= 0xDFFF && (i == 0 || units[i-1] < 0xD800 || units[i-1] > 0xDBFF) {
			return "", &CapabilityError{"native path lone UTF-16 " + label}
		}
	}
	if len(units) == 0 || value == "." || value == ".." || strings.ContainsAny(value, "/\\") {
		return "", fmt.Errorf("FileHistory: Refusing unsafe %s: %s", label, value)
	}
	if !utf8.ValidString(value) {
		return "", &CapabilityError{"native path UTF-8 " + label}
	}
	return value, nil
}
func (s *Service) Begin(scope *cooperative.Scope, log *Log) (*ownerasync.OwnerFuture[struct{}], error) {
	return s.begin(scope, log, nil)
}

// BeginInteractive borrows the actual native checkpoint service. Its live
// preference replaces only the headless environment opt-in in this caller.
// The copy, captured old SID, native jobs and snapshot object identities are
// the same producer used by Begin.
func (s *Service) BeginInteractive(scope *cooperative.Scope, log *Log, checkpoints *filehistory.Service) (*ownerasync.OwnerFuture[struct{}], error) {
	if s == nil || checkpoints == nil || !checkpoints.Owns(s.options.Owner) {
		return nil, cooperative.ErrRestricted
	}
	return s.begin(scope, log, checkpoints.Enabled)
}

func (s *Service) begin(scope *cooperative.Scope, log *Log, interactive func(*cooperative.Scope) (bool, error)) (*ownerasync.OwnerFuture[struct{}], error) {
	if s == nil || !scope.BelongsTo(s.options.Owner) {
		return nil, cooperative.ErrRestricted
	}
	future, e := ownerasync.NewOwnerFuture[struct{}](s.options.Owner)
	if e != nil {
		return nil, e
	}
	e = scope.Inline(cooperative.Read, func(next *cooperative.Scope) error {
		if log == nil {
			return &CapabilityError{"original log"}
		}
		if interactive == nil && !s.options.NonInteractive() {
			return &CapabilityError{"interactive global checkpoint setting"}
		}
		enabled := envTruthy(s.options.Environment("CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING")) && !envTruthy(s.options.Environment("CLAUDE_CODE_DISABLE_FILE_CHECKPOINTING"))
		if interactive != nil {
			var e error
			enabled, e = interactive(next)
			if e != nil {
				return e
			}
		}
		if !enabled || log.Snapshots == nil || len(log.Messages) == 0 {
			return future.Resolve(next, struct{}{})
		}
		previous := log.Messages[len(log.Messages)-1].Get("sessionId")
		if !truthy(previous) {
			s.options.OnError(errors.New("FileHistory: Failed to copy backups on restore (no previous session id)"))
			return future.Resolve(next, struct{}{})
		}
		sid := s.options.SessionID()
		previousUnits, previousError := protocol.DecodeJavaScriptJSONString(previous)
		if previousError == nil && slices.Equal(previousUnits, utf16.Encode([]rune(sid))) {
			s.options.Debug("FileHistory: No need to copy file history for resuming with same session id: " + sid)
			return future.Resolve(next, struct{}{})
		}
		if _, e := pathString(quoted(sid), "session ID"); e != nil {
			return e
		}
		job := &copyJob{service: s, future: future, snapshots: log.Snapshots, previous: previous, sid: sid, directory: filepath.Join(s.options.ConfigHome(), "file-history", sid)}
		return job.start(next)
	})
	if e != nil {
		return nil, e
	}
	return future, nil
}
