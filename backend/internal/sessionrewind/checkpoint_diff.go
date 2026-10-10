package sessionrewind

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Selector struct {
	TargetUserMessageID string  `json:"targetUserMessageId,omitempty"`
	UserMessageIndex    *int    `json:"userMessageIndex,omitempty"`
	ExpectedContent     *string `json:"expectedContent,omitempty"`
}

type BadRequest struct{ Message string }

func (e *BadRequest) Error() string { return e.Message }

type CheckpointDiffResult struct {
	Target  Target `json:"target"`
	WorkDir string `json:"workDir"`
	Path    string `json:"path"`
	State   string `json:"state"`
	Diff    string `json:"diff,omitempty"`
	Error   string `json:"error,omitempty"`
}

type selectedTarget struct {
	Target
	activeIndex int
	user        protocol.JSONObject
}

func promptText(message protocol.JSONObject) string {
	if content, ok := stringValue(message.Get("content")); ok {
		return content
	}
	parts := []string{}
	for _, block := range blocks(message) {
		if text(block, "type") == "text" {
			if value, ok := stringValue(block.Get("text")); ok {
				parts = append(parts, value)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func resolveTarget(messages []protocol.JSONObject, selector Selector) (selectedTarget, error) {
	users := []protocol.JSONObject{}
	for _, message := range messages {
		if text(message, "type") == "user" {
			users = append(users, message)
		}
	}
	if len(users) == 0 {
		return selectedTarget{}, &BadRequest{"This session has no user messages to rewind."}
	}
	index := -1
	var selected protocol.JSONObject
	if selector.TargetUserMessageID != "" {
		for _, message := range messages {
			if text(message, "id") != selector.TargetUserMessageID {
				continue
			}
			if text(message, "type") != "user" {
				return selectedTarget{}, &BadRequest{"The selected rewind target is not a user message."}
			}
			selected = message
			for i, user := range users {
				if text(user, "id") == text(message, "id") {
					index = i
					break
				}
			}
			break
		}
	}
	if index < 0 && selector.UserMessageIndex != nil {
		index = *selector.UserMessageIndex
		if index >= 0 && index < len(users) {
			selected = users[index]
		}
	}
	if !selected.IsObject() || index < 0 || index >= len(users) {
		return selectedTarget{}, &BadRequest{fmt.Sprintf("Invalid rewind target. Expected targetUserMessageId or userMessageIndex 0-%d.", len(users)-1)}
	}
	if selector.ExpectedContent != nil {
		normalize := func(value string) string { return jsTrimCommand(strings.ReplaceAll(value, "\r\n", "\n")) }
		if normalize(promptText(selected)) != normalize(*selector.ExpectedContent) {
			return selectedTarget{}, &BadRequest{"The resolved rewind target does not match the selected prompt. Refresh the session and try again."}
		}
	}
	activeIndex := -1
	for i, message := range messages {
		if text(message, "id") == text(selected, "id") {
			activeIndex = i
			break
		}
	}
	if activeIndex < 0 {
		return selectedTarget{}, &BadRequest{"The selected user message is not in the active chain."}
	}
	return selectedTarget{Target{text(selected, "id"), index, len(users)}, activeIndex, selected}, nil
}

func comparablePath(path string) string { return strings.ReplaceAll(path, "\\", "/") }
func responsePath(path, base string) string {
	if filepath.IsAbs(path) {
		return path
	}
	rel, err := filepath.Rel(base, absolute(base, path))
	if err == nil && rel != "" && rel != "." && !strings.HasPrefix(comparablePath(rel), "../") {
		return comparablePath(rel)
	}
	return comparablePath(path)
}
func matchesPath(requested, path, base string) bool {
	requested = comparablePath(requested)
	return requested == comparablePath(absolute(base, path)) || requested == comparablePath(path) || requested == comparablePath(responsePath(path, base))
}

func checkpointPatch(path, before, after string, oldExists, newExists bool) string {
	oldName, newName := "/dev/null", "/dev/null"
	if oldExists {
		oldName = "a/" + path
	}
	if newExists {
		newName = "b/" + path
	}
	var out strings.Builder
	if oldName == newName {
		out.WriteString("Index: " + oldName + "\n")
	}
	out.WriteString("===================================================================\n--- " + oldName + "\t\n+++ " + newName + "\t\n")
	for _, hunk := range builtinwrite.StructuredPatchUnbounded(before, after) {
		oldStart, newStart := hunk.OldStart, hunk.NewStart
		if hunk.OldLines == 0 {
			oldStart--
		}
		if hunk.NewLines == 0 {
			newStart--
		}
		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", oldStart, hunk.OldLines, newStart, hunk.NewLines)
		for _, line := range hunk.Lines {
			out.WriteString(line)
			out.WriteByte('\n')
		}
	}
	return out.String()
}

// CheckpointDiff uses recorded boundaries in frozen mode. Live workspace bytes
// are read only when the original algorithm requests the final live boundary.
func (s *Service) CheckpointDiff(ctx context.Context, sessionID string, selector Selector, requestedPath string, frozen bool) (CheckpointDiffResult, error) {
	evidence, err := s.options.MessagesWithEvidence(ctx, sessionID)
	if err != nil {
		return CheckpointDiffResult{}, err
	}
	target, err := resolveTarget(evidence.Messages, selector)
	if err != nil {
		return CheckpointDiffResult{}, err
	}
	if frozen && ((selector.TargetUserMessageID != "" && selector.TargetUserMessageID != target.TargetUserMessageID) || (selector.UserMessageIndex != nil && *selector.UserMessageIndex != target.UserMessageIndex)) {
		return CheckpointDiffResult{}, &BadRequest{"The recorded checkpoint identity no longer matches this turn"}
	}
	workDir, err := s.options.WorkDir(ctx, sessionID)
	if err != nil {
		return CheckpointDiffResult{}, err
	}
	base := text(target.user, "cwd")
	if base == "" {
		base = workDir
	}
	raw, err := s.options.Snapshots(ctx, sessionID)
	if err != nil {
		return CheckpointDiffResult{}, err
	}
	if !raw.Complete {
		return CheckpointDiffResult{}, ErrSnapshotEvidenceIncomplete
	}
	snapshots, turns := loadSnapshots(raw.Rows), buildTurns(evidence.Messages)
	if evidence.Complete {
		if err = s.scopeCarriedSnapshots(ctx, sessionID, snapshots, turns, workDir, frozen); err != nil {
			return CheckpointDiffResult{}, err
		}
	}
	result := CheckpointDiffResult{Target: target.Target, WorkDir: base, Path: comparablePath(requestedPath), State: "missing"}
	var transcriptResult *CheckpointDiffResult
	if target.UserMessageIndex < len(turns) {
		changes := s.collect(turns[target.UserMessageIndex].messages, base, frozen)
		if changes.failure != nil {
			return CheckpointDiffResult{}, changes.failure
		}
		for _, change := range changes.confirmed {
			if matchesPath(requestedPath, change.path, base) || comparablePath(requestedPath) == comparablePath(change.absolute) {
				if change.diff != "" {
					value := result
					value.Path, value.State, value.Diff = change.path, "ok", change.diff
					transcriptResult = &value
				}
				break
			}
		}
	}
	fallback := func() CheckpointDiffResult {
		if transcriptResult != nil {
			return *transcriptResult
		}
		return result
	}
	targetSnapshot := snapshots[target.TargetUserMessageID]
	if targetSnapshot == nil {
		return fallback(), ctx.Err()
	}
	var next *snapshot
	if target.UserMessageIndex+1 < len(turns) {
		next = snapshots[text(turns[target.UserMessageIndex+1].user, "id")]
	}
	seen := map[string]bool{}
	for _, path := range keys(targetSnapshot.before) {
		identity := s.identity(absolute(base, path), frozen)
		if seen[identity] {
			continue
		}
		seen[identity] = true
		if !matchesPath(requestedPath, path, base) {
			continue
		}
		result.Path = responsePath(path, base)
		item := s.boundary(ctx, sessionID, base, path, targetSnapshot, next, frozen)
		if frozen {
			item = s.frozenBoundary(ctx, sessionID, base, path, targetSnapshot, next)
		}
		if err := ctx.Err(); err != nil {
			return CheckpointDiffResult{}, err
		}
		if !item.afterAvailable {
			return fallback(), nil
		}
		if !item.changed {
			return result, nil
		}
		if !item.textComparable {
			result.Error = "This file cannot be compared as UTF-8 text."
			return result, nil
		}
		result.State, result.Diff = "ok", checkpointPatch(result.Path, item.before.content, item.after.content, item.before.exists, item.after.exists)
		return result, ctx.Err()
	}
	return fallback(), ctx.Err()
}
