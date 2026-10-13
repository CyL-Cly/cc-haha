package localindex

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

const SessionParserVersion int64 = 10
const MaxProjectionRecordBytes = 8 * 1024 * 1024
const MaxProjectionRecords = 50000
const MaxProjectionMetadataBytes = 16 * 1024 * 1024
const SourceLimit = "LOCAL_INDEX_SOURCE_LIMIT"

var errProjectionCanceled = errors.New("Projection generation canceled")

type SessionCandidate struct {
	Path               string  `json:"path"`
	SessionID          string  `json:"sessionId"`
	ProjectPath        string  `json:"projectPath"`
	FallbackCreatedAt  string  `json:"fallbackCreatedAt"`
	FallbackModifiedAt string  `json:"fallbackModifiedAt"`
	FallbackWorkDir    *string `json:"fallbackWorkDir"`
	ModifiedAtMs       float64 `json:"modifiedAtMs"`
}
type ProjectionWork struct {
	MaxBufferedChunks int `json:"maxBufferedChunks"`
	MaxBufferedBytes  int `json:"maxBufferedBytes"`
}
type SessionProjectResult struct {
	Kind          string                `json:"kind"`
	Action        string                `json:"action,omitempty"`
	Projection    *TranscriptProjection `json:"projection,omitempty"`
	Work          *ProjectionWork       `json:"work,omitempty"`
	Reason        string                `json:"reason,omitempty"`
	EntryLocators *[]EntryLocator       `json:"entryLocators,omitempty"`
	Fingerprint   *Fingerprint          `json:"fingerprint,omitempty"`
}
type ProjectionProgress struct {
	State                         string
	Discovered, Indexed, Degraded int64
	LastErrorCode                 *string
}
type SourceMetadata struct{ BirthtimeISO, MtimeISO string }
type SessionProjectorOptions struct {
	Database                 *Database
	Index                    *SessionIndex
	Activity                 *ActivityIndex
	Scope                    string
	Budget                   *sessionstore.Budget
	Reduction                ReductionOptions
	SourceMetadata           func(string) (SourceMetadata, error)
	Now                      func() time.Time
	ParserVersion            int64
	RecordStreamingThreshold int
	CanCommit                func() bool
	Verify                   func(context.Context, string, Fingerprint) SourceChange
	BeforeSessionUpsert      func()
}
type retainedProjection struct {
	projection             *TranscriptProjection
	records, metadataBytes int
}
type SessionProjector struct {
	o          SessionProjectorOptions
	mu         sync.Mutex
	cache      map[string]retainedProjection
	cacheOrder []string
}

func NewSessionProjector(o SessionProjectorOptions) (*SessionProjector, error) {
	if o.Database == nil || o.Index == nil || o.Activity == nil || o.Scope == "" || o.Budget == nil || o.SourceMetadata == nil || o.Reduction.ParseDate == nil || o.Reduction.Compare == nil {
		return nil, errors.New("Session projector requires its shared primary database/index/activity/budget and actual metadata/language adapters")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.ParserVersion == 0 {
		o.ParserVersion = SessionParserVersion
	}
	if o.RecordStreamingThreshold <= 0 {
		o.RecordStreamingThreshold = MaxProjectionRecordBytes
	}
	o.RecordStreamingThreshold = min(MaxProjectionRecordBytes, max(1, o.RecordStreamingThreshold))
	if o.Verify == nil {
		o.Verify = VerifyFingerprint
	}
	return &SessionProjector{o: o, cache: map[string]retainedProjection{}}, nil
}
func sourceLimitError() error {
	return coded(SourceLimit, "Transcript exceeds the bounded local-index projection budget")
}
func initialSessionProjection(c SessionCandidate) *TranscriptProjection {
	return &TranscriptProjection{Summary: rowObject(protocol.JSONField{Name: "title", Value: raw("Untitled Session")}, protocol.JSONField{Name: "createdAt", Value: raw(c.FallbackCreatedAt)}, protocol.JSONField{Name: "modifiedAt", Value: raw(c.FallbackModifiedAt)}, protocol.JSONField{Name: "messageCount", Value: raw(0)}, protocol.JSONField{Name: "workDir", Value: raw(c.FallbackWorkDir)})}
}
func (p *SessionProjector) active(ctx context.Context) error {
	if ctx.Err() != nil || p.o.CanCommit != nil && !p.o.CanCommit() {
		return errProjectionCanceled
	}
	return nil
}
func (p *SessionProjector) progress(ctx context.Context, path string, progress *ProjectionProgress, fallback int64) error {
	if progress == nil {
		progress = &ProjectionProgress{State: "building", Discovered: fallback, Indexed: fallback}
	}
	state := progress.State
	if state == "" {
		state = "building"
	}
	return p.o.Database.Transaction(ctx, func(op *Operation) error {
		if e := p.active(ctx); e != nil {
			return e
		}
		if e := WriteSessionBackfill(op, Backfill{p.o.Scope, state, &path, progress.Discovered, progress.Indexed, progress.Degraded, progress.LastErrorCode, p.o.Now().UnixMilli()}); e != nil {
			return e
		}
		return p.active(ctx)
	})
}
func WriteSessionBackfill(op *Operation, state Backfill) error {
	return op.Exec(`INSERT INTO backfill_state(scope,state,watermark,discovered,indexed,degraded,last_error_code,updated_at_ms) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(scope) DO UPDATE SET state=excluded.state,watermark=excluded.watermark,discovered=excluded.discovered,indexed=excluded.indexed,degraded=excluded.degraded,last_error_code=excluded.last_error_code,updated_at_ms=excluded.updated_at_ms`, state.Scope, state.State, state.Watermark, state.Discovered, state.Indexed, state.Degraded, state.LastErrorCode, state.UpdatedAtMs)
}
func (p *SessionProjector) remember(path string, b retainedProjection) {
	delete(p.cache, path)
	for n, v := range p.cacheOrder {
		if v == path {
			p.cacheOrder = append(p.cacheOrder[:n], p.cacheOrder[n+1:]...)
			break
		}
	}
	p.cache[path] = b
	p.cacheOrder = append(p.cacheOrder, path)
	for len(p.cacheOrder) > 8 {
		old := p.cacheOrder[0]
		p.cacheOrder = p.cacheOrder[1:]
		delete(p.cache, old)
	}
}
func (p *SessionProjector) Project(ctx context.Context, c SessionCandidate, progress *ProjectionProgress) (SessionProjectResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if ctx.Err() != nil {
		return sessionRetry(SourceChange{Kind: "retry", Reason: "transient-io"}), nil
	}
	var result SessionProjectResult
	err := p.o.Budget.With(ctx, sessionstore.MetadataLane, func() error { var e error; result, e = p.project(ctx, c, progress, false); return e })
	return result, err
}
func (p *SessionProjector) ProjectActivity(ctx context.Context, c SessionCandidate) (SessionProjectResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if ctx.Err() != nil {
		return sessionRetry(SourceChange{Kind: "retry", Reason: "transient-io"}), nil
	}
	var result SessionProjectResult
	err := p.o.Budget.With(ctx, sessionstore.MetadataLane, func() error { var e error; result, e = p.project(ctx, c, nil, true); return e })
	return result, err
}
func sessionRetry(change SourceChange) SessionProjectResult {
	return SessionProjectResult{Kind: change.Kind, Reason: change.Reason}
}
func (p *SessionProjector) project(ctx context.Context, c SessionCandidate, progress *ProjectionProgress, activityOnly bool) (SessionProjectResult, error) {
	if e := p.active(ctx); e != nil {
		return sessionRetry(SourceChange{Kind: "retry", Reason: "transient-io"}), nil
	}
	action := "full"
	start := int64(0)
	seed := initialSessionProjection(c)
	cached, cachedExists := p.cache[c.Path]
	var existing *SessionSource
	if activityOnly {
		source, e := p.o.Activity.GetSource(ctx, c.Path)
		if e != nil {
			return SessionProjectResult{}, e
		}
		if source != nil {
			existing = &SessionSource{Path: source.Path, Size: source.Size, MtimeMs: source.MtimeMs, FileIdentity: source.FileIdentity, Fingerprint: source.Fingerprint, IndexedBytes: source.IndexedBytes, ParserVersion: source.ParserVersion, State: source.State}
			action = "rebuild"
		}
	} else {
		source, e := p.o.Index.GetSource(ctx, c.Path)
		if e != nil {
			return SessionProjectResult{}, e
		}
		existing = source
	}
	if existing != nil {
		previous := DeserializeFingerprint(existing.Fingerprint)
		change := SourceChange{Kind: "rebuild", Reason: "rewrite"}
		if previous != nil {
			change = DetectSourceChange(ctx, c.Path, *previous, p.o.ParserVersion)
		}
		if e := p.active(ctx); e != nil {
			return sessionRetry(SourceChange{Kind: "retry", Reason: "transient-io"}), nil
		}
		if change.Kind == "retry" {
			return sessionRetry(change), nil
		}
		if change.Kind == "deleted" {
			return p.delete(ctx, c.Path, progress, activityOnly)
		}
		if activityOnly {
			if change.Kind == "unchanged" {
				projection := initialSessionProjection(c)
				projection.IndexedBytes = existing.IndexedBytes
				projection.PendingTailBytes = existing.Size - existing.IndexedBytes
				work := ProjectionWork{}
				return SessionProjectResult{Kind: "indexed", Action: "unchanged", Projection: projection, Work: &work}, nil
			}
			if change.Kind == "append" && cachedExists {
				action = "append"
				start = change.ReadFrom
				seed = cached.projection
			}
		} else {
			previousProjection := cached.projection
			if !cachedExists {
				value, e := p.o.Index.ProjectionSeed(ctx, c.Path)
				if e != nil {
					return SessionProjectResult{}, e
				}
				if value != nil {
					previousProjection = &TranscriptProjection{Summary: objectValue(value.Get("summary")), IndexedBytes: intValue(value.Get("indexedBytes")), PendingTailBytes: intValue(value.Get("pendingTailBytes")), MalformedLineCount: intValue(value.Get("malformedLineCount"))}
				}
			}
			if existing.State == "degraded" {
				if existing.LastErrorCode != nil && *existing.LastErrorCode == SourceLimit && (change.Kind == "unchanged" || change.Kind == "append") {
					return SessionProjectResult{}, sourceLimitError()
				}
				action = "rebuild"
			} else if change.Kind == "unchanged" {
				activity, e := p.o.Activity.GetSource(ctx, c.Path)
				if e != nil {
					return SessionProjectResult{}, e
				}
				if activity != nil {
					count, e := p.o.Index.CountSources(ctx)
					if e != nil {
						return SessionProjectResult{}, e
					}
					if e = p.progress(ctx, c.Path, progress, count); e != nil {
						return SessionProjectResult{}, e
					}
					if previousProjection == nil {
						return sessionRetry(SourceChange{Kind: "retry", Reason: "transient-io"}), nil
					}
					work := ProjectionWork{}
					return SessionProjectResult{Kind: "indexed", Action: "unchanged", Projection: previousProjection, Work: &work}, nil
				}
				action = "rebuild"
			} else if change.Kind == "append" {
				depended := previous != nil && previousProjection != nil && stringValue(previousProjection.Summary.Get("modifiedAt")) == time.UnixMilli(int64(previous.MtimeMs)).UTC().Format("2006-01-02T15:04:05.000Z")
				if depended {
					action = "rebuild"
				} else {
					action = "append"
					start = change.ReadFrom
					if previousProjection != nil {
						seed = previousProjection
					}
				}
			} else {
				action = "rebuild"
			}
		}
	}
	built, locators, fingerprint, work, e := p.build(ctx, c, start, seed, activityOnly, cached)
	if e != nil && action == "append" && errorCode(e, "") == "LOCAL_INDEX_REBUILD_REQUIRED" {
		action = "rebuild"
		built, locators, fingerprint, work, e = p.build(ctx, c, 0, initialSessionProjection(c), activityOnly, retainedProjection{})
	}
	if e != nil {
		if errorCode(e, "") == SourceLimit {
			if existing != nil && !activityOnly {
				_ = p.o.Database.Transaction(ctx, func(op *Operation) error {
					if e := p.active(ctx); e != nil {
						return e
					}
					return op.Exec("UPDATE source_files SET state='degraded',last_error_code=?,updated_at_ms=? WHERE path=? AND NOT(state='degraded' AND last_error_code IS ?)", SourceLimit, p.o.Now().UnixMilli(), c.Path, SourceLimit)
				})
			}
			return SessionProjectResult{}, e
		}
		return sessionRetry(retryChange(e)), nil
	}
	verified := p.o.Verify(ctx, c.Path, *fingerprint)
	if e = p.active(ctx); e != nil {
		return sessionRetry(SourceChange{Kind: "retry", Reason: "transient-io"}), nil
	}
	if verified.Kind != "unchanged" {
		if verified.Kind != "retry" {
			verified = SourceChange{Kind: "retry", Reason: "changed-during-read"}
		}
		return sessionRetry(verified), nil
	}
	e = p.commit(ctx, c, built.projection, locators, *fingerprint, action == "append", progress, activityOnly)
	if errors.Is(e, errSourceChanged) || errors.Is(e, errProjectionCanceled) {
		return sessionRetry(SourceChange{Kind: "retry", Reason: "changed-during-read"}), nil
	}
	if e != nil {
		return SessionProjectResult{}, e
	}
	p.remember(c.Path, built)
	return SessionProjectResult{Kind: "indexed", Action: action, Projection: built.projection, Work: &work, EntryLocators: &locators, Fingerprint: fingerprint}, nil
}
func (p *SessionProjector) commit(ctx context.Context, c SessionCandidate, projection *TranscriptProjection, locators []EntryLocator, f Fingerprint, appendOnly bool, progress *ProjectionProgress, activityOnly bool) error {
	count, e := p.o.Index.CountSources(ctx)
	if e != nil {
		return e
	}
	if !activityOnly {
		old, e := p.o.Index.GetSource(ctx, c.Path)
		if e != nil {
			return e
		}
		if old == nil {
			count++
		}
	}
	state := "ready"
	if projection.PendingTailBytes > 0 {
		state = "pending"
	}
	updated := p.o.Now().UnixMilli()
	guard := func() error {
		if e := p.active(ctx); e != nil {
			return e
		}
		if !FingerprintMatchesPath(c.Path, f) {
			return errSourceChanged
		}
		return nil
	}
	return p.o.Database.Transaction(ctx, func(op *Operation) error {
		if e := guard(); e != nil {
			return e
		}
		if !activityOnly {
			if e := op.Exec(`INSERT INTO source_files(path,kind,size_bytes,mtime_ms,file_identity,prefix_hash,indexed_bytes,parser_version,state,last_error_code,updated_at_ms) VALUES(?,'transcript',?,?,?,?,?,?,?,NULL,?) ON CONFLICT(path) DO UPDATE SET kind=excluded.kind,size_bytes=excluded.size_bytes,mtime_ms=excluded.mtime_ms,file_identity=excluded.file_identity,prefix_hash=excluded.prefix_hash,indexed_bytes=excluded.indexed_bytes,parser_version=excluded.parser_version,state=excluded.state,last_error_code=NULL,updated_at_ms=excluded.updated_at_ms`, c.Path, f.Size, f.MtimeMs, f.FileIdentity, SerializeFingerprint(f), projection.IndexedBytes, p.o.ParserVersion, state, updated); e != nil {
				return e
			}
		}
		if projection.Activity != nil {
			if e := WriteActivityProjection(op, ActivitySourceDescriptor{c.Path, c.SessionID, c.ProjectPath, activityOnly, f, SerializeFingerprint(f), projection.IndexedBytes, p.o.ParserVersion, state, updated}, *projection.Activity, p.o.Reduction.ParseDate); e != nil {
				return e
			}
		} else if activityOnly {
			return errors.New("Activity projection is unavailable")
		}
		if activityOnly {
			return guard()
		}
		if !appendOnly {
			if e := op.Exec("DELETE FROM session_entries WHERE transcript_path=?", c.Path); e != nil {
				return e
			}
		}
		for _, locator := range locators {
			if e := op.Exec(`INSERT INTO session_entries(transcript_path,ordinal,jsonl_line,byte_start,byte_length,entry_type,message_id,role,timestamp,parent_tool_use_id) VALUES(?,?,?,?,?,?,?,?,?,?)`, c.Path, locator.Ordinal, locator.JSONLLine, locator.ByteStart, locator.ByteLength, locator.EntryType, locator.MessageID, locator.Role, locator.Timestamp, locator.ParentToolUseID); e != nil {
				return e
			}
		}
		if p.o.BeforeSessionUpsert != nil {
			p.o.BeforeSessionUpsert()
		}
		summary := projection.Summary
		modified, valid, e := p.o.Reduction.ParseDate(stringValue(summary.Get("modifiedAt")))
		if e != nil {
			return e
		}
		if !valid {
			return errors.New("Session projection has an invalid modifiedAt timestamp")
		}
		providerPresent := 0
		if summary.Get("runtimeProviderId") != nil {
			providerPresent = 1
		}
		repository := any(nil)
		if jsTruthy(summary.Get("repository")) {
			repository = string(protocol.StringifyValidatedJavaScriptJSON(summary.Get("repository")))
		}
		worktree := any(nil)
		if summary.Get("worktreeSession") != nil {
			worktree = string(protocol.StringifyValidatedJavaScriptJSON(summary.Get("worktreeSession")))
		}
		if e = op.Exec(`INSERT INTO sessions(transcript_path,session_id,project_path,title,created_at,modified_at,modified_at_ms,message_count,work_dir,repository_json,worktree_session_json,permission_mode,runtime_provider_id,runtime_provider_present,runtime_model_id,effort_level,openai_fast_mode,is_team_worker) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(transcript_path) DO UPDATE SET session_id=excluded.session_id,project_path=excluded.project_path,title=excluded.title,created_at=excluded.created_at,modified_at=excluded.modified_at,modified_at_ms=excluded.modified_at_ms,message_count=excluded.message_count,work_dir=excluded.work_dir,repository_json=excluded.repository_json,worktree_session_json=excluded.worktree_session_json,permission_mode=excluded.permission_mode,runtime_provider_id=excluded.runtime_provider_id,runtime_provider_present=excluded.runtime_provider_present,runtime_model_id=excluded.runtime_model_id,effort_level=excluded.effort_level,openai_fast_mode=excluded.openai_fast_mode,is_team_worker=excluded.is_team_worker`, c.Path, c.SessionID, c.ProjectPath, binding(summary.Get("title")), binding(summary.Get("createdAt")), binding(summary.Get("modifiedAt")), modified, binding(summary.Get("messageCount")), binding(summary.Get("workDir")), repository, worktree, binding(summary.Get("permissionMode")), binding(summary.Get("runtimeProviderId")), providerPresent, binding(summary.Get("runtimeModelId")), binding(summary.Get("effortLevel")), fastModeBinding(summary.Get("openAIFastMode")), booleanInt(jsTruthy(summary.Get("isTeamWorker")))); e != nil {
			return e
		}
		if progress == nil {
			progress = &ProjectionProgress{State: "building", Discovered: count, Indexed: count}
		}
		progressState := progress.State
		if progressState == "" {
			progressState = "building"
		}
		if e = WriteSessionBackfill(op, Backfill{p.o.Scope, progressState, &c.Path, progress.Discovered, progress.Indexed, progress.Degraded, progress.LastErrorCode, updated}); e != nil {
			return e
		}
		return guard()
	})
}
func (p *SessionProjector) Delete(ctx context.Context, path string, progress *ProjectionProgress) (SessionProjectResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.delete(ctx, path, progress, false)
}
func (p *SessionProjector) DeleteActivity(ctx context.Context, path string) (SessionProjectResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.delete(ctx, path, nil, true)
}
func (p *SessionProjector) delete(ctx context.Context, path string, progress *ProjectionProgress, activityOnly bool) (SessionProjectResult, error) {
	if e := p.active(ctx); e != nil {
		return sessionRetry(SourceChange{Kind: "retry", Reason: "transient-io"}), nil
	}
	guard := func() error {
		_, e := os.Stat(path)
		if os.IsNotExist(e) {
			return nil
		}
		if e == nil {
			return errSourceChanged
		}
		return e
	}
	if e := guard(); e != nil {
		return sessionRetry(retryChange(e)), nil
	}
	count, e := p.o.Index.CountSources(ctx)
	if e != nil {
		return SessionProjectResult{}, e
	}
	if progress == nil {
		remaining := max(int64(0), count-1)
		progress = &ProjectionProgress{State: "building", Discovered: remaining, Indexed: remaining}
	}
	err := p.o.Database.Transaction(ctx, func(op *Operation) error {
		if e := p.active(ctx); e != nil {
			return e
		}
		if e := guard(); e != nil {
			return e
		}
		if !activityOnly {
			if e := op.Exec("DELETE FROM source_files WHERE path=?", path); e != nil {
				return e
			}
		}
		if e := op.Exec("DELETE FROM activity_sources WHERE path=?", path); e != nil {
			return e
		}
		if !activityOnly {
			state := progress.State
			if state == "" {
				state = "building"
			}
			if e := WriteSessionBackfill(op, Backfill{p.o.Scope, state, &path, progress.Discovered, progress.Indexed, progress.Degraded, progress.LastErrorCode, p.o.Now().UnixMilli()}); e != nil {
				return e
			}
		}
		if e := guard(); e != nil {
			return e
		}
		return p.active(ctx)
	})
	if errors.Is(err, errSourceChanged) {
		return sessionRetry(SourceChange{Kind: "retry", Reason: "changed-during-read"}), nil
	}
	if err != nil {
		if errors.Is(err, errProjectionCanceled) {
			return sessionRetry(SourceChange{Kind: "retry", Reason: "transient-io"}), nil
		}
		return SessionProjectResult{}, err
	}
	delete(p.cache, path)
	return SessionProjectResult{Kind: "deleted"}, nil
}
func retainedMetadata(entry protocol.JSONObject, used *int) error {
	values := []json.RawMessage{}
	for _, field := range []string{"type", "uuid", "messageId", "timestamp", "parent_tool_use_id", "cwd", "workDir", "runtimeProviderId", "runtimeModelId", "customTitle", "aiTitle", "repository", "worktreeSession", "requestId", "version", "sessionId"} {
		values = append(values, entry.Get(field))
	}
	message := objectValue(entry.Get("message"))
	for _, field := range []string{"id", "role", "model"} {
		values = append(values, message.Get(field))
	}
	var blocks []json.RawMessage
	_ = json.Unmarshal(message.Get("content"), &blocks)
	for _, block := range blocks {
		fields := objectValue(block)
		if fields.String("type") == "tool_use" {
			values = append(values, fields.Get("name"))
			if fields.String("name") == "Skill" {
				values = append(values, objectValue(fields.Get("input")).Get("skill"))
			}
		}
		if len(values) > 16384 {
			return sourceLimitError()
		}
	}
	var iterations []json.RawMessage
	_ = json.Unmarshal(objectValue(message.Get("usage")).Get("iterations"), &iterations)
	for _, iteration := range iterations {
		fields := objectValue(iteration)
		if fields.String("type") == "advisor_message" {
			values = append(values, fields.Get("model"))
		}
		if len(values) > 16384 {
			return sourceLimitError()
		}
	}
	visited := 0
	for len(values) > 0 {
		value := values[len(values)-1]
		values = values[:len(values)-1]
		if len(value) == 0 || string(value) == "null" {
			continue
		}
		visited++
		if visited > 16384 {
			return sourceLimitError()
		}
		*used += 64
		if jsonStringValue(value) {
			units, e := protocol.DecodeJavaScriptJSONString(value)
			if e != nil {
				return e
			}
			if len(units) > 4096 {
				return sourceLimitError()
			}
			*used += len(string(utf16.Decode(units)))
		} else if value[0] == '{' {
			o := objectValue(value)
			for _, property := range o.OwnProperties() {
				units, _ := protocol.DecodeJavaScriptJSONString(property.NameJSON)
				*used += len(string(utf16.Decode(units)))
				values = append(values, property.Value)
				if len(values) > 16384 {
					return sourceLimitError()
				}
			}
		} else if value[0] == '[' {
			var array []json.RawMessage
			_ = json.Unmarshal(value, &array)
			for n, item := range array {
				*used += len(fmtInt(n))
				values = append(values, item)
				if len(values) > 16384 {
					return sourceLimitError()
				}
			}
		}
		if *used > MaxProjectionMetadataBytes {
			return sourceLimitError()
		}
	}
	return nil
}
func fmtInt(n int) string { return strconv.Itoa(n) }
