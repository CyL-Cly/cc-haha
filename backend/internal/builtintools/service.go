// Package builtintools supplies native tool implementations to the existing
// query loop. It owns no model loop, transcript writer or session controller.
package builtintools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/analytics"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/nativepaths"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/powershellpolicy"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/windowshell"
)

type Options struct {
	ShellHints                     *ShellHintBindings
	Shells                         *windowshell.Resolver
	WindowsPowerShellSandboxPolicy func(*cooperative.Scope) error
	PermissionRealpath             func(string) (string, error)
	RipgrepConfig                  func() (string, []string, error)
	BashSandbox                    *BashSandboxBindings
	ReportEvent                    func(*cooperative.Scope, analytics.Event) error
	Images                         *ImageBindings
	BashCwd                        *BashCwdBindings
	SessionEnvironmentScript       func(context.Context, *cooperative.Scope) (*string, error)
	SessionEnvironmentCapture      func(*cooperative.Scope) (func() (*string, error), error)
	HookPathSafety                 func(*cooperative.Scope) (HookPathSafetyContext, error)
	HookInternalEditable           func(*cooperative.Scope, string) (bool, error)
	InternalReadable               func(*cooperative.Scope, string) (bool, string, error)
	HookPermissionRoot             func(*cooperative.Scope, string) (string, error)
	FileHistoryTrack               func(*cooperative.Scope, string, json.RawMessage) error
	ForegroundShellProgress        bool
	ToolResultsDirectory           func(*cooperative.Scope) (string, error)
	Owner                          *cooperative.Owner
	NativeBinding                  *ownerasync.NativeBinding
	Cache                          *builtinwrite.FileStateCache
	Write                          *builtinwrite.Service
	Home, CWD, Platform            string
	NowMillis                      func() float64
	Environment                    map[string]string
	ProcessEnvironment             func(*cooperative.Scope) (map[string]string, error)
	PermissionContext              func(*cooperative.Scope) (protocol.JSONObject, error)
	PermissionRoots                map[string]string
	// Large text reads use the caller's actual token counter. Small reads retain
	// Source's rough-estimate fast path and do not call a model.
	CountTokensOnOwner    func(context.Context, *cooperative.Scope, string) (*float64, error)
	CountTokens           func(context.Context, string) (*int, error)
	Random                func() float64
	RunAgent              func(context.Context, *cooperative.Scope, AgentRequest) (*ownerasync.OwnerFuture[AgentRunResult], error)
	RunBackgroundAgent    func(context.Context, *cooperative.Scope, AgentRequest) (*ownerasync.OwnerFuture[AgentRunResult], error)
	AgentDefinitions      func(*cooperative.Scope) ([]agentcatalog.Definition, error)
	AgentPrompt           func(*cooperative.Scope, agentcatalog.Definition) (string, error)
	AgentPromptJSON       func(*cooperative.Scope, agentcatalog.Definition) (json.RawMessage, error)
	AgentAdditionalTools  func(*cooperative.Scope) ([]queryloop.RegisteredTool, error)
	GetTodos              func(*cooperative.Scope) (json.RawMessage, error)
	SetTodos              func(*cooperative.Scope, json.RawMessage) error
	NonInteractive        bool
	QuestionPreviewFormat string
	TaskStore             *taskstore.Service
	TaskListID            func(*cooperative.Scope, string) (string, error)
	ExpandTaskView        func(*cooperative.Scope) error
	TaskHook              func(context.Context, *cooperative.Scope, string, protocol.JSONObject) error
	TaskHookWithContext   func(context.Context, *cooperative.Scope, string, protocol.JSONObject, queryloop.JSONToolCallContext) error
	PlanMode              *PlanModeBindings
	WebFetch              *WebFetchBindings
	WebSearch             *WebSearchBindings
	FileSkills            func(context.Context, *cooperative.Scope, string) (*ownerasync.OwnerFuture[struct{}], error)
	Skill                 *SkillBindings
	Worktree              *WorktreeBindings
	MCPResources          *MCPResourceBindings
	ToolSearch            *ToolSearchBindings
	BackgroundTasks       *BackgroundTaskBindings
	SessionCollaboration  *SessionCollaborationBindings
	Workflow              *WorkflowBindings
	ResumeAgent           func(context.Context, *cooperative.Scope, string, string) (string, error)
	SendMessageEnabled    func(*cooperative.Scope) (bool, error)
	TeamMessage           func(context.Context, *cooperative.Scope, protocol.JSONObject) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], error)
	Team                  *TeamBindings
}

type AgentRequest struct {
	Name                                                string
	ResumeAgentID                                       string
	ResumeHistory                                       []protocol.JSONObject
	ResumedWorktreePath                                 string
	OwnerAgentID                                        string
	PersistedAgentType                                  string
	PersistedDescription                                json.RawMessage
	Prompt, Description, AgentType, Model, SystemPrompt string
	ParentModel                                         string
	SystemPromptSections                                []json.RawMessage
	PermissionContext                                   protocol.JSONObject
	SelectedAgent                                       protocol.JSONObject
	Tools                                               []string
	ParentTools                                         []string
	ToolUseID                                           json.RawMessage
	DefinitionModel, Source                             string
	Effort                                              json.RawMessage
	MaxTurns                                            *float64
	OmitClaudeMd                                        bool
	ParentPermissionMode, PermissionModeOverride        string
	Isolation                                           string
	CanReadOutputFile                                   bool
}
type AgentRunResult struct {
	Async                                                  bool
	OutputFile                                             string
	AgentID                                                string
	Messages                                               []queryloop.Message
	DurationMillis                                         float64
	WorktreePath, WorktreeBranch, WorktreeIsolationSkipped string
}

type Service struct {
	powerShellParser     *powershellpolicy.Parser
	powerShellClassifier *powershellpolicy.Classifier
	shellTool            string
	options              Options
	agentSnapshot        []agentcatalog.Definition
}
type CapabilityError struct{ Boundary string }

func (e *CapabilityError) Error() string {
	return "native tools capability is not migrated: " + e.Boundary
}
func capability(v string) error { return &CapabilityError{v} }

func New(o Options) (*Service, error) {
	if o.Owner == nil || o.NativeBinding == nil || !o.NativeBinding.Owns(o.Owner) || o.Cache == nil || !o.Cache.Owns(o.Owner) || o.Write == nil || !o.Write.Owns(o.Owner) || !filepath.IsAbs(o.Home) || !filepath.IsAbs(o.CWD) || o.NowMillis == nil || o.PermissionContext == nil {
		return nil, capability("shared owner, writer binding, file-state cache and original paths")
	}
	if o.Platform != "darwin" && o.Platform != "linux" && o.Platform != "windows" {
		return nil, capability("Windows file, process and shell semantics")
	}
	environment := make(map[string]string, len(o.Environment))
	for key, value := range o.Environment {
		environment[key] = value
	}
	o.Environment = environment
	if o.QuestionPreviewFormat == "" {
		if preview := environment["CLAUDE_CODE_QUESTION_PREVIEW_FORMAT"]; preview == "markdown" || preview == "html" {
			o.QuestionPreviewFormat = preview
		} else {
			entry := environment["CLAUDE_CODE_ENTRYPOINT"]
			if !strings.HasPrefix(entry, "sdk-") && entry != "claude-desktop" && entry != "local-agent" && entry != "remote" {
				o.QuestionPreviewFormat = "markdown"
			}
		}
	}
	roots := make(map[string]string, len(o.PermissionRoots))
	for key, value := range o.PermissionRoots {
		roots[key] = value
	}
	o.PermissionRoots = roots
	return &Service{options: o}, nil
}
func (s *Service) Owns(o *cooperative.Owner) bool { return s != nil && s.options.Owner == o }
func (s *Service) check(scope *cooperative.Scope) error {
	if s == nil || !scope.BelongsTo(s.options.Owner) {
		return cooperative.ErrRestricted
	}
	return scope.Inline(cooperative.Read, func(*cooperative.Scope) error { return nil })
}
func scopeFor(v any) *cooperative.Scope {
	if c, ok := v.(*builtinwrite.CallContext); ok && c != nil {
		return c.Scope
	}
	return nil
}
func quote(v string) json.RawMessage { return protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(v))) }
func raw(v any) json.RawMessage      { b, _ := json.Marshal(v); return b }
func object(fields ...protocol.JSONField) json.RawMessage {
	return raw(protocol.NewJSONObject(fields...))
}
func field(k string, v any) protocol.JSONField { return protocol.JSONField{Name: k, Value: raw(v)} }
func text(v json.RawMessage) (string, error) {
	u, e := protocol.DecodeJavaScriptJSONString(v)
	if e != nil {
		return "", e
	}
	for i, c := range u {
		if c >= 0xd800 && c <= 0xdbff && (i+1 == len(u) || u[i+1] < 0xdc00 || u[i+1] > 0xdfff) || c >= 0xdc00 && c <= 0xdfff && (i == 0 || u[i-1] < 0xd800 || u[i-1] > 0xdbff) {
			return "", capability("unpaired UTF-16 native input")
		}
	}
	return string(utf16.Decode(u)), nil
}
func (s *Service) expand(v json.RawMessage) (string, error) {
	p, e := text(v)
	if e != nil {
		return "", e
	}
	if strings.ContainsRune(p, 0) {
		return "", errors.New("Path contains null bytes")
	}
	p = strings.TrimSpace(p)
	if s.options.Platform == "windows" {
		return nativepaths.ExpandWindows(p, s.options.CWD, s.options.Home)
	}
	if p == "~" {
		p = s.options.Home
	} else if strings.HasPrefix(p, "~/") {
		p = filepath.Join(s.options.Home, p[2:])
	} else if !filepath.IsAbs(p) {
		p = filepath.Join(s.options.CWD, p)
	}
	if strings.HasPrefix(p, `\\`) || strings.HasPrefix(p, "//") {
		return "", capability("UNC network paths")
	}
	return filepath.Clean(p), nil
}
func (s *Service) path(in protocol.JSONObject, key string) (string, error) {
	v := in.Get(key)
	if v == nil {
		return s.options.CWD, nil
	}
	return s.expand(v)
}
func number(in protocol.JSONObject, k string, d int) int {
	var v float64
	if in.Get(k) == nil || json.Unmarshal(in.Get(k), &v) != nil {
		return d
	}
	return int(v)
}
func boolean(in protocol.JSONObject, k string, d bool) bool {
	var v bool
	if in.Get(k) == nil || json.Unmarshal(in.Get(k), &v) != nil {
		return d
	}
	return v
}
func mtime(p string) (float64, error) {
	st, e := os.Stat(p)
	if e != nil {
		return 0, e
	}
	return float64(st.ModTime().UnixMilli()), nil
}

// Native work never receives Scope or mutable session state. Its completion
// and cache mutations settle on the caller's one cooperative owner.
func native[T any](s *Service, scope *cooperative.Scope, work func() (T, error)) (*ownerasync.OwnerFuture[T], error) {
	if e := s.check(scope); e != nil {
		return nil, e
	}
	ref, e := s.options.NativeBinding.Acquire(scope)
	if e != nil {
		return nil, e
	}
	future, e := ownerasync.NewOwnerFuture[T](s.options.Owner)
	if e != nil {
		ref.DeliveryFinished()
		return nil, e
	}
	go func() {
		v, failure := work()
		receipt, e := s.options.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(next *cooperative.Scope) error {
			var e error
			if failure != nil {
				e = future.Reject(next, failure)
			} else {
				e = future.Resolve(next, v)
			}
			if e != nil {
				_ = future.RejectAdministrative(e)
			}
			r, e := s.options.Owner.Microtask(cooperative.Read, func(*cooperative.Scope) error { ref.DeliveryFinished(); return nil })
			if e != nil {
				ref.DeliveryFinished()
			} else {
				ref.ReclaimRejectedReceipt(r)
			}
			return nil
		})
		if e == nil {
			e = receipt.Wait()
		}
		if e != nil {
			_ = future.RejectAdministrative(e)
			ref.DeliveryFinished()
		}
	}()
	return future, nil
}

func transform[A, B any](s *Service, scope *cooperative.Scope, input *ownerasync.OwnerFuture[A], finish func(*cooperative.Scope, A) (B, error)) (*ownerasync.OwnerFuture[B], error) {
	output, e := ownerasync.NewOwnerFuture[B](s.options.Owner)
	if e != nil {
		return nil, e
	}
	_, e = input.Then(scope, func(callback *cooperative.Scope, value A, failure error) error {
		if failure != nil {
			return output.Reject(callback, failure)
		}
		result, e := finish(callback, value)
		if e != nil {
			return output.Reject(callback, e)
		}
		return output.Resolve(callback, result)
	})
	if e != nil {
		_ = output.RejectAdministrative(e)
		return nil, e
	}
	return output, nil
}

func (s *Service) SetWorkingDirectory(scope *cooperative.Scope, cwd string) error {
	if e := s.check(scope); e != nil {
		return e
	}
	if !filepath.IsAbs(cwd) {
		return capability("actual working directory")
	}
	if e := s.options.Write.SetWorkingDirectory(scope, cwd); e != nil {
		return e
	}
	s.options.CWD = cwd
	return nil
}

// HasInternalReadableBindings requires the same owner-bound path producer used by native Read.
func (s *Service) HasInternalReadableBindings() bool {
	return s != nil && s.options.InternalReadable != nil
}
