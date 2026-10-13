package sessioncontroller

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/diagnostics"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/filehistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/filehistorycopy"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/resumestate"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioninbox"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
)

type Options struct {
	TeamContext              *TeamContext
	InProcessTeammateContext *InProcessTeammateContext
	// Bootstrap binds the existing CLI owner state as the sole live SID authority.
	Bootstrap *bootstrapstate.State
	// AsyncPersistence selects the independent owner Record path before activation.
	PublicResumeStartup    *clistartupenv.Service
	AsyncPersistence       bool
	RecordBranchProfile    string
	BeginOwnedRecordBranch *OwnedRecordBranch
	ResumeOptions          resumestate.Options
	WriterOptions          WriterOptions
	Factory                *messages.Factory
	UUID                   func() string
	Now                    func() time.Time
	NowMillis              func() float64
	Diagnostics            *diagnostics.Process
	OnPhase                func(string)
	ReplayUserMessages     bool
	// The finite profile resolves original --tools "" and a custom system prompt.
	// Full permissions/commands/default prompt and SDK controls remain refused.
	PublicPrint            bool
	NativeWorktreeResume   func(*cooperative.Scope, json.RawMessage, bool) error
	NativeInteractiveQueue bool
	NativeTools            bool
	StartupHooks           *queryhooks.Service
	BaseToolsCLI           []string
	CustomSystemPrompt     json.RawMessage
	// Resolves original string sections on this Controller, before request
	// preparation. The resolver owns its session cache and native producers.
	ResolveSystemPrompt         func(context.Context, []protocol.JSONObject) ([]json.RawMessage, error)
	ResolveSystemPromptForModel func(context.Context, []protocol.JSONObject, string) ([]json.RawMessage, error)
	UserContext                 *usercontext.Service
	// Public headless restore reads the same current-project config after the
	// adopted SID's file pointer reset. Interactive save/reset has another host.
	RestoreCosts         func(*cooperative.Scope, string) (bool, error)
	QuerySource          string
	NonInteractive       *bool
	SharedAccounting     *modelaccounting.Session
	InheritedPermissions *Controller
	AgentPermissionMode  string
	AgentOmitClaudeMd    bool
	LiveSDKSettings      func(*cooperative.Scope) (*protocol.JSONObject, protocol.JSONObject, error)
}
type Controller struct {
	commandLifecycle            func(json.RawMessage, string)
	permissionModeChanged       func(*cooperative.Scope, string, string) error
	commandLifecycleIDs         []json.RawMessage
	leaderTeamName              string
	nativeFileHistory           *filehistory.Service
	fileHistorySink             *FileHistorySink
	options                     Options
	inputProcessor              *InputProcessor
	localInputResult            *LocalInputResult
	promptInputResult           *ProcessedInput
	registeredTools             []protocol.JSONObject
	modelAccounting             *modelaccounting.Session
	resume                      *resumestate.Service
	writer                      *Writer
	messages                    []*protocol.JSONObject
	received                    map[string]bool
	receivedOrder               []string
	queue                       []command
	result                      *resumestate.Result
	sid                         string
	active                      bool
	activationPending           bool
	freshOwned                  bool
	appState                    AppState
	readCache                   *ReadCache
	queueEvents                 []protocol.JSONObject
	replays                     []protocol.JSONObject
	snapshotMu                  sync.Mutex
	snapshotRecords             []protocol.JSONObject
	resumeReturnedSnapshotCount int
	newSDKAdmitted              bool
	legacyEnvironment           *clistartupenv.Environment
	promptID                    json.RawMessage
	sessionInbox                *sessioninbox.Inbox
	inboxScope                  *cooperative.Scope
	foregroundObservers         map[uint64]*foregroundObserver
	foregroundObserverSequence  uint64
	backgroundObservers         map[uint64]func(protocol.JSONObject)
	backgroundObserverSequence  uint64
	taskNotificationPublisher   func(*cooperative.Scope, TaskNotificationCommand) (bool, error)
	taskNotificationGeneration  uint64
}
type command struct {
	Content, UUID            json.RawMessage
	Priority                 string
	Meta                     bool
	Mode, AgentID            string
	Origin                   json.RawMessage
	SkipSlash                bool
	taskNotificationIdentity *taskNotificationIdentity
}
type Activation struct {
	// ValidateLoaded is a caller-specific check before session switching/restoration.
	ValidateLoaded func(*resumestate.Result) error
	Resume         resumestate.Input
	ForkSession    bool
	ResumeAt       string
	// Original URL/CCR loadInitialMessages treats a remotely hydrated empty
	// transcript as startup, before resume restoration or SID adoption.
	RemoteEmptyStartup bool
}
type Admission struct {
	Duplicate       bool
	Historical      bool
	Acknowledgement *protocol.JSONObject
}

func New(options Options) (*Controller, error) {
	if options.RecordBranchProfile == OwnedGitRecordProfile {
		if options.Bootstrap == nil || !options.AsyncPersistence || options.BeginOwnedRecordBranch == nil || options.WriterOptions.Branch != nil {
			return nil, capability("shared-owner Git requires independent Record without synchronous fallback")
		}
		if err := options.BeginOwnedRecordBranch.checkBinding(options.Bootstrap); err != nil {
			return nil, err
		}
		// Take a private value snapshot: overwriting the caller's exported zero
		// value must not change a previously admitted Controller capability.
		binding := *options.BeginOwnedRecordBranch
		options.BeginOwnedRecordBranch = &binding
		options.WriterOptions.Branch = func() (string, error) { return "", cooperative.ErrRestricted }
	}
	if options.Bootstrap != nil {
		if options.Bootstrap.SessionID() != options.ResumeOptions.CurrentSessionID || options.Bootstrap.SessionID() != options.WriterOptions.SessionID {
			return nil, capability("bootstrap/startup SID mismatch")
		}
		if options.WriterOptions.Bootstrap != nil && options.WriterOptions.Bootstrap != options.Bootstrap {
			return nil, capability("multiple bootstrap session authorities")
		}
		options.WriterOptions.Bootstrap = options.Bootstrap
	} else if options.WriterOptions.Bootstrap != nil {
		return nil, capability("writer bootstrap without controller authority")
	}
	if options.PublicPrint && (options.PublicResumeStartup == nil || options.StartupHooks == nil || options.UserContext == nil) {
		return nil, capability("public print shared startup/hook/user-context bindings")
	}
	if options.Factory == nil || options.UUID == nil || options.Now == nil || options.OnPhase == nil {
		return nil, errors.New("headless controller requires real factory, clock/UUID and phase observer")
	}
	if !options.NativeTools && (len(options.BaseToolsCLI) != 1 || (options.BaseToolsCLI[0] != "" && (!options.PublicPrint || options.BaseToolsCLI[0] != "Write"))) {
		return nil, capability("available tools other than original --tools empty")
	}
	if _, err := protocol.DecodeJavaScriptJSONString(options.CustomSystemPrompt); err != nil && options.ResolveSystemPrompt == nil && options.ResolveSystemPromptForModel == nil {
		return nil, capability("default/non-string system prompt")
	}
	if options.ResumeOptions.Environment["USER_TYPE"] == "ant" || envTruthy(options.ResumeOptions.Environment["CLAUDE_CODE_SAVE_HOOK_ADDITIONAL_CONTEXT"]) {
		return nil, capability("internal/hook transcript policy")
	}
	var legacyEnvironment *clistartupenv.Environment
	if options.UserContext == nil {
		if options.PublicResumeStartup != nil {
			return nil, capability("public resume requires original context service binding without constructor policy reads")
		}
		var err error
		options.UserContext, legacyEnvironment, err = legacyContext(options)
		if err != nil {
			return nil, err
		}
	}
	var writer *Writer
	var controller *Controller
	options.ResumeOptions.OnSnapshotRecord = func(entry protocol.JSONObject) error {
		controller.snapshotMu.Lock()
		controller.snapshotRecords = append(controller.snapshotRecords, entry.Clone())
		controller.snapshotMu.Unlock()
		return writer.Append(&entry)
	}
	var service *resumestate.Service
	var err error
	if options.PublicResumeStartup != nil {
		service, err = resumestate.NewOwned(options.ResumeOptions, options.Bootstrap, options.PublicResumeStartup)
	} else {
		service, err = resumestate.New(options.ResumeOptions)
	}
	if err != nil {
		return nil, err
	}
	writer, err = NewWriter(options.WriterOptions)
	if err != nil {
		return nil, err
	}
	controller = &Controller{options: options, legacyEnvironment: legacyEnvironment, resume: service, writer: writer, received: map[string]bool{}, appState: emptyAppState(), readCache: newReadCache(headlessReadCacheEntries), sid: options.ResumeOptions.CurrentSessionID}
	if options.Bootstrap != nil {
		if options.AsyncPersistence {
			pending, e := NewPendingWriter(writer, options.Bootstrap.Owner())
			if e != nil {
				return nil, e
			}
			sink, e := NewFileHistorySink(pending, options.ResumeOptions.Loader)
			if e != nil {
				return nil, e
			}
			controller.fileHistorySink = sink
			copy, e := filehistorycopy.New(filehistorycopy.Options{Owner: options.Bootstrap.Owner(), Binding: pending.NativeCopyBinding(), Sink: sink, Files: options.ResumeOptions.Files, Environment: func(k string) string { return options.ResumeOptions.Environment[k] }, NonInteractive: func() bool { return options.ResumeOptions.NonInteractive }, ConfigHome: func() string { return options.ResumeOptions.ConfigDir }, SessionID: options.Bootstrap.SessionID, OnError: options.ResumeOptions.OnError, Debug: func(v string) { service.DiagnosticForCopy("debug", v, nil) }, Event: func(v string, fields protocol.JSONObject) { service.DiagnosticForCopy("event", v, raw(fields)) }})
			if e != nil {
				return nil, e
			}
			if e = service.BindOwnedFileHistory(copy); e != nil {
				return nil, e
			}
			if options.PublicResumeStartup != nil {
				e = service.BindOwnedNative(pending.NativeCopyBinding(), func(scope *cooperative.Scope, sid string, t *sessionhistory.Transcript) error {
					_, _, e := pending.PrimeSessionUUIDCache(scope, options.ResumeOptions.Loader, sid, t)
					return e
				})
				if e != nil {
					return nil, e
				}
			}
		}
		// This finite startup still begins with original zero costs. The actual
		// project-config resume/restore cost state remains an explicit missing path.
		if options.SharedAccounting != nil {
			if !options.SharedAccounting.Owns(options.Bootstrap.Owner()) {
				return nil, cooperative.ErrRestricted
			}
			controller.modelAccounting = options.SharedAccounting
		} else {
			controller.modelAccounting, err = modelaccounting.NewOwnedSession(options.Bootstrap.Owner(), nil)
		}
		if err != nil {
			return nil, err
		}
		controller.sid = ""
	}
	return controller, nil
}
func (controller *Controller) Activate(input Activation) error {
	if controller.options.Bootstrap != nil {
		return cooperative.ErrRestricted
	}
	return controller.activate(nil, input)
}

// ActivateOwned enters the real resume switch callsite on the startup Owner.
// A listener failure propagates before Writer reset/metadata restoration; the
// already switched bootstrap state is retained, as in Source.
func (controller *Controller) ActivateOwned(scope *cooperative.Scope, input Activation) error {
	if controller.options.PublicResumeStartup != nil {
		return capability("public resume requires BeginActivate producer")
	}
	if controller.options.Bootstrap == nil {
		return cooperative.ErrRestricted
	}
	if err := controller.options.Bootstrap.CheckScope(scope); err != nil {
		return err
	}
	return controller.activate(scope, input)
}
func (controller *Controller) activate(scope *cooperative.Scope, input Activation) error {
	if controller.active || controller.activationPending {
		return capability("repeated headless startup activation")
	}
	controller.options.OnPhase("resume:start")
	input.Resume.ResumeAt = input.ResumeAt
	result, err := controller.resume.Load(input.Resume)
	if err != nil {
		return err
	}
	controller.snapshotMu.Lock()
	controller.resumeReturnedSnapshotCount = len(controller.snapshotRecords)
	controller.snapshotMu.Unlock()
	controller.options.OnPhase("resume:return")
	if result == nil {
		return errors.New("No conversation found")
	}
	history := make([]*protocol.JSONObject, 0, len(result.Messages))
	for _, message := range result.Messages {
		var record protocol.JSONObject
		if err = json.Unmarshal(raw(message), &record); err != nil {
			return err
		}
		history = append(history, &record)
	}
	sid := text(result.Metadata.Get("sessionId"))
	if !input.ForkSession && sid != "" {
		if controller.options.Bootstrap == nil {
			controller.sid = sid
		}
		projectDir := ""
		if path := text(result.Metadata.Get("fullPath")); path != "" {
			projectDir = directory(path)
		}
		controller.options.OnPhase("switch")
		if controller.options.Bootstrap != nil {
			var path *string
			if text(result.Metadata.Get("fullPath")) != "" {
				path = &projectDir
			}
			if err = controller.options.Bootstrap.SwitchSession(scope, sid, path); err != nil {
				return err
			}
		}
		controller.writer.Switch(sid, projectDir)
		if !controller.options.WriterOptions.SessionPersistenceDisabled {
			controller.options.OnPhase("reset")
			if controller.options.Bootstrap != nil {
				if err = controller.writer.ResetOnOwner(scope); err != nil {
					return err
				}
			} else if err = controller.writer.Reset(); err != nil {
				return err
			}
		}
	}
	return controller.restoreActivated(result, history, input)
}
func (controller *Controller) restoreActivated(result *resumestate.Result, history []*protocol.JSONObject, input Activation) error {
	var err error
	// Prime uses the actual original Loader membership, including branch UUIDs
	// excluded from the selected history. SDK duplicate checking sees that Set.
	if uuids := controller.resume.State().SessionUUIDs[controller.SessionID()]; !controller.options.AsyncPersistence && uuids != nil {
		if err = controller.writer.Prime(uuids); err != nil {
			return err
		}
	}
	liveMetadata := result.MetadataSnapshot()
	metadata := liveMetadata.Clone()
	if input.ForkSession {
		metadata.Set("worktreeSession", nil)
	}
	controller.options.OnPhase("state:restore")
	if err = controller.restoreAppState(liveMetadata, history); err != nil {
		return err
	}
	controller.options.OnPhase("metadata:restore")
	controller.writer.RestoreMetadata(metadata)
	controller.readCache, err = extractReadCache(history, controller.options.ResumeOptions.CWD, controller.options.ResumeOptions.Environment["HOME"], controller.options.WriterOptions.Files, controller.options.ResumeOptions.ParseDate, headlessReadCacheEntries)
	if err != nil {
		return err
	}
	controller.options.OnPhase("read-cache:return")
	controller.messages = history
	controller.result = result
	controller.active = true
	interrupted := result.TurnInterruptionState
	if interrupted.Kind != "none" && interrupted.Message != nil && controller.options.ResumeOptions.Environment["CLAUDE_CODE_RESUME_INTERRUPTED_TURN"] != "" {
		var message protocol.JSONObject
		_ = json.Unmarshal(raw(interrupted.Message), &message)
		id, _ := stringKey(message.Get("uuid"))
		for index, candidate := range controller.messages {
			key, err := stringKey(candidate.Get("uuid"))
			if err == nil && key == id {
				end := index + 2
				if end > len(controller.messages) {
					end = len(controller.messages)
				}
				controller.messages = append(controller.messages[:index:index], controller.messages[end:]...)
				break
			}
		}
		return controller.enqueue(command{Content: object(message.Get("message")).Get("content"), UUID: quote(controller.options.UUID()), Priority: "next"})
	}
	return nil
}
func directory(value string) string { return filepath.Dir(value) }
func (controller *Controller) AdmitSDK(input protocol.JSONObject) (Admission, error) {
	return controller.admitSDK(nil, input)
}
func (controller *Controller) admitSDK(scope *cooperative.Scope, input protocol.JSONObject, modes ...string) (Admission, error) {
	mode := ""
	if len(modes) > 0 {
		mode = modes[0]
	}
	if len(modes) > 1 || mode != "" && (mode != "bash" || scope == nil || !scope.BelongsTo(controller.options.Bootstrap.Owner()) || !controller.options.PublicPrint || !controller.options.NativeTools) {
		return Admission{}, cooperative.ErrRestricted
	}
	if !controller.active {
		return Admission{}, errors.New("headless session is not active")
	}
	content, uuid, priority, err := controller.validateSDKInput(input)
	if err != nil {
		return Admission{}, err
	}
	if truthy(uuid) {
		key, err := stringKey(uuid)
		if err != nil {
			return Admission{}, err
		}
		historical, err := controller.hasOriginalUUID(scope, uuid)
		if err != nil {
			return Admission{}, err
		}
		if historical || controller.received[key] {
			result := Admission{Duplicate: true, Historical: historical}
			if controller.options.ReplayUserMessages {
				ack := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("user")}, protocol.JSONField{Name: "message", Value: input.Get("message")}, protocol.JSONField{Name: "session_id", Value: quote(controller.SessionID())}, protocol.JSONField{Name: "parent_tool_use_id", Value: json.RawMessage("null")}, protocol.JSONField{Name: "uuid", Value: uuid}, protocol.JSONField{Name: "timestamp", Value: input.Get("timestamp")}, protocol.JSONField{Name: "isReplay", Value: raw(true)})
				result.Acknowledgement = &ack
				controller.replays = append(controller.replays, ack.Clone())
			}
			if historical {
				controller.options.OnPhase("lifecycle:completed")
			}
			return result, nil
		}
	}
	if controller.newSDKAdmitted {
		return Admission{}, capability("multiple novel SDK messages require active-turn streaming/cancellation")
	}
	if truthy(uuid) {
		key, _ := stringKey(uuid)
		controller.trackReceived(key)
	}
	controller.newSDKAdmitted = true
	return Admission{}, controller.enqueueOwned(scope, command{Content: content, UUID: uuid, Priority: priority, Mode: mode})
}
func validatePlainSDK(input protocol.JSONObject) (content, uuid json.RawMessage, priority string, err error) {
	return validateSDKInput(input, false)
}
func validateSDKInput(input protocol.JSONObject, allowSlash bool, nativeBlocks ...bool) (content, uuid json.RawMessage, priority string, err error) {
	allowed := map[string]bool{"type": true, "message": true, "uuid": true, "priority": true, "timestamp": true, "session_id": true, "parent_tool_use_id": true}
	for _, field := range input.OwnProperties() {
		name := text(field.NameJSON)
		if !allowed[name] {
			return nil, nil, "", capability("SDK field outside original plain-text profile: " + name)
		}
	}
	if text(input.Get("type")) != "user" {
		return nil, nil, "", capability("non-user SDK input/control protocol")
	}
	content = object(input.Get("message")).Get("content")
	units, decodeError := protocol.DecodeJavaScriptJSONString(content)
	if decodeError != nil {
		if len(nativeBlocks) == 0 || !nativeBlocks[0] {
			return nil, nil, "", capability("SDK non-string prompt/file attachments")
		}
		var blocks []protocol.JSONObject
		if err = json.Unmarshal(content, &blocks); err != nil || blocks == nil {
			return nil, nil, "", capability("SDK native text content blocks")
		}
		for _, block := range blocks {
			units = nil
			switch block.String("type") {
			case "text":
				if units, err = protocol.DecodeJavaScriptJSONString(block.Get("text")); err != nil {
					return nil, nil, "", capability("SDK native text content block string")
				}
			case "image", "document":
				if !allowSlash || len(nativeBlocks) < 2 || !nativeBlocks[1] {
					return nil, nil, "", capability("SDK native media input processor")
				}
				// Native processing must normalize images before recording the
				// original user and metadata on this same Harness owner.
				if _, ok := block.Object("source"); !ok {
					return nil, nil, "", capability("SDK native media source")
				}
			default:
				return nil, nil, "", capability("SDK native media content block " + block.String("type"))
			}
		}
	}
	trimmed := trimUnits(units)
	if !allowSlash && len(trimmed) > 0 && trimmed[0] == '/' {
		return nil, nil, "", capability("slash-command dispatch")
	}
	uuid = input.Get("uuid")
	if truthy(uuid) {
		if _, err = stringKey(uuid); err != nil {
			return nil, nil, "", err
		}
	}
	priority = text(input.Get("priority"))
	if priority == "" {
		priority = "next"
	}
	if priority != "next" && priority != "now" && priority != "later" {
		return nil, nil, "", capability("unknown SDK queue priority")
	}
	return
}
func (controller *Controller) trackReceived(key string) {
	controller.received[key] = true
	controller.receivedOrder = append(controller.receivedOrder, key)
	if len(controller.receivedOrder) > 10000 {
		delete(controller.received, controller.receivedOrder[0])
		controller.receivedOrder = controller.receivedOrder[1:]
	}
}

// AdmitOriginalBatch is the finite host entry for an original bounded SDK input
// batch. Preflight uses independently loaded UUID membership and rejects multiple
// novel inputs before enqueuing anything. Activation may already have copied
// plans/backups; this is not a promise of rollback of activation side effects.
func (controller *Controller) AdmitOriginalBatch(inputs []protocol.JSONObject) ([]Admission, error) {
	return controller.admitOriginalBatch(nil, inputs)
}
func (controller *Controller) admitOriginalBatch(scope *cooperative.Scope, inputs []protocol.JSONObject, modes ...string) ([]Admission, error) {
	if !controller.active {
		return nil, errors.New("headless session is not active")
	}
	if scope == nil {
		if err := controller.primeMembers(); err != nil {
			return nil, err
		}
	}
	seen := map[string]bool{}
	novel := 0
	for _, input := range inputs {
		_, uuid, _, err := controller.validateSDKInput(input)
		if err != nil {
			return nil, err
		}
		if !truthy(uuid) {
			novel++
			continue
		}
		key, err := stringKey(uuid)
		if err != nil {
			return nil, err
		}
		historical, err := controller.hasOriginalUUID(scope, uuid)
		if err != nil {
			return nil, err
		}
		if !historical && !controller.received[key] && !seen[key] {
			novel++
		}
		seen[key] = true
	}
	if novel > 1 {
		return nil, capability("multiple novel SDK messages require active-turn streaming/cancellation")
	}
	if novel > 0 && controller.nativeFileHistory == nil && envTruthy(controller.options.ResumeOptions.Environment["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"]) && !envTruthy(controller.options.ResumeOptions.Environment["CLAUDE_CODE_DISABLE_FILE_CHECKPOINTING"]) {
		return nil, capability("active file-history snapshot creation before query")
	}
	results := []Admission{}
	for _, input := range inputs {
		value, err := controller.admitSDK(scope, input, modes...)
		if err != nil {
			return nil, err
		}
		results = append(results, value)
	}
	return results, nil
}
func (controller *Controller) enqueue(input command) error {
	return controller.enqueueOwned(nil, input)
}
func (controller *Controller) enqueueOwned(scope *cooperative.Scope, input command) error {
	controller.queue = append(controller.queue, input)
	mode := input.Mode
	if mode == "" {
		mode = "prompt"
	}
	controller.queueEvents = append(controller.queueEvents, protocol.NewJSONObject(protocol.JSONField{Name: "mode", Value: quote(mode)}, protocol.JSONField{Name: "value", Value: input.Content}, protocol.JSONField{Name: "uuid", Value: input.UUID}, protocol.JSONField{Name: "priority", Value: quote(input.Priority)}))
	controller.options.OnPhase("enqueue")
	if err := controller.publishTaskNotificationOnOwner(scope, len(controller.queue)-1); err != nil {
		return err
	}
	return controller.logQueueOwned(scope, "enqueue", input.Content)
}
func (controller *Controller) logQueue(operation string, content json.RawMessage) error {
	return controller.logQueueOwned(nil, operation, content)
}
func (controller *Controller) logQueueOwned(scope *cooperative.Scope, operation string, content json.RawMessage) error {
	entry := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("queue-operation")}, protocol.JSONField{Name: "operation", Value: quote(operation)}, protocol.JSONField{Name: "timestamp", Value: quote(controller.options.Now().UTC().Format("2006-01-02T15:04:05.000Z"))}, protocol.JSONField{Name: "sessionId", Value: quote(controller.SessionID())}, protocol.JSONField{Name: "content", Value: content})
	if scope != nil {
		token, err := controller.writer.async.BeginTrackedWrite(scope)
		if err != nil {
			return err
		}
		future, err := controller.writer.async.BeginAppend(scope, controller.options.ResumeOptions.Loader, &entry, "")
		if err != nil {
			_ = token.Complete(scope)
			return err
		}
		_ = future.thenOwner(controller.writer.async.owner, func(scope *cooperative.Scope, err error) {
			_ = token.Complete(scope)
			if err != nil {
				controller.writer.options.OnError(err)
			}
		})
		return nil
	}
	return controller.writer.Append(&entry)
}

// AcceptNext performs the finite plain-text processUserInput before request
// preparation. It does not counterfeit QueryEngine results or SDK completion.
func (controller *Controller) AcceptNext() (*protocol.JSONObject, error) {
	return controller.acceptNext(nil)
}
func (controller *Controller) acceptNext(scope *cooperative.Scope) (*protocol.JSONObject, error) {
	if !controller.active {
		return nil, errors.New("headless session is not active")
	}
	if len(controller.queue) == 0 {
		return nil, nil
	}
	order := map[string]int{"now": 0, "next": 1, "later": 2}
	index := -1
	for candidate := range controller.queue {
		if controller.options.WriterOptions.AgentID == "" && controller.queue[candidate].AgentID != "" {
			continue
		}
		if index < 0 || order[controller.queue[candidate].Priority] < order[controller.queue[index].Priority] {
			index = candidate
		}
	}
	if index < 0 {
		return nil, nil
	}
	dequeue := func(index int) (command, error) {
		input := controller.queue[index]
		controller.queue = append(controller.queue[:index:index], controller.queue[index+1:]...)
		controller.options.OnPhase("dequeue")
		return input, controller.logQueueOwned(scope, "dequeue", nil)
	}
	var input command
	var batch []command
	var err error
	if controller.options.NativeInteractiveQueue {
		batch, err = controller.takeInteractiveBatch(scope, index)
		if err != nil {
			return nil, err
		}
		input = batch[0]
	} else {
		input, err = dequeue(index)
		if err != nil {
			return nil, err
		}
		batch = []command{input}
		for len(controller.queue) > 0 {
			next := -1
			for candidate := range controller.queue {
				if controller.options.WriterOptions.AgentID == "" && controller.queue[candidate].AgentID != "" {
					continue
				}
				if next < 0 || order[controller.queue[candidate].Priority] < order[controller.queue[next].Priority] {
					next = candidate
				}
			}
			if next < 0 {
				break
			}
			if input.Mode == "bash" || controller.options.NativeTools && input.Mode != "" && input.Mode != "prompt" || controller.queue[next].Mode != input.Mode || controller.queue[next].Meta != input.Meta || controller.inboxPending(input.UUID) || controller.inboxPending(controller.queue[next].UUID) {
				break
			}
			candidate, err := dequeue(next)
			if err != nil {
				return nil, err
			}
			batch = append(batch, candidate)
		}

	}
	if len(batch) > 1 && !controller.options.NativeInteractiveQueue {
		content := []uint16{}
		for index, candidate := range batch {
			if index > 0 {
				content = append(content, '\n')
			}
			units, err := protocol.DecodeJavaScriptJSONString(candidate.Content)
			if err != nil {
				return nil, err
			}
			content = append(content, units...)
			if truthy(candidate.UUID) {
				input.UUID = candidate.UUID
			}
		}
		input.Content = protocol.QuoteJavaScriptUTF16(content)
		if controller.options.ReplayUserMessages {
			for _, candidate := range batch {
				if truthy(candidate.UUID) && string(candidate.UUID) != string(input.UUID) {
					message := protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: quote("user")}, protocol.JSONField{Name: "content", Value: candidate.Content})
					controller.replays = append(controller.replays, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("user")}, protocol.JSONField{Name: "message", Value: raw(message)}, protocol.JSONField{Name: "session_id", Value: quote(controller.SessionID())}, protocol.JSONField{Name: "parent_tool_use_id", Value: raw(nil)}, protocol.JSONField{Name: "uuid", Value: candidate.UUID}, protocol.JSONField{Name: "isReplay", Value: raw(true)}))
				}
			}
		}
	}
	for _, candidate := range batch {
		if candidate.UUID != nil {
			if controller.commandLifecycle != nil {
				controller.commandLifecycleIDs = append(controller.commandLifecycleIDs, append(json.RawMessage(nil), candidate.UUID...))
				controller.commandLifecycle(candidate.UUID, "started")
			}
			controller.options.OnPhase("lifecycle:started")
		}
	}
	controller.options.OnPhase("ask:start")
	promptID := quote(controller.options.UUID())
	controller.promptID = promptID
	controller.localInputResult = nil
	controller.promptInputResult = nil
	var processed *ProcessedInput
	if controller.inputProcessor != nil {
		if controller.options.NativeInteractiveQueue && len(batch) > 1 {
			if controller.inputProcessor.ProcessQueuedBatch == nil {
				return nil, capability("actual interactive queued input producer")
			}
			processed, err = controller.inputProcessor.ProcessQueuedBatch(scope, queuedInputs(batch))
		} else if input.Mode == "bash" {
			if controller.inputProcessor.ProcessTerminalBash == nil {
				return nil, capability("actual terminal Bash input producer")
			}
			processed, err = controller.inputProcessor.ProcessTerminalBash(scope, input.Content)
		} else if controller.inputProcessor.ProcessQueued != nil {
			processed, err = controller.inputProcessor.ProcessQueued(scope, input.Content, input.UUID, input.SkipSlash, input.Meta, input.Origin)
		} else {
			processed, err = controller.inputProcessor.Process(scope, input.Content, input.UUID)
		}
		if err != nil {
			return nil, err
		}
	}
	var message protocol.JSONObject
	if processed != nil {
		if len(processed.Messages) == 0 {
			return nil, capability("actual processUserInput output")
		}
		for _, current := range processed.Messages {
			record := current
			if input.Meta && record.String("type") == "user" {
				record.Set("isMeta", raw(true))
			}
			controller.messages = append(controller.messages, &record)
		}
		message = processed.Messages[0]
		controller.localInputResult = processed.LocalResult
		if processed.LocalResult == nil {
			controller.promptInputResult = processed
		}
	} else {
		userOptions := storage.Object{"content": input.Content, "uuid": input.UUID, "permissionMode": quote("default")}
		if input.Meta {
			userOptions["isMeta"] = raw(true)
		}
		values := controller.options.Factory.CreateUser(userOptions)
		message = orderedUser(values)
		controller.messages = append(controller.messages, &message)
	}
	if !controller.options.WriterOptions.SessionPersistenceDisabled && scope == nil {
		controller.options.OnPhase("record")
		if err := controller.primeMembers(); err != nil {
			return nil, err
		}
		if _, err := controller.writer.Record(controller.messages, promptID); err != nil {
			return nil, err
		}
	}
	return &message, nil
}
func orderedUser(value storage.Object) protocol.JSONObject { return messages.OrderedUser(value) }
func (controller *Controller) Prepare(input requestcontext.Context, dependencies requestcontext.Dependencies) (*requestcontext.PreparedContext, error) {
	resolved, err := controller.ResolveContext(context.Background(), input)
	if err != nil {
		return nil, err
	}
	return requestcontext.Prepare(resolved, dependencies)
}
func (controller *Controller) Writer() *Writer { return controller.writer }
func (controller *Controller) SessionID() string {
	if controller.options.Bootstrap != nil {
		return controller.options.Bootstrap.SessionID()
	}
	return controller.sid
}
func (controller *Controller) BootstrapOwns(owner *cooperative.Owner) bool {
	return controller.options.Bootstrap == nil || controller.options.Bootstrap.Owns(owner)
}
func (controller *Controller) SnapshotRecords() []protocol.JSONObject {
	controller.snapshotMu.Lock()
	defer controller.snapshotMu.Unlock()
	result := []protocol.JSONObject{}
	for _, item := range controller.snapshotRecords {
		result = append(result, item.Clone())
	}
	return result
}
func (controller *Controller) ResumeReturnedSnapshotCount() int {
	controller.snapshotMu.Lock()
	defer controller.snapshotMu.Unlock()
	return controller.resumeReturnedSnapshotCount
}
func (controller *Controller) Replays() []protocol.JSONObject {
	result := []protocol.JSONObject{}
	for _, item := range controller.replays {
		result = append(result, item.Clone())
	}
	return result
}
func (controller *Controller) QueueEvents() []protocol.JSONObject {
	result := []protocol.JSONObject{}
	for _, event := range controller.queueEvents {
		result = append(result, event.Clone())
	}
	return result
}
func (controller *Controller) primeMembers() error {
	if controller.writer.Primed() {
		return nil
	}
	value, err := controller.options.ResumeOptions.Loader.LoadTranscriptFile(controller.writer.target(), false)
	if errors.Is(err, os.ErrNotExist) {
		return controller.writer.Prime(nil)
	}
	if err != nil {
		return err
	}
	data, err := value.MarshalJSON()
	if err != nil {
		return err
	}
	snapshot := object(data)
	var pairs [][2]json.RawMessage
	if err = json.Unmarshal(snapshot.Get("messages"), &pairs); err != nil {
		return err
	}
	uuids := []json.RawMessage{}
	for _, pair := range pairs {
		uuids = append(uuids, pair[0])
	}
	return controller.writer.Prime(uuids)
}
func (controller *Controller) AwaitBackground() { controller.resume.AwaitBackground() }
func (controller *Controller) History() []protocol.JSONObject {
	result := make([]protocol.JSONObject, 0, len(controller.messages))
	for _, message := range controller.messages {
		result = append(result, message.Clone())
	}
	return result
}

func (controller *Controller) hasOriginalUUID(scope *cooperative.Scope, uuid json.RawMessage) (bool, error) {
	if scope == nil {
		if controller.options.AsyncPersistence {
			return false, capability("independent persistence requires canonical owner admission")
		}
		if err := controller.primeMembers(); err != nil {
			return false, err
		}
		return controller.writer.Has(uuid)
	}
	if controller.writer.async == nil || !scope.BelongsTo(controller.writer.async.owner) {
		return false, cooperative.ErrRestricted
	}
	future, err := controller.writer.async.SessionUUIDs(scope, controller.options.ResumeOptions.Loader, controller.SessionID())
	if err != nil {
		return false, err
	}
	set, err := future.Await(scope)
	if err != nil {
		return false, err
	}
	return set.OwnerHas(scope, uuid)
}

// ModelAccountingFor exposes the one Controller session authority. Every cost
// read/write still requires its existing Owner Scope; another Host cannot reset
// it, create an independent mutable ledger, or reconstruct it from transcript.
func (controller *Controller) ModelAccountingFor(owner *cooperative.Owner) (*modelaccounting.Session, error) {
	if controller == nil || !controller.BootstrapOwns(owner) || !controller.modelAccounting.Owns(owner) {
		return nil, cooperative.ErrRestricted
	}
	return controller.modelAccounting, nil
}
