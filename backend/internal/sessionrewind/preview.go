package sessionrewind

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func fmtHunk(old, new int) string { return fmt.Sprintf("@@ -1,%d +1,%d @@", old, new) }
func (s *Service) preview(ctx context.Context, id, base string, target, next *snapshot, messages []protocol.JSONObject, complete, frozen bool) (TurnCheckpoint, error) {
	checkpoint := s.snapshotPreview(ctx, id, base, target, next, frozen)
	evidence := s.collect(messages, base, frozen)
	if evidence.failure != nil {
		return TurnCheckpoint{}, evidence.failure
	}
	if checkpoint != nil && complete && len(evidence.unverified) == 0 {
		attributed := map[string]bool{}
		for _, item := range evidence.confirmed {
			attributed[item.identity] = true
		}
		for _, item := range evidence.uncertain {
			attributed[item.identity] = true
		}
		if len(attributed) > 0 {
			files := []string{}
			stats := map[string]changeStats{}
			for _, path := range checkpoint.code.FilesChanged {
				identity := s.identity(path, frozen)
				if attributed[identity] {
					files = append(files, path)
					if item, ok := checkpoint.stats[identity]; ok {
						stats[identity] = item
					}
				}
			}
			checkpoint.code = normalizeStats(files, stats)
			checkpoint.stats = stats
			checkpoint.restore = true
			for identity := range attributed {
				if checkpoint.unrestorable[identity] {
					checkpoint.restore = false
				}
			}
		}
	}
	transcript := CodePreview{FilesChanged: []string{}, stats: map[string]changeStats{}, Reason: "No transcript file changes were recorded for this turn."}
	if len(evidence.confirmed) > 0 {
		transcript.Available = true
		transcript.Reason = ""
		for _, item := range evidence.confirmed {
			transcript.FilesChanged = append(transcript.FilesChanged, item.absolute)
			transcript.Insertions += item.additions
			transcript.Deletions += item.deletions
			transcript.stats[item.identity] = changeStats{insertions: item.additions, deletions: item.deletions}
		}
	}
	result := TurnCheckpoint{Code: transcript, UnverifiedChangeSources: evidence.unverified}
	uncoveredUncertain := false
	for _, item := range evidence.uncertain {
		if checkpoint == nil || !checkpoint.covered[item.identity] {
			uncoveredUncertain = true
		}
	}
	if checkpoint == nil || !checkpoint.code.Available {
		result.RestoreAvailable = !transcript.Available && !uncoveredUncertain && complete
	} else {
		result.Code = checkpoint.code
		result.RestoreAvailable = checkpoint.restore && !uncoveredUncertain && complete
		for _, item := range evidence.confirmed {
			if checkpoint.covered[item.identity] {
				continue
			}
			result.Code.FilesChanged = append(result.Code.FilesChanged, item.absolute)
			result.Code.Insertions += item.additions
			result.Code.Deletions += item.deletions
			result.Code.stats[item.identity] = changeStats{insertions: item.additions, deletions: item.deletions}
			if !checkpoint.restorable[item.identity] {
				result.RestoreAvailable = false
			}
		}
	}
	if frozen && target != nil {
		transcriptDiff := map[string]bool{}
		for _, item := range evidence.confirmed {
			if item.diff != "" {
				transcriptDiff[item.identity] = true
			}
		}
		missing := []string{}
		for _, path := range unionKeys(target.before, target.completed) {
			identity := s.identity(s.absolute(base, path), true)
			if (checkpoint == nil || !checkpoint.covered[identity]) && !transcriptDiff[identity] {
				response := path
				if !filepath.IsAbs(path) {
					if rel, err := filepath.Rel(s.absolute(base, "."), s.absolute(base, path)); err == nil && rel != "" && !strings.HasPrefix(rel, "../") {
						response = filepath.ToSlash(rel)
					} else {
						response = strings.ReplaceAll(path, "\\", "/")
					}
				}
				missing = append(missing, response)
			}
		}
		if target.malformedBefore {
			missing = append(missing, "invalid before-file map")
		}
		if len(missing) > 0 {
			result.Code.Available = false
			result.Code.Reason = "Recorded file history is incomplete for: " + strings.Join(missing, ", ")
			result.RestoreAvailable = false
			for _, path := range missing {
				result.UnverifiedChangeSources = append(result.UnverifiedChangeSources, "file-history:"+path)
			}
			result.UnverifiedChangeSources = normalizedSources(result.UnverifiedChangeSources)
		}
	}
	return result, nil
}
