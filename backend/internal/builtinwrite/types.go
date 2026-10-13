// Package builtinwrite is the shared native built-in Write implementation.
// The registry, canonical parser, real file side effect and result mapper are
// independent of CLI/SDK/Server callers, which still own permissions and hooks.
package builtinwrite

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/analytics"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolsearch"
)

type CapabilityError struct{ Boundary string }

func (e *CapabilityError) Error() string {
	return "built-in Write capability is not migrated: " + e.Boundary
}
func capability(s string) error { return &CapabilityError{s} }

type ValidationError struct {
	Message   string
	ErrorCode int
}

func (e *ValidationError) Error() string { return e.Message }

type ReadHandle interface {
	Fd() uintptr
	ReadAt([]byte, int64) (int, error)
	Close() error
}
type Files interface {
	Stat(string) (fs.FileInfo, error)
	Lstat(string) (fs.FileInfo, error)
	Realpath(string) (string, error)
	Readlink(string) (string, error)
	OpenRead(string) (ReadHandle, error)
	ReadFile(string) ([]byte, error)
	Mkdir(string) error
	WriteFlush(string, []byte) error
	Chmod(string, fs.FileMode) error
	Rename(string, string) error
	Unlink(string) error
}

// Host contains actual caller-owned service state, never phases or results.
// An enabled dependency has a named refusal until its real service is wired.
type Host struct {
	Platform                                                            string
	NonInteractive                                                      bool
	TeamMemoryEnabled, DiagnosticClientConnected                        bool
	ConditionalSkillsPresent, LSPEnabled, VSCodeClientConnected         bool
	RemoteDiffEnabled, ActiveHooks, AnalyticsEnabled, LOCCounterEnabled bool
}
type Options struct {
	ReportEvent       func(*cooperative.Scope, analytics.Event) error
	FileHistoryTrack  func(*cooperative.Scope, string, json.RawMessage) error
	Owner             *cooperative.Owner
	NativeBinding     *ownerasync.NativeBinding // borrowed from the existing Writer
	Cache             *FileStateCache
	Home, CWD         string
	PID               int
	NowMillis         func() float64
	Environment       func(string) string
	PermissionContext func(*cooperative.Scope) (protocol.JSONObject, error)
	PermissionRoots   map[string]string // actual settings-source roots; absent roots refuse rooted rules
	Host              Host              // platform only; dependency state is obtained live below
	HostState         func(*cooperative.Scope) (Host, error)
	Files             Files
	FileSkills        func(context.Context, *cooperative.Scope, string) (*ownerasync.OwnerFuture[struct{}], error)
}
type Service struct {
	options           Options
	dynamicSkillDirs  map[string]bool
	unportedSkillDirs map[string]bool
	addedLines        int
	removedLines      int
}

func New(o Options) (*Service, error) {
	if o.Owner == nil || !o.NativeBinding.Owns(o.Owner) || o.Cache == nil || !o.Cache.Owns(o.Owner) || !filepath.IsAbs(o.Home) || !filepath.IsAbs(o.CWD) || o.PID <= 0 || o.NowMillis == nil || o.Environment == nil || o.PermissionContext == nil || o.HostState == nil {
		return nil, capability("unresolved shared owner/native binding/cache/paths/clocks/permission producer")
	}
	if o.Host.Platform != "darwin" && o.Host.Platform != "linux" && o.Host.Platform != "windows" {
		return nil, capability("native Windows path, permission and replacement semantics")
	}
	if o.Files == nil {
		o.Files = OSFiles{}
	}
	return &Service{options: o, dynamicSkillDirs: map[string]bool{}, unportedSkillDirs: map[string]bool{}}, nil
}
func (s *Service) Owns(o *cooperative.Owner) bool { return s != nil && s.options.Owner == o }
func (s *Service) check(scope *cooperative.Scope) error {
	if s == nil || !scope.BelongsTo(s.options.Owner) {
		return cooperative.ErrRestricted
	}
	return scope.Inline(cooperative.Read, func(*cooperative.Scope) error { return nil })
}

type CallContext struct {
	PermissionContext   func(*cooperative.Scope) (protocol.JSONObject, error)
	ToolSearchCatalogue *toolsearch.Catalogue
	Scope               *cooperative.Scope
	AgentID             string
	MainLoopModel       string
	SkillOverrides      *protocol.JSONObject
	Messages            []queryloop.Message
}

func scopeFor(v any) *cooperative.Scope {
	if c, ok := v.(*CallContext); ok && c != nil {
		return c.Scope
	}
	return nil
}
func quoted(s string) json.RawMessage           { b, _ := json.Marshal(s); return b }
func raw(o protocol.JSONObject) json.RawMessage { b, _ := o.MarshalJSON(); return b }
func scalar(raw json.RawMessage) (string, error) {
	units, e := protocol.DecodeJavaScriptJSONString(raw)
	if e != nil {
		return "", e
	}
	for i, u := range units {
		if u >= 0xd800 && u <= 0xdbff && (i+1 == len(units) || units[i+1] < 0xdc00 || units[i+1] > 0xdfff) || u >= 0xdc00 && u <= 0xdfff && (i == 0 || units[i-1] < 0xd800 || units[i-1] > 0xdbff) {
			return "", capability("unpaired UTF16 filesystem path")
		}
	}
	var value string
	err := json.Unmarshal(raw, &value)
	return value, err
}
func truth(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
func (s *Service) hostCheck(scope *cooperative.Scope) error {
	h, err := s.options.HostState(scope)
	if err != nil {
		return err
	}
	if h.AnalyticsEnabled && s.options.ReportEvent == nil {
		return capability("enabled analytics event transport and file-operation reporting")
	}
	if h.LOCCounterEnabled {
		return capability("enabled existing bootstrap LOC counter")
	}
	if !h.NonInteractive && s.options.FileHistoryTrack == nil {
		return capability("interactive file-history/global configuration")
	}
	if h.TeamMemoryEnabled {
		return capability("team-memory path and secret scanning")
	}
	if h.ActiveHooks {
		return capability("active pre/post tool hooks")
	}
	if h.DiagnosticClientConnected {
		return capability("IDE beforeFileEdited diagnostics")
	}
	if h.ConditionalSkillsPresent && s.options.FileSkills == nil {
		return capability("conditional-skill matching and activation")
	}
	if h.LSPEnabled && !truth(s.options.Environment("CLAUDE_CODE_SIMPLE")) {
		return capability("LSP change/save notifications")
	}
	if h.VSCodeClientConnected && s.options.Environment("USER_TYPE") == "ant" {
		return capability("VSCode file_updated notification")
	}
	if h.RemoteDiffEnabled && truth(s.options.Environment("CLAUDE_CODE_REMOTE")) {
		return capability("remote git-diff producer")
	}
	if s.options.FileHistoryTrack == nil && truth(s.options.Environment("CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING")) && !truth(s.options.Environment("CLAUDE_CODE_DISABLE_FILE_CHECKPOINTING")) {
		return capability("enabled actual fileHistoryTrackEdit")
	}
	return nil
}

// cancelled returns the caller's real cancellation. Physical OS work is not
// abandoned or treated as a fabricated successful completion.
func cancelled(ctx context.Context) error {
	if ctx == nil {
		return cooperative.ErrRestricted
	}
	return context.Cause(ctx)
}

var _ ReadHandle = (*os.File)(nil)
