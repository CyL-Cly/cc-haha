package sessioncontroller

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/remoteio"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
)

// WriterOptions are original host inputs and shared CLI settings state. No
// process HOME or parsed Source history is used to choose a transcript.
type WriterOptions struct {
	Bootstrap                                      *bootstrapstate.State
	ConfigDir, CWD, SessionID, ProjectDir, Version string
	AgentID                                        string
	Environment                                    map[string]string
	SessionPersistenceDisabled                     bool
	SettingsState                                  *clisettings.State
	Files                                          TranscriptFiles
	// Branch is an actual host Git capability. Source catches its errors.
	Branch   func() (string, error)
	OnError  func(error)
	Schedule func(time.Duration, func()) func()
	Now      func() time.Time
}
type queuedEntry struct {
	entry    *protocol.JSONObject
	identity identity
}
type Writer struct {
	nativeParentSession    *nativeParentSessionSnapshot
	remote                 *remoteio.Service
	ingress                *remoteio.Ingress
	ingressURL             string
	ingressContext         context.Context
	ingressEnabled         func() bool
	ingressFailure         func(error)
	mu                     sync.Mutex
	options                WriterOptions
	path                   string
	pending                []*protocol.JSONObject
	queues                 map[string][]queuedEntry
	queueGenerations       map[string]*transcriptQueueGeneration
	queueFirst             *transcriptQueueGeneration
	queueLast              *transcriptQueueGeneration
	identities             map[string]identity
	members                map[string]bool
	excluded               map[string]bool
	excludedSessions       map[string]map[string]bool
	metadata               protocol.JSONObject
	lastPrompt             json.RawMessage
	primed                 bool
	stop                   func()
	liveEntries            map[*protocol.JSONObject]*streamhistory.Envelope
	liveFileHistoryEntries map[*protocol.JSONObject]*protocol.JSONObject
	async                  *PendingWriter
}

func NewWriter(options WriterOptions) (*Writer, error) {
	if options.ConfigDir == "" || options.CWD == "" || options.SessionID == "" || options.Version == "" || options.Environment == nil || options.SettingsState == nil || options.Files == nil || options.Branch == nil || options.OnError == nil || options.Schedule == nil || options.Now == nil {
		return nil, errors.New("headless writer requires original paths/environment, CLI settings, files, Git, errors and scheduler")
	}
	cache := options.SettingsState.GetSessionSettingsCache()
	if cache == nil || cache.Settings == nil {
		return nil, capability("unresolved original CLI settings")
	}
	excluded := map[string]bool{}
	return &Writer{options: options, queues: map[string][]queuedEntry{}, identities: map[string]identity{}, members: map[string]bool{}, excluded: excluded, excludedSessions: map[string]map[string]bool{options.SessionID: excluded}, metadata: protocol.NewJSONObject(), liveEntries: map[*protocol.JSONObject]*streamhistory.Envelope{}, liveFileHistoryEntries: map[*protocol.JSONObject]*protocol.JSONObject{}}, nil
}
func RealSchedule(delay time.Duration, callback func()) func() {
	timer := time.AfterFunc(delay, callback)
	return func() { timer.Stop() }
}
func (writer *Writer) skipped() bool {
	env := writer.options.Environment
	cache := writer.options.SettingsState.GetSessionSettingsCache()
	cleanupDisabled := false
	if cache != nil && cache.Settings != nil {
		var scalar any
		if json.Unmarshal(cache.Settings.Get("cleanupPeriodDays"), &scalar) == nil {
			number, numeric := scalar.(float64)
			cleanupDisabled = numeric && number == 0
		}
	}
	return env["NODE_ENV"] == "test" && !envTruthy(env["TEST_ENABLE_SESSION_PERSISTENCE"]) || cleanupDisabled || writer.options.SessionPersistenceDisabled || envTruthy(env["CLAUDE_CODE_SKIP_PROMPT_HISTORY"])
}
func (writer *Writer) currentSessionID() string {
	if writer.nativeParentSession != nil {
		return writer.nativeParentSession.parent.SessionID()
	}
	if writer.options.Bootstrap != nil {
		return writer.options.Bootstrap.SessionID()
	}
	return writer.options.SessionID
}
func (writer *Writer) target() string {
	dir := writer.options.ProjectDir
	cwd := writer.options.CWD
	if writer.options.Bootstrap != nil {
		snapshot := writer.options.Bootstrap.Snapshot()
		if writer.nativeParentSession != nil {
			snapshot = writer.nativeParentSession.parent.Snapshot()
		}
		cwd = snapshot.OriginalCWD
		if project := snapshot.SessionProjectDir; project != nil {
			return writer.transcriptIn(*project, writer.currentSessionID())
		}
		dir = ""
	}
	if dir == "" {
		dir = filepath.Join(writer.options.ConfigDir, "projects", sanitize(cwd))
	}
	return writer.transcriptIn(dir, writer.currentSessionID())
}

func (writer *Writer) transcriptIn(project, sid string) string {
	if writer.options.AgentID != "" {
		return filepath.Join(project, sid, "subagents", "agent-"+writer.options.AgentID+".jsonl")
	}
	return filepath.Join(project, sid+".jsonl")
}
func sanitize(value string) string { return projectpath.Sanitize(value) }

// Switch occurs after restore and before the Project pointer reset.
func (writer *Writer) Switch(sid, projectDir string) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.options.SessionID = sid
	writer.options.ProjectDir = projectDir
	writer.excluded = writer.exclusions(sid)
}
func (writer *Writer) Reset() error {
	if writer.options.Bootstrap != nil {
		return cooperative.ErrRestricted
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.reset()
	return nil
}
func (writer *Writer) reset() {
	for _, entry := range writer.pending {
		delete(writer.liveFileHistoryEntries, entry)
	}
	writer.path = ""
	writer.pending = nil
}

// ResetOnOwner is Project.resetSessionFile's synchronous private method.
func (writer *Writer) ResetOnOwner(scope *cooperative.Scope) error {
	if writer.options.Bootstrap == nil {
		return cooperative.ErrRestricted
	}
	if err := writer.options.Bootstrap.CheckScope(scope); err != nil {
		return err
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.reset()
	return nil
}

// BeginResetSessionFile is Source's async exported resetSessionFilePointer:
// synchronous mutation followed by an immediately fulfilled original Promise.
func (writer *Writer) BeginResetSessionFile(scope *cooperative.Scope) (*ownerasync.OwnerFuture[struct{}], error) {
	if err := writer.ResetOnOwner(scope); err != nil {
		return nil, err
	}
	future, _ := ownerasync.NewOwnerFuture[struct{}](writer.options.Bootstrap.Owner())
	if err := future.Resolve(scope, struct{}{}); err != nil {
		return nil, err
	}
	return future, nil
}

// ComputedTranscriptPath never changes the cached physical sessionFile pointer.
func (writer *Writer) ComputedTranscriptPath(scope *cooperative.Scope) (string, error) {
	if writer.options.Bootstrap == nil {
		return "", cooperative.ErrRestricted
	}
	if err := writer.options.Bootstrap.CheckScope(scope); err != nil {
		return "", err
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.target(), nil
}

// AdoptSessionFile ports Source Project.adoptSessionFile: only the pointer
// changes. The public adoptResumedSessionFile wrapper also refreshes metadata
// with skipTitleRefresh; that wrapper is not implemented by this method.
// The finite runHeadless startup uses Reset; this is a separate Project seam.
func (writer *Writer) AdoptSessionFile() {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.path = writer.target()
}
func (writer *Writer) Prime(uuids []json.RawMessage) error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	for _, uuid := range uuids {
		key, err := stringKey(uuid)
		if err != nil {
			return err
		}
		writer.members[key] = true
	}
	writer.primed = true
	return nil
}
func (writer *Writer) Primed() bool { writer.mu.Lock(); defer writer.mu.Unlock(); return writer.primed }
func (writer *Writer) Has(uuid json.RawMessage) (bool, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	key, err := stringKey(uuid)
	return writer.members[key], err
}
func (writer *Writer) RestoreMetadata(value protocol.JSONObject) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	for _, name := range []string{"agentName", "agentColor", "agentSetting", "mode", "prUrl", "prRepository"} {
		if truthy(value.Get(name)) {
			writer.metadata.Set(name, value.Get(name))
		}
	}
	if truthy(value.Get("customTitle")) && !truthy(writer.metadata.Get("customTitle")) {
		writer.metadata.Set("customTitle", value.Get("customTitle"))
	}
	if value.Get("tag") != nil {
		writer.metadata.Set("tag", value.Get("tag"))
	}
	if value.Get("worktreeSession") != nil {
		writer.metadata.Set("worktreeSession", value.Get("worktreeSession"))
	}
	if value.Get("prNumber") != nil {
		writer.metadata.Set("prNumber", value.Get("prNumber"))
	}
}
func (writer *Writer) Snapshot() protocol.JSONObject {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return protocol.NewJSONObject(protocol.JSONField{Name: "path", Value: quote(writer.path)}, protocol.JSONField{Name: "pendingCount", Value: raw(len(writer.pending))}, protocol.JSONField{Name: "metadata", Value: raw(writer.metadata)}, protocol.JSONField{Name: "lastPrompt", Value: writer.lastPrompt})
}
func (writer *Writer) prepare(path string) error {
	if writer.skipped() {
		return nil
	}
	if err := writer.options.Files.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if writer.skipped() {
		return nil
	}
	file, err := writer.options.Files.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0600)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = errors.New("Refusing non-regular transcript target: " + path)
	}
	if err == nil {
		writer.identities[path], err = fileIdentity(info)
	}
	closeErr := file.Close()
	if closeErr != nil {
		err = closeErr
	}
	return err
}
func (writer *Writer) discard(entries []queuedEntry) {
	for _, item := range entries {
		delete(writer.liveEntries, item.entry)
		delete(writer.liveFileHistoryEntries, item.entry)
		if uuid := item.entry.Get("uuid"); uuid != nil {
			key, err := stringKey(uuid)
			if err == nil {
				sid := item.entry.String("sessionId")
				if sid == "" {
					sid = writer.currentSessionID()
				}
				writer.exclusions(sid)[key] = true
			}
		}
	}
	writer.lastPrompt = nil
}
func (writer *Writer) enqueue(entry *protocol.JSONObject) error {
	path := writer.path
	id, exists := writer.identities[path]
	if !exists {
		file, err := writer.options.Files.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
		if errors.Is(err, fs.ErrNotExist) {
			writer.discard([]queuedEntry{{entry: entry}})
			return nil
		}
		if err != nil {
			return err
		}
		info, err := file.Stat()
		if err == nil && !info.Mode().IsRegular() {
			err = errors.New("Refusing non-regular transcript target: " + path)
		}
		if err == nil {
			id, err = fileIdentity(info)
		}
		if err == nil {
			// Source installs the identity before its finally close await.
			writer.identities[path] = id
		}
		closed := file.Close()
		if closed != nil {
			err = closed
		}
		if err != nil {
			return err
		}
	}
	writer.appendQueuedLocked(path, queuedEntry{entry, id})
	if writer.stop == nil {
		// Capture the immutable adapter on owner; timer callbacks read no Writer state.
		pending := writer.async
		writer.stop = writer.options.Schedule(100*time.Millisecond, func() {
			if pending != nil {
				pending.postDrain()
				return
			}
			if err := writer.Flush(); err != nil {
				writer.options.OnError(err)
			}
		})
	}
	return nil
}
func (writer *Writer) append(entry *protocol.JSONObject) error {
	if writer.async != nil && writer.async.closing {
		return ErrPendingClosed
	}
	if writer.skipped() {
		return nil
	}
	if writer.path == "" {
		writer.pending = append(writer.pending, entry)
		return nil
	}
	uuid := entry.Get("uuid")
	if uuid != nil {
		key, err := stringKey(uuid)
		if err != nil {
			return err
		}
		if writer.excluded[key] || writer.members[key] {
			return nil
		}
		if err = writer.enqueue(entry); err != nil {
			return err
		}
		writer.members[key] = true
		return nil
	}
	return writer.enqueue(entry)
}
func (writer *Writer) Append(entry *protocol.JSONObject) error {
	if entry == nil {
		return errors.New("nil transcript entry")
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.append(entry)
}
func (writer *Writer) Flush() error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.async != nil {
		return capability("independent transcript drain requires owner Begin/Await")
	}
	defer writer.compactQueueGenerationsLocked()

	if writer.stop != nil {
		writer.stop()
		writer.stop = nil
	}
	for path, queue := range writer.queues {
		writer.queues[path] = nil
		for _, item := range queue {
			defer delete(writer.liveEntries, item.entry)
			defer delete(writer.liveFileHistoryEntries, item.entry)
		}
		if writer.skipped() {
			writer.discard(queue)
			continue
		}
		file, err := writer.options.Files.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
		if errors.Is(err, fs.ErrNotExist) {
			writer.discard(queue)
			continue
		}
		if err != nil {
			return err
		}
		info, err := file.Stat()
		if err == nil && !info.Mode().IsRegular() {
			err = errors.New("Refusing non-regular transcript target: " + path)
		}
		var id identity
		if err == nil {
			id, err = fileIdentity(info)
		}
		var content []byte
		contentUnits := 0
		if err == nil {
			for _, item := range queue {
				if writer.skipped() || item.identity != id {
					writer.discard([]queuedEntry{item})
					continue
				}
				encoded, encodeErr := writer.marshalLiveEntry(item.entry)
				if encodeErr != nil {
					err = encodeErr
					break
				}
				encoded, encodeErr = protocol.ParseAndStringifyJavaScriptJSON(encoded)
				if encodeErr != nil {
					err = encodeErr
					break
				}
				lineUnits := len(utf16.Encode([]rune(string(encoded)))) + 1
				if contentUnits+lineUnits >= 100*1024*1024 {
					if err = writeAll(file, content); err != nil {
						break
					}
					content = nil
					contentUnits = 0
				}
				contentUnits += lineUnits
				content = append(content, encoded...)
				content = append(content, '\n')
			}
		}
		if err == nil && len(content) > 0 {
			err = writeAll(file, content)
		}
		closed := file.Close()
		if closed != nil {
			err = closed
		}
		if err != nil {
			return err
		}
		writer.deleteQueueLocked(path)
	}
	return nil
}
