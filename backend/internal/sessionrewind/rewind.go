package sessionrewind

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
)

type TrimResult struct {
	RemovedCount      int      `json:"removedCount"`
	RemovedMessageIDs []string `json:"removedMessageIds"`
}

type Mode string

const (
	Both             Mode = "both"
	ConversationOnly Mode = "conversation"
)

type Failure struct {
	Message string
	Cause   error
}

func (e *Failure) Error() string { return e.Message }
func (e *Failure) Unwrap() error { return e.Cause }

type UnsupportedOperation struct{ Feature string }

func (e *UnsupportedOperation) Error() string {
	return "session rewind requires native capability: " + e.Feature
}

type RewindPreview struct {
	Target                  Target       `json:"target"`
	Conversation            Conversation `json:"conversation"`
	Code                    CodePreview  `json:"code"`
	RestoreAvailable        bool         `json:"restoreAvailable"`
	UnverifiedChangeSources []string     `json:"unverifiedChangeSources"`
}
type ExecuteConversation struct {
	MessagesRemoved   int      `json:"messagesRemoved"`
	RemovedMessageIDs []string `json:"removedMessageIds"`
}
type ExecuteResult struct {
	Target                  Target              `json:"target"`
	Conversation            ExecuteConversation `json:"conversation"`
	Code                    CodePreview         `json:"code"`
	RestoreAvailable        bool                `json:"restoreAvailable"`
	UnverifiedChangeSources []string            `json:"unverifiedChangeSources"`
	Mode                    Mode                `json:"mode"`
}

func ParseMode(raw json.RawMessage) (Mode, error) {
	if raw == nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return Both, nil
	}
	value, ok := stringValue(raw)
	if ok && (Mode(value) == Both || Mode(value) == ConversationOnly) {
		return Mode(value), nil
	}
	return "", &BadRequest{"Invalid rewind mode: expected 'both' or 'conversation'."}
}

type restorePlanEntry struct {
	tracking, absolute string
	original, target   FileState
}

func stateMatches(a, b FileState) bool {
	return a.Exists == b.Exists && (!a.Exists || bytes.Equal(a.Content, b.Content))
}
func stateBytes(value FileState) fileBytes {
	return fileBytes{defined: true, exists: value.Exists, data: value.Content}
}

type orderedSnapshot struct {
	id    string
	value *snapshot
}

func snapshotOrder(rows []json.RawMessage, values map[string]*snapshot) []orderedSnapshot {
	seen := map[string]bool{}
	result := []orderedSnapshot{}
	for _, raw := range rows {
		row := object(raw)
		if text(row, "type") != "file-history-snapshot" || !truthy(row.Get("snapshot")) {
			continue
		}
		value := object(row.Get("snapshot"))
		id, ok := stringValue(value.Get("messageId"))
		if !ok {
			id, _ = stringValue(row.Get("messageId"))
		}
		if id != "" && !seen[id] && values[id] != nil {
			seen[id] = true
			result = append(result, orderedSnapshot{id, values[id]})
		}
	}
	return result
}

func trackedPaths(snapshots []orderedSnapshot) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, snap := range snapshots {
		for _, path := range keys(snap.value.before) {
			if !seen[path] {
				seen[path] = true
				result = append(result, path)
			}
		}
	}
	return result
}
func targetBackup(path string, snapshots []orderedSnapshot, target *snapshot) json.RawMessage {
	value := object(target.before.Get(path))
	if value.IsObject() && value.Get("backupFileName") != nil {
		return value.Get("backupFileName")
	}
	for _, snap := range snapshots {
		value := object(snap.value.before.Get(path))
		if primitiveKey(value.Get("version")) == "number:1" {
			return value.Get("backupFileName")
		}
	}
	return nil
}

func (s *Service) restorePlan(ctx context.Context, id, base string, snapshots []orderedSnapshot, target *snapshot, files []string, writable bool) ([]restorePlanEntry, error) {
	if s.options.Files == nil {
		return nil, &UnsupportedOperation{"file states, modes and writable preflight"}
	}
	plan := []restorePlanEntry{}
	backups := map[string]string{}
	identities := map[string]bool{}
	identityOrder := []string{}
	for _, path := range files {
		identity := s.identity(path, false)
		if !identities[identity] {
			identities[identity] = true
			identityOrder = append(identityOrder, identity)
		}
	}
	for _, path := range trackedPaths(snapshots) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		abs := s.absolute(base, path)
		identity := s.identity(abs, false)
		if !identities[identity] {
			continue
		}
		raw := targetBackup(path, snapshots, target)
		if raw == nil {
			return nil, &BadRequest{"No restore point is available for tracked path: " + path}
		}
		key := primitiveKey(raw)
		if old, ok := backups[identity]; ok {
			if old != key {
				return nil, &BadRequest{"Conflicting checkpoints for tracked path: " + path}
			}
			continue
		}
		backups[identity] = key
		if !s.options.SafeTrackedPath(ctx, s.absolute(base, "."), path) {
			return nil, &BadRequest{"Tracked path became unsafe before restore: " + path}
		}
		original, err := s.options.Files.ReadCurrentState(ctx, abs)
		if err != nil {
			return nil, err
		}
		desired := FileState{}
		if !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			name, ok := stringValue(raw)
			if !ok {
				return nil, ErrUnsafeCheckpointFile
			}
			desired, err = s.options.Files.ReadBackupState(ctx, id, name)
			if err != nil {
				return nil, err
			}
			if !desired.Exists {
				return nil, &BadRequest{"Checkpoint backup is missing: " + name}
			}
		}
		if stateMatches(original, desired) {
			continue
		}
		if writable {
			if err = s.options.Files.AssertWritable(ctx, abs, original, desired); err != nil {
				return nil, err
			}
		}
		plan = append(plan, restorePlanEntry{path, abs, original, desired})
	}
	for _, identity := range identityOrder {
		if _, ok := backups[identity]; !ok {
			return nil, &BadRequest{"No restore point is available for changed path: " + identity}
		}
	}
	return plan, nil
}

func (s *Service) rollback(ctx context.Context, base string, plan []restorePlanEntry) []string {
	// Rollback must remain possible after the request is canceled.
	ctx = context.WithoutCancel(ctx)
	failures := []string{}
	for i := len(plan) - 1; i >= 0; i-- {
		entry := plan[i]
		if !s.options.SafeTrackedPath(ctx, s.absolute(base, "."), entry.tracking) {
			failures = append(failures, "Tracked path became unsafe: "+entry.tracking)
			continue
		}
		if err := s.options.Files.WriteState(ctx, entry.absolute, entry.original); err != nil {
			failures = append(failures, err.Error())
		}
	}
	return failures
}

func (s *Service) applyPlan(ctx context.Context, base string, plan []restorePlanEntry) error {
	attempted := []restorePlanEntry{}
	for _, entry := range plan {
		var err error
		if !s.options.SafeTrackedPath(ctx, s.absolute(base, "."), entry.tracking) {
			err = &BadRequest{"Tracked path became unsafe before restore: " + entry.tracking}
		} else {
			attempted = append(attempted, entry)
			err = s.options.Files.WriteState(ctx, entry.absolute, entry.target)
		}
		if err == nil {
			continue
		}
		failures := s.rollback(ctx, base, attempted)
		if len(failures) > 0 {
			return &Failure{"Restore failed and rollback was incomplete: " + strings.Join(failures, "; "), err}
		}
		return &BadRequest{"The checkpoint could not be restored safely. No messages or files were changed."}
	}
	return nil
}

func (s *Service) codeFromPlan(ctx context.Context, previous CodePreview, plan []restorePlanEntry) CodePreview {
	stats := map[string]changeStats{}
	files := []string{}
	for _, entry := range plan {
		identity := s.identity(entry.absolute, false)
		old, new := checkpointText(stateBytes(entry.target), true), checkpointText(stateBytes(entry.original), true)
		item := changeStats{nonText: !old.defined || !new.defined}
		if !item.nonText {
			item.insertions, item.deletions = countDiffLines(ctx, old.content, new.content)
		}
		if original, ok := previous.stats[identity]; ok && !item.nonText && !original.nonText {
			item = original
		}
		stats[identity] = item
		files = append(files, entry.absolute)
	}
	return normalizeStats(files, stats)
}

func (s *Service) legacyCodePreview(ctx context.Context, id, base string, snapshots []orderedSnapshot, target *snapshot) (CodePreview, bool) {
	if len(snapshots) == 0 {
		return CodePreview{FilesChanged: []string{}, Reason: "No file checkpoints were recorded for this session."}, true
	}
	if target == nil {
		return CodePreview{FilesChanged: []string{}, Reason: "No file checkpoint is available for the selected message."}, true
	}
	stats := map[string]changeStats{}
	files := []string{}
	seen := map[string]string{}
	restore := true
	for _, path := range trackedPaths(snapshots) {
		raw := targetBackup(path, snapshots, target)
		if raw == nil {
			continue
		}
		abs := s.absolute(base, path)
		identity := s.identity(abs, false)
		if old, ok := seen[identity]; ok {
			if old != primitiveKey(raw) {
				restore = false
			}
			continue
		}
		seen[identity] = primitiveKey(raw)
		if !s.options.SafeTrackedPath(ctx, s.absolute(base, "."), path) {
			restore = false
			continue
		}
		current, backup := s.current(ctx, abs), s.backup(ctx, id, raw)
		if !current.defined || !backup.defined {
			restore = false
			continue
		}
		if sameBytes(current, backup) {
			continue
		}
		old, new := checkpointText(current, true), checkpointText(backup, true)
		if !backup.exists {
			old, new = new, old
		}
		item := changeStats{nonText: !old.defined || !new.defined}
		if !item.nonText {
			item.insertions, item.deletions = countDiffLines(ctx, old.content, new.content)
		}
		stats[identity] = item
		files = append(files, abs)
	}
	return normalizeStats(files, stats), restore
}

func (s *Service) unavailableRestorePoints(ctx context.Context, id, base string, target *snapshot) []string {
	result := []string{}
	seen := map[string]bool{}
	add := func(path string) {
		if !seen[path] {
			seen[path] = true
			result = append(result, path)
		}
	}
	if target.malformedBefore {
		add("invalid before-file map")
	}
	backups := map[string]string{}
	for _, path := range unionKeys(target.before, target.completed) {
		response := responsePath(path, base)
		backup := object(target.before.Get(path))
		raw := backup.Get("backupFileName")
		name, isString := stringValue(raw)
		isNull := bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
		if !backup.IsObject() || (!isNull && !isString) {
			add(response)
			continue
		}
		identity := s.identity(s.absolute(base, path), false)
		if old, ok := backups[identity]; ok && old != primitiveKey(raw) {
			add(response)
			continue
		}
		backups[identity] = primitiveKey(raw)
		if !isNull {
			if _, err := s.options.ReadBackup(ctx, id, name); err != nil {
				add(response)
			}
		}
	}
	return result
}

func (s *Service) mergeCode(old, new CodePreview) CodePreview {
	if !old.Available {
		return new
	}
	if !new.Available {
		return old
	}
	stats := map[string]changeStats{}
	for id, item := range old.stats {
		stats[id] = item
	}
	files := append([]string{}, old.FilesChanged...)
	seen := map[string]bool{}
	for _, path := range old.FilesChanged {
		seen[s.identity(path, false)] = true
	}
	for _, path := range new.FilesChanged {
		identity := s.identity(path, false)
		if seen[identity] {
			continue
		}
		seen[identity] = true
		files = append(files, path)
		if item, ok := new.stats[identity]; ok {
			stats[identity] = item
		}
	}
	return normalizeStats(files, stats)
}

func (s *Service) rewindCheckpoint(ctx context.Context, id string, target selectedTarget, evidence Evidence, raw SnapshotEvidence, workDir string) (TurnCheckpoint, error) {
	turns := buildTurns(evidence.Messages)
	snapshots := loadSnapshots(raw.Rows)
	original := loadSnapshots(raw.Rows)
	if evidence.Complete {
		if err := s.scopeCarriedSnapshots(ctx, id, snapshots, turns, workDir, false); err != nil {
			return TurnCheckpoint{}, err
		}
	}
	checkpoints := []TurnCheckpoint{}
	unavailable := []string{}
	unavailableSeen := map[string]bool{}
	for index := target.UserMessageIndex; index < len(turns); index++ {
		turn := turns[index]
		base := text(turn.user, "cwd")
		if base == "" {
			base = workDir
		}
		var next *snapshot
		if index+1 < len(turns) {
			next = snapshots[text(turns[index+1].user, "id")]
		}
		checkpoint, err := s.preview(ctx, id, base, snapshots[text(turn.user, "id")], next, turn.messages, evidence.Complete, false)
		if err != nil {
			return TurnCheckpoint{}, err
		}
		checkpoint.Target = Target{text(turn.user, "id"), index, len(turns)}
		checkpoint.Conversation = Conversation{len(evidence.Messages) - target.activeIndex}
		checkpoint.WorkDir = base
		checkpoints = append(checkpoints, checkpoint)
		if snapshot := original[text(turn.user, "id")]; snapshot != nil {
			for _, path := range s.unavailableRestorePoints(ctx, id, base, snapshot) {
				if !unavailableSeen[path] {
					unavailableSeen[path] = true
					unavailable = append(unavailable, path)
				}
			}
		}
	}
	if len(checkpoints) == 0 {
		return TurnCheckpoint{}, &BadRequest{"The selected user message is not in the active chain."}
	}
	result := checkpoints[0]
	sources := []string{}
	restore := len(unavailable) == 0
	for i, checkpoint := range checkpoints {
		restore = restore && checkpoint.RestoreAvailable
		sources = append(sources, checkpoint.UnverifiedChangeSources...)
		if i > 0 {
			result.Code = s.mergeCode(result.Code, checkpoint.Code)
		}
	}
	if restore && result.Code.Available && original[target.TargetUserMessageID] != nil {
		plan, err := s.restorePlan(ctx, id, result.WorkDir, snapshotOrder(raw.Rows, original), original[target.TargetUserMessageID], result.Code.FilesChanged, false)
		if err != nil {
			var capability *UnsupportedOperation
			if errors.As(err, &capability) {
				return TurnCheckpoint{}, err
			}
			restore = false
			result.Code.Available = false
			result.Code.Reason = "The checkpoint could not be prepared safely."
		} else {
			result.Code = s.codeFromPlan(ctx, result.Code, plan)
		}
	}
	if len(unavailable) > 0 {
		result.Code.Available = false
		result.Code.Reason = "Recorded restore points are incomplete for: " + strings.Join(unavailable, ", ")
		for _, path := range unavailable {
			sources = append(sources, "file-history:"+path)
		}
	}
	result.RestoreAvailable = restore
	result.UnverifiedChangeSources = normalizedSources(sources)
	return result, ctx.Err()
}

func (s *Service) loadSelected(ctx context.Context, id string, selector Selector) (selectedTarget, Evidence, SnapshotEvidence, string, error) {
	evidence, err := s.options.MessagesWithEvidence(ctx, id)
	if err != nil {
		return selectedTarget{}, Evidence{}, SnapshotEvidence{}, "", err
	}
	target, err := resolveTarget(evidence.Messages, selector)
	if err != nil {
		return target, Evidence{}, SnapshotEvidence{}, "", err
	}
	raw, err := s.options.Snapshots(ctx, id)
	if err != nil {
		return target, Evidence{}, raw, "", err
	}
	if !raw.Complete {
		return target, Evidence{}, raw, "", ErrSnapshotEvidenceIncomplete
	}
	workDir, err := s.options.WorkDir(ctx, id)
	return target, evidence, raw, workDir, err
}

func rewindPreview(checkpoint TurnCheckpoint, code CodePreview, restore bool) RewindPreview {
	return RewindPreview{checkpoint.Target, checkpoint.Conversation, code, restore, checkpoint.UnverifiedChangeSources}
}
func (s *Service) Preview(ctx context.Context, id string, selector Selector) (RewindPreview, error) {
	target, evidence, raw, workDir, err := s.loadSelected(ctx, id, selector)
	if err != nil {
		return RewindPreview{}, err
	}
	checkpoint, err := s.rewindCheckpoint(ctx, id, target, evidence, raw, workDir)
	if err != nil {
		return RewindPreview{}, err
	}
	if checkpoint.Code.Available || !checkpoint.RestoreAvailable {
		return rewindPreview(checkpoint, checkpoint.Code, checkpoint.RestoreAvailable), nil
	}
	values := loadSnapshots(raw.Rows)
	code, restore := s.legacyCodePreview(ctx, id, checkpoint.WorkDir, snapshotOrder(raw.Rows, values), values[target.TargetUserMessageID])
	return rewindPreview(checkpoint, code, restore), ctx.Err()
}

func (s *Service) Execute(ctx context.Context, id string, selector Selector, mode Mode) (ExecuteResult, error) {
	if mode == "" {
		mode = Both
	}
	if mode != Both && mode != ConversationOnly {
		return ExecuteResult{}, &BadRequest{"Invalid rewind mode: expected 'both' or 'conversation'."}
	}
	if s.options.StopSessionAndWait == nil || s.options.TrimSessionMessagesFrom == nil {
		return ExecuteResult{}, &UnsupportedOperation{"shared runtime stop/drain and physical transcript trim"}
	}
	initial, err := s.options.MessagesWithEvidence(ctx, id)
	if err != nil {
		return ExecuteResult{}, err
	}
	selected, err := resolveTarget(initial.Messages, selector)
	if err != nil {
		return ExecuteResult{}, err
	}
	if err = s.options.StopSessionAndWait(ctx, id); err != nil {
		return ExecuteResult{}, err
	}
	target, evidence, raw, workDir, err := s.loadSelected(ctx, id, Selector{TargetUserMessageID: selected.TargetUserMessageID, ExpectedContent: selector.ExpectedContent})
	if err != nil {
		return ExecuteResult{}, err
	}
	checkpoint, err := s.rewindCheckpoint(ctx, id, target, evidence, raw, workDir)
	if err != nil {
		return ExecuteResult{}, err
	}
	if mode == Both && !checkpoint.RestoreAvailable {
		return ExecuteResult{}, &BadRequest{"This turn includes file changes without a complete restorable checkpoint. No messages or files were changed."}
	}
	values := loadSnapshots(raw.Rows)
	ordered := snapshotOrder(raw.Rows, values)
	legacy, legacyRestore := s.legacyCodePreview(ctx, id, checkpoint.WorkDir, ordered, values[target.TargetUserMessageID])
	hasTurn := checkpoint.Code.Available || !checkpoint.RestoreAvailable
	if mode == Both && !hasTurn && !legacyRestore {
		return ExecuteResult{}, &BadRequest{"One or more tracked files cannot be safely restored from this checkpoint. No messages or files were changed."}
	}
	code, restore := legacy, legacyRestore
	if hasTurn {
		code, restore = checkpoint.Code, checkpoint.RestoreAvailable
	}
	plan := []restorePlanEntry{}
	if mode == Both && code.Available && len(ordered) > 0 {
		if values[target.TargetUserMessageID] == nil {
			return ExecuteResult{}, &BadRequest{"No file checkpoint is available for the selected message."}
		}
		plan, err = s.restorePlan(ctx, id, checkpoint.WorkDir, ordered, values[target.TargetUserMessageID], code.FilesChanged, true)
		if err != nil {
			var bad *BadRequest
			var capability *UnsupportedOperation
			if errors.As(err, &bad) || errors.As(err, &capability) {
				return ExecuteResult{}, err
			}
			return ExecuteResult{}, &BadRequest{"The checkpoint could not be prepared safely. No messages or files were changed."}
		}
		if err = s.applyPlan(ctx, checkpoint.WorkDir, plan); err != nil {
			return ExecuteResult{}, err
		}
	}
	trim, err := s.options.TrimSessionMessagesFrom(ctx, id, target.TargetUserMessageID)
	if err != nil {
		failures := s.rollback(ctx, checkpoint.WorkDir, plan)
		if len(failures) > 0 {
			return ExecuteResult{}, &Failure{"Transcript trim failed and file rollback was incomplete: " + strings.Join(failures, "; "), err}
		}
		return ExecuteResult{}, err
	}
	return ExecuteResult{target.Target, ExecuteConversation{trim.RemovedCount, trim.RemovedMessageIDs}, code, restore, checkpoint.UnverifiedChangeSources, mode}, nil
}
