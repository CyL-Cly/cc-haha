// Package sessionrewind derives turn checkpoints from the shared server
// transcript and recorded file-history snapshots. It never runs an Agent.
package sessionrewind

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
)

type Evidence struct {
	Messages []protocol.JSONObject
	Complete bool
}
type SnapshotEvidence struct {
	Rows     []json.RawMessage
	Complete bool
}

var ErrSnapshotEvidenceIncomplete = errors.New("recorded file-history evidence is incomplete")

type Options struct {
	MessagesWithEvidence func(context.Context, string) (Evidence, error)
	Snapshots            func(context.Context, string) (SnapshotEvidence, error)
	WorkDir              func(context.Context, string) (string, error)
	// Native callbacks retain original paths and byte states. ReadCurrent must
	// distinguish an absent file from an unreadable or unsafe file via error.
	ReadBackup                func(context.Context, string, string) ([]byte, error)
	ReadCurrent               func(context.Context, string) ([]byte, bool, error)
	SafeTrackedPath           func(context.Context, string, string) bool
	CanonicalPath             func(string) string
	RecordedCommandIsReadOnly func(string) bool
	ClassifyRecordedCommand   func(string) (bool, error)
	Platform                  string
	ProcessCWD                string
	Files                     RestoreFiles
	StopSessionAndWait        func(context.Context, string) error
	TrimSessionMessagesFrom   func(context.Context, string, string) (TrimResult, error)
}

type Target struct {
	TargetUserMessageID string `json:"targetUserMessageId"`
	UserMessageIndex    int    `json:"userMessageIndex"`
	UserMessageCount    int    `json:"userMessageCount"`
}
type Conversation struct {
	MessagesRemoved int `json:"messagesRemoved"`
}
type CodePreview struct {
	Available    bool     `json:"available"`
	Reason       string   `json:"reason,omitempty"`
	FilesChanged []string `json:"filesChanged"`
	Insertions   int      `json:"insertions"`
	Deletions    int      `json:"deletions"`
	stats        map[string]changeStats
}
type TurnCheckpoint struct {
	StartedAt               *float64     `json:"startedAt,omitempty"`
	Target                  Target       `json:"target"`
	Conversation            Conversation `json:"conversation"`
	Code                    CodePreview  `json:"code"`
	WorkDir                 string       `json:"workDir"`
	RestoreAvailable        bool         `json:"restoreAvailable"`
	UnverifiedChangeSources []string     `json:"unverifiedChangeSources"`
}
type Service struct {
	options    Options
	processCWD string
}

func New(options Options) (*Service, error) {
	if options.MessagesWithEvidence == nil || options.Snapshots == nil || options.WorkDir == nil || options.ReadBackup == nil || options.ReadCurrent == nil || options.SafeTrackedPath == nil || options.CanonicalPath == nil || (options.RecordedCommandIsReadOnly == nil && options.ClassifyRecordedCommand == nil) {
		return nil, errors.New("session rewind requires shared transcript evidence, recorded snapshots, original workdir and native file/shell primitives")
	}
	cwd := options.ProcessCWD
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}
	if !filepath.IsAbs(cwd) {
		return nil, errors.New("session rewind requires an absolute native process cwd")
	}
	return &Service{options, cwd}, nil
}

func object(raw json.RawMessage) protocol.JSONObject {
	var value protocol.JSONObject
	_ = value.UnmarshalJSON(raw)
	return value
}
func text(value protocol.JSONObject, field string) string {
	var result string
	_ = json.Unmarshal(value.Get(field), &result)
	return result
}
func blocks(value protocol.JSONObject) []protocol.JSONObject {
	var raw []json.RawMessage
	if json.Unmarshal(value.Get("content"), &raw) != nil {
		return nil
	}
	result := []protocol.JSONObject{}
	for _, item := range raw {
		if value := object(item); value.IsObject() {
			result = append(result, value)
		}
	}
	return result
}
func truthy(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) || bytes.Equal(raw, []byte("false")) {
		return false
	}
	if raw[0] == '"' {
		value, err := protocol.DecodeJavaScriptJSONString(raw)
		return err == nil && len(value) > 0
	}
	if raw[0] == '-' || (raw[0] >= '0' && raw[0] <= '9') {
		value, err := strconv.ParseFloat(string(raw), 64)
		return value != 0 && (err == nil || errors.Is(err, strconv.ErrRange))
	}
	return true
}
func (s *Service) identity(path string, frozen bool) string {
	if !filepath.IsAbs(path) {
		path = filepath.Join(s.processCWD, path)
	}
	if frozen {
		path, _ = filepath.Abs(path)
	} else {
		path = s.options.CanonicalPath(path)
	}
	if s.options.Platform == "win32" || s.options.Platform == "windows" {
		path = strings.ToLower(path)
	}
	return path
}
func absolute(base, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	value, _ := filepath.Abs(filepath.Join(base, path))
	return value
}

func (s *Service) absolute(base, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	if !filepath.IsAbs(base) {
		base = filepath.Join(s.processCWD, base)
	}
	return filepath.Clean(filepath.Join(base, path))
}
func expandTracking(base, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(base, path)
}
func nullishCWD(message protocol.JSONObject, fallback string) string {
	value, ok := stringValue(message.Get("cwd"))
	if !ok {
		return fallback
	}
	return value
}

// List reads the same active chain as history once, indexes descendant Agent
// ownership, and lists completed root turns in their original order.
func (s *Service) List(ctx context.Context, sessionID string, frozen bool) ([]TurnCheckpoint, error) {
	evidence, err := s.options.MessagesWithEvidence(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	turns := buildTurns(evidence.Messages)
	result := []TurnCheckpoint{}
	if len(turns) == 0 {
		return result, nil
	}
	workDir, err := s.options.WorkDir(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	rawSnapshots, err := s.options.Snapshots(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if !rawSnapshots.Complete {
		return nil, ErrSnapshotEvidenceIncomplete
	}
	snapshots := loadSnapshots(rawSnapshots.Rows)
	if evidence.Complete {
		if err := s.scopeCarriedSnapshots(ctx, sessionID, snapshots, turns, workDir, frozen); err != nil {
			return nil, err
		}
	}
	for index, turn := range turns {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !turn.completed {
			continue
		}
		base := nullishCWD(turn.user, workDir)
		var next *snapshot
		if index+1 < len(turns) {
			next = snapshots[text(turns[index+1].user, "id")]
		}
		checkpoint, err := s.preview(ctx, sessionID, base, snapshots[text(turn.user, "id")], next, turn.messages, evidence.Complete, frozen)
		if err != nil {
			return nil, err
		}
		checkpoint.Target = Target{text(turn.user, "id"), index, len(turns)}
		checkpoint.Conversation = Conversation{len(evidence.Messages) - turn.activeIndex}
		checkpoint.WorkDir = base
		if started, valid, _ := sessionprojection.ParseDateUTC(utf16.Encode([]rune(text(turn.user, "timestamp")))); valid {
			checkpoint.StartedAt = &started
		}
		result = append(result, checkpoint)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
