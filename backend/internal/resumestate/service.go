package resumestate

import (
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/filehistorycopy"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionresume"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

// Files performs real filesystem work. A host can wrap it to enforce its
// capability boundary without replacing the state restoration business.
type Files interface {
	Open(string) (sessionstore.File, error)
	ReadFile(string) ([]byte, error)
	WriteFile(string, []byte) error
	MkdirAll(string) error
	Link(string, string) error
	CopyFile(string, string) error
	ReadDir(string) ([]fs.DirEntry, error)
}
type Stat struct {
	Modified, Birth float64
	Size            int64
}
type Options struct {
	ConfigDir, CWD, OriginalCWD, SessionProjectDir, CurrentSessionID, EnvironmentKind string
	NonInteractive                                                                    bool
	Environment                                                                       map[string]string
	Arguments                                                                         []string
	Files                                                                             Files
	Stat                                                                              func(string) (Stat, error)
	Loader                                                                            *sessionhistory.Loader
	Reducer                                                                           *sessionresume.Reducer
	ParseDate                                                                         sessionprojection.DateParser
	Now                                                                               func() int64
	UUID                                                                              func() string
	OnDiagnostic                                                                      func(sessionresume.Diagnostic)
	OnError                                                                           func(error)
	OnPhase                                                                           func(string)
	// Policy paths are explicit real CLI policy sources, not the cc-haha Server
	// managed-settings service. Nil means the host has not wired policy discovery.
	// OnSnapshotRecord connects the actual completed background snapshot to the host Project writer.
	// Nil retains component-only observation and performs no transcript materialization.
	OnSnapshotRecord func(protocol.JSONObject) error
	PolicyPaths      []string
	PublicSID        bool
	// NativeProjectPaths shares the original long-path hash with the native transcript writer.
	// Finite component hosts retain their explicit unsupported-path boundary.
	NativeProjectPaths bool
	// NativeSkillBindings proves that the same runtime registry, invocation
	// authority and listing consumer are bound to this resumed shared session.
	NativeSkillBindings func() bool
	StartupHooks        *queryhooks.Service
	SettingsState       *clisettings.State
}
type State struct {
	Skills                   []protocol.JSONObject
	PlanSlugs                []protocol.JSONObject
	SuppressNextSkillListing bool
	PendingEntries           []json.RawMessage
	SessionUUIDs             map[string][]json.RawMessage
}
type Service struct {
	ownedFileHistory *filehistorycopy.Service
	owned            *ownedResume
	options          Options
	mu               sync.Mutex
	state            State
	plansDirectory   string
	background       sync.WaitGroup
	planMu           sync.Mutex
	uuidTranscripts  map[string]*sessionhistory.Transcript
}

func newBoundService(options Options) (*Service, error) {
	if options.ConfigDir == "" || options.CWD == "" || options.OriginalCWD == "" || options.CurrentSessionID == "" || options.Files == nil || options.Stat == nil || options.Loader == nil || options.Reducer == nil || options.ParseDate == nil || options.Now == nil || options.UUID == nil || options.OnDiagnostic == nil || options.OnError == nil || options.OnPhase == nil || options.Environment == nil || options.PolicyPaths == nil || options.SettingsState == nil {
		return nil, errors.New("resume state requires real host paths, settings/policy discovery, files, loader/reducer, clock/UUID and diagnostics")
	}
	if !options.NonInteractive {
		return nil, &CapabilityError{Capability: "interactive global-config/host restore"}
	}
	if !options.NativeProjectPaths && len(sanitizePath(options.OriginalCWD)) > 200 {
		return nil, &CapabilityError{Capability: "Bun long project-path hash"}
	}
	service := &Service{options: options, uuidTranscripts: map[string]*sessionhistory.Transcript{}, state: State{Skills: []protocol.JSONObject{}, PlanSlugs: []protocol.JSONObject{}, PendingEntries: []json.RawMessage{}, SessionUUIDs: map[string][]json.RawMessage{}}}
	return service, nil
}
func New(options Options) (*Service, error) {
	service, err := newBoundService(options)
	if err != nil {
		return nil, err
	}
	merged, policy, err := service.settings()
	if err != nil {
		return nil, err
	}
	if err := service.validateResolvedProfile(merged, policy); err != nil {
		return nil, err
	}
	options.SettingsState.SetSessionSettingsCache(&clisettings.ParsedSettings{Settings: &merged, Errors: []json.RawMessage{}})
	options.SettingsState.SetCachedSettingsForSource("policySettings", &policy)
	return service, nil
}

type Input struct {
	// Nil Source selects --continue. A JSON string selects a SID. An object is
	// the original caller's LogOption. JSONLPath has Source's same precedence.
	Original  *OriginalLog
	Source    json.RawMessage
	JSONLPath string
}
type Result struct {
	sessionresume.Result
	Metadata       protocol.JSONObject
	ownedSnapshots *filehistorycopy.SnapshotArray
}

// MetadataSnapshot reads the live supported array at this actual consumer
// boundary. It does not change the copy's captured array or create a new cache.
func (result *Result) MetadataSnapshot() protocol.JSONObject {
	metadata := result.Metadata.Clone()
	if result.ownedSnapshots != nil {
		metadata.Set("fileHistorySnapshots", encode(result.ownedSnapshots.Items))
	}
	return metadata
}
func (result *Result) MarshalJSON() ([]byte, error) {
	if result == nil {
		return []byte("null"), nil
	}
	fields := protocol.NewJSONObject(protocol.JSONField{Name: "messages", Value: encode(result.Messages)}, protocol.JSONField{Name: "turnInterruptionState", Value: encode(result.TurnInterruptionState)})
	for _, name := range []string{"fileHistorySnapshots", "attributionSnapshots", "contentReplacements", "contextCollapseCommits", "contextCollapseSnapshot", "sessionId", "agentName", "agentColor", "agentSetting", "customTitle", "tag", "mode", "worktreeSession", "prNumber", "prUrl", "prRepository", "fullPath"} {
		if name == "fileHistorySnapshots" && result.ownedSnapshots != nil {
			fields.Set(name, encode(result.ownedSnapshots.Items))
		} else {
			fields.Set(name, result.Metadata.Get(name))
		}
	}
	return fields.MarshalJSON()
}
func (service *Service) diagnostic(kind, name string, fields json.RawMessage) {
	service.options.OnDiagnostic(sessionresume.Diagnostic{Kind: kind, Name: name, Fields: fields})
}
func (service *Service) State() State {
	service.mu.Lock()
	defer service.mu.Unlock()
	copy := State{Skills: []protocol.JSONObject{}, PlanSlugs: []protocol.JSONObject{}, SuppressNextSkillListing: service.state.SuppressNextSkillListing, PendingEntries: []json.RawMessage{}, SessionUUIDs: map[string][]json.RawMessage{}}
	for _, raw := range service.state.PendingEntries {
		copy.PendingEntries = append(copy.PendingEntries, append(json.RawMessage(nil), raw...))
	}
	for _, item := range service.state.Skills {
		copy.Skills = append(copy.Skills, item.Clone())
	}
	for _, item := range service.state.PlanSlugs {
		copy.PlanSlugs = append(copy.PlanSlugs, item.Clone())
	}
	for key, items := range service.state.SessionUUIDs {
		copy.SessionUUIDs[key] = []json.RawMessage{}
		for _, raw := range items {
			copy.SessionUUIDs[key] = append(copy.SessionUUIDs[key], append(json.RawMessage(nil), raw...))
		}
	}
	return copy
}
func (service *Service) AwaitBackground() { service.background.Wait() }
func (service *Service) Load(input Input) (result *Result, err error) {
	if service.owned != nil {
		return nil, &CapabilityError{Capability: "Owner public load requires BeginLoad producer"}
	}
	defer func() {
		if err != nil {
			service.options.OnError(err)
		}
	}()
	merged, policy, err := service.settings()
	if err != nil {
		return nil, err
	}
	if err = service.validateResolvedProfile(merged, policy); err != nil {
		return nil, err
	}
	var log protocol.JSONObject
	var rawMessages []json.RawMessage
	var sid json.RawMessage
	if len(input.Source) == 0 {
		log, err = service.continueLog()
	} else if input.JSONLPath != "" {
		var loaded sessionhistory.ResumeResult
		loaded, err = service.options.Loader.LoadMessagesFromJSONLPath(input.JSONLPath)
		rawMessages, sid = loaded.Messages, loaded.SessionID
	} else if _, ok := units(input.Source); ok {
		sid = input.Source
		if !uuidName.MatchString(text(sid)) {
			return nil, &CapabilityError{Capability: "SID selector outside original ASCII UUID profile"}
		}
		log, err = service.lastLog(text(sid))
	} else {
		log = object(input.Source)
		if !log.IsObject() {
			return nil, &CapabilityError{Capability: "non-object LogOption"}
		}
	}
	if err != nil {
		return nil, err
	}
	if !log.IsObject() && rawMessages == nil {
		return nil, nil
	}
	if log.IsObject() {
		log, err = service.fullLog(log)
		if err != nil {
			return nil, err
		}
		rawMessages, _ = array(log.Get("messages"))
		if rawMessages == nil {
			return nil, &CapabilityError{Capability: "LogOption.messages must be an array"}
		}
		if !truthy(sid) {
			sid = log.Get("sessionId")
			if !truthy(sid) {
				sid = nil
				if len(rawMessages) > 0 {
					sid = object(rawMessages[0]).Get("sessionId")
				}
			}
		}
		if truthy(sid) {
			service.options.OnPhase("plan:start")
			if _, err = service.copyPlan(log, sid); err != nil {
				return nil, err
			}
			service.options.OnPhase("plan:end")
		}
		if service.ownedFileHistory != nil && log.Get("fileHistorySnapshots") != nil && envTruthy(service.options.Environment["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"]) && !envTruthy(service.options.Environment["CLAUDE_CODE_DISABLE_FILE_CHECKPOINTING"]) {
			return nil, &CapabilityError{Capability: "owned public resume plan/SessionStart producer not implemented"}
		}
		// Source starts copying before consistency/filter/skills/reducer/hooks,
		// but does not join the copy. AwaitBackground is a separate host boundary.
		service.options.OnPhase("history:start")
		service.startFileHistory(log)
		if err = service.checkConsistency(rawMessages); err != nil {
			return nil, err
		}
	}
	history, err := sessionresume.ParseMessages(rawMessages)
	if err != nil {
		return nil, err
	}
	history, err = service.options.Reducer.FilterMalformedAttachments(history)
	if err != nil {
		return nil, err
	}
	if err = service.restoreSkills(history); err != nil {
		return nil, err
	}
	reduced, err := service.options.Reducer.Deserialize(history)
	if err != nil {
		return nil, err
	}
	service.options.OnPhase("hooks:start")
	if err = service.sessionStart(sid); err != nil {
		return nil, err
	}
	service.options.OnPhase("hooks:end")
	metadata := log.Clone()
	metadata.Set("sessionId", sid)
	return &Result{Result: reduced, Metadata: metadata}, nil
}
func (service *Service) projectDir() string {
	if service.options.SessionProjectDir != "" {
		return service.options.SessionProjectDir
	}
	return filepath.Join(service.options.ConfigDir, "projects", service.sanitizeProjectPath(service.options.OriginalCWD))
}
