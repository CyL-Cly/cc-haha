package sessionrewind

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type snapshot struct {
	before            protocol.JSONObject
	completed         protocol.JSONObject
	completionPresent bool
	malformedBefore   bool
}

func keys(value protocol.JSONObject) []string {
	result := []string{}
	for _, property := range value.OwnProperties() {
		var key string
		_ = json.Unmarshal(property.NameJSON, &key)
		result = append(result, key)
	}
	return result
}
func loadSnapshots(rows []json.RawMessage) map[string]*snapshot {
	result := map[string]*snapshot{}
	for _, raw := range rows {
		row := object(raw)
		if text(row, "type") != "file-history-snapshot" || !truthy(row.Get("snapshot")) {
			continue
		}
		value := object(row.Get("snapshot"))
		id, present := stringValue(value.Get("messageId"))
		if !present {
			id, _ = stringValue(row.Get("messageId"))
		}
		if id == "" {
			continue
		}
		before := object(value.Get("trackedFileBackups"))
		malformed := !before.IsObject()
		if malformed {
			before = protocol.NewJSONObject()
		}
		completed := object(value.Get("completedFileBackups"))
		if !completed.IsObject() {
			completed = protocol.NewJSONObject()
		}
		result[id] = &snapshot{before, completed, value.Get("completedFileBackups") != nil, malformed}
	}
	return result
}

type fileBytes struct {
	defined, exists bool
	data            []byte
}

func (s *Service) backup(ctx context.Context, sessionID string, raw json.RawMessage) fileBytes {
	if raw == nil {
		return fileBytes{}
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return fileBytes{defined: true}
	}
	var name string
	if json.Unmarshal(raw, &name) != nil {
		return fileBytes{}
	}
	data, err := s.options.ReadBackup(ctx, sessionID, name)
	if err != nil {
		return fileBytes{}
	}
	return fileBytes{true, true, data}
}
func backupName(value protocol.JSONObject, path string) json.RawMessage {
	return object(value.Get(path)).Get("backupFileName")
}
func (s *Service) current(ctx context.Context, path string) fileBytes {
	data, exists, err := s.options.ReadCurrent(ctx, path)
	if err != nil {
		return fileBytes{}
	}
	return fileBytes{true, exists, data}
}

type fileText struct {
	defined, exists bool
	content         string
}

func checkpointText(value fileBytes, strict bool) fileText {
	if !value.defined {
		return fileText{}
	}
	if !value.exists {
		return fileText{defined: true}
	}
	if strict && (!utf8.Valid(value.data) || bytes.Contains(value.data, []byte{0})) {
		return fileText{}
	}
	return fileText{true, true, string(protocol.DecodeJavaScriptUTF8(value.data))}
}
func sameBytes(a, b fileBytes) bool {
	return a.exists == b.exists && (!a.exists || bytes.Equal(a.data, b.data))
}

type boundary struct {
	before, after                                             fileText
	afterAvailable, restoreAvailable, changed, textComparable bool
}

func (s *Service) boundary(ctx context.Context, id, base, path string, target, next *snapshot, frozen bool) boundary {
	before := s.backup(ctx, id, backupName(target.before, path))
	after := fileBytes{}
	if frozen && target.completionPresent {
		after = s.backup(ctx, id, backupName(target.completed, path))
	} else if next == nil {
		if !frozen {
			after = s.current(ctx, s.absolute(base, path))
		}
	} else {
		identity := s.identity(s.absolute(base, path), frozen)
		seen := map[string]json.RawMessage{}
		for _, candidate := range keys(next.before) {
			if s.identity(s.absolute(base, candidate), frozen) == identity {
				raw := backupName(next.before, candidate)
				seen[primitiveKey(raw)] = raw
			}
		}
		if len(seen) == 1 {
			for _, raw := range seen {
				after = s.backup(ctx, id, raw)
			}
		}
	}
	beforeText, afterText := checkpointText(before, frozen), checkpointText(after, frozen)
	restore := beforeText.defined && (frozen || next != nil || after.defined)
	afterAvailable := afterText.defined && (!frozen || restore)
	effectiveAfter := before
	if afterAvailable {
		effectiveAfter = after
	} else {
		afterText = beforeText
	}
	return boundary{beforeText, afterText, afterAvailable, restore, (!frozen && next == nil && !after.defined) || !sameBytes(before, effectiveAfter), checkpointText(before, true).defined && checkpointText(effectiveAfter, true).defined}
}
func (s *Service) frozenBoundary(ctx context.Context, id, base, path string, target, next *snapshot) boundary {
	identity := s.identity(s.absolute(base, path), true)
	var result boundary
	hasResult := false
	for _, candidate := range unionKeys(target.before, target.completed) {
		if s.identity(s.absolute(base, candidate), true) != identity {
			continue
		}
		item := s.boundary(ctx, id, base, candidate, target, next, true)
		if !item.afterAvailable {
			return item
		}
		if hasResult && (result.before != item.before || result.after != item.after) {
			result.afterAvailable = false
			return result
		}
		result = item
		hasResult = true
	}
	return result
}
func unionKeys(a, b protocol.JSONObject) []string {
	result := keys(a)
	seen := map[string]bool{}
	for _, p := range result {
		seen[p] = true
	}
	for _, p := range keys(b) {
		if !seen[p] {
			result = append(result, p)
			seen[p] = true
		}
	}
	return result
}

type changeStats struct {
	insertions, deletions int
	nonText               bool
}
type snapshotPreview struct {
	code                              CodePreview
	covered, restorable, unrestorable map[string]bool
	stats                             map[string]changeStats
	restore                           bool
}

func normalizeStats(files []string, stats map[string]changeStats) CodePreview {
	value := CodePreview{Available: true, FilesChanged: files, stats: stats}
	for _, item := range stats {
		value.Insertions += item.insertions
		value.Deletions += item.deletions
		if item.nonText {
			value.Reason = "Some changed files cannot be compared as UTF-8 text."
		}
	}
	return value
}
func (s *Service) snapshotPreview(ctx context.Context, id, base string, target, next *snapshot, frozen bool) *snapshotPreview {
	if target == nil {
		return nil
	}
	result := &snapshotPreview{covered: map[string]bool{}, restorable: map[string]bool{}, unrestorable: map[string]bool{}, stats: map[string]changeStats{}, restore: true}
	files := []string{}
	seen := map[string]string{}
	for _, path := range keys(target.before) {
		if ctx.Err() != nil {
			break
		}
		identity := s.identity(s.absolute(base, path), frozen)
		name := backupName(target.before, path)
		if name == nil {
			result.unrestorable[identity] = true
			result.restore = false
			continue
		}
		if old, ok := seen[identity]; ok {
			if old != primitiveKey(name) {
				result.unrestorable[identity] = true
				result.restore = false
			}
			continue
		}
		seen[identity] = primitiveKey(name)
		item := s.boundary(ctx, id, base, path, target, next, frozen)
		if frozen {
			item = s.frozenBoundary(ctx, id, base, path, target, next)
		}
		safe := s.options.SafeTrackedPath(ctx, s.absolute(base, "."), path)
		if item.restoreAvailable && safe {
			result.restorable[identity] = true
		}
		if item.afterAvailable {
			result.covered[identity] = true
		}
		if !item.restoreAvailable || !safe {
			result.unrestorable[identity] = true
			result.restore = false
		}
		if !item.changed {
			continue
		}
		files = append(files, expandTracking(base, path))
		stats := changeStats{nonText: !item.textComparable}
		if item.textComparable {
			stats.insertions, stats.deletions = countDiffLines(ctx, item.before.content, item.after.content)
		}
		result.stats[identity] = stats
	}
	result.code = normalizeStats(files, result.stats)
	return result
}
func lineTokens(content string) []string {
	result := []string{}
	start := 0
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			result = append(result, content[start:i+1])
			start = i + 1
		}
	}
	if start < len(content) {
		result = append(result, content[start:])
	}
	return result
}

// CountDiffLines supplies Source diffLines statistics to other native file
// projections. A caller must also check ctx.Err before returning the result.
func CountDiffLines(ctx context.Context, before, after string) (int, int) {
	return countDiffLines(ctx, before, after)
}

// Myers' shortest edit path counts the same newline-bearing tokens as diffLines.
// It uses linear frontier storage and checks cancellation during long histories.
func countDiffLines(ctx context.Context, before, after string) (int, int) {
	a, b := lineTokens(before), lineTokens(after)
	n, m := len(a), len(b)
	if n == 0 {
		return m, 0
	}
	if m == 0 {
		return 0, n
	}
	frontier := map[int]int{1: 0}
	for d := 0; d <= n+m; d++ {
		if ctx.Err() != nil {
			return 0, 0
		}
		for k := -d; k <= d; k += 2 {
			x := 0
			if k == -d || (k != d && frontier[k-1] < frontier[k+1]) {
				x = frontier[k+1]
			} else {
				x = frontier[k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			frontier[k] = x
			if x >= n && y >= m {
				deletions := (d + n - m) / 2
				return d - deletions, deletions
			}
		}
	}
	return m, n
}
func (s *Service) scopeCarriedSnapshots(ctx context.Context, id string, snapshots map[string]*snapshot, turns []turn, workDir string, frozen bool) error {
	attributed := map[string]protocol.JSONObject{}
	for _, turn := range turns {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		base := nullishCWD(turn.user, workDir)
		evidence := s.collect(turn.messages, base, frozen)
		if evidence.failure != nil {
			return evidence.failure
		}
		if len(evidence.unverified) > 0 {
			attributed = map[string]protocol.JSONObject{}
			continue
		}
		target := snapshots[text(turn.user, "id")]
		attempted, confirmed, uncertain := map[string]bool{}, map[string]bool{}, map[string]bool{}
		for _, item := range evidence.confirmed {
			attempted[item.identity] = true
			confirmed[item.identity] = true
		}
		for _, item := range evidence.uncertain {
			attempted[item.identity] = true
			uncertain[item.identity] = true
		}
		for identity := range attempted {
			delete(attributed, identity)
		}
		if target == nil {
			continue
		}
		before := target.before.Clone()
		for _, path := range keys(before) {
			backup := object(before.Get(path))
			name := backup.Get("backupFileName")
			if !backup.IsObject() || name == nil {
				continue
			}
			var filename string
			if string(name) != "null" && json.Unmarshal(name, &filename) != nil {
				continue
			}
			identity := s.identity(s.absolute(base, path), frozen)
			previous, ok := attributed[identity]
			if ok && primitiveKey(previous.Get("backupFileName")) == primitiveKey(name) && primitiveKey(previous.Get("version")) == primitiveKey(backup.Get("version")) {
				if !attempted[identity] {
					before.Delete(path)
				}
			} else {
				delete(attributed, identity)
			}
			if confirmed[identity] && !uncertain[identity] {
				delete(attributed, identity)
				raw := s.backup(ctx, id, name)
				if raw.defined {
					after := s.replay(turn.messages, base, identity, checkpointText(raw, true), frozen)
					if after.defined && !sameBytes(raw, fileBytes{true, after.exists, []byte(after.content)}) {
						attributed[identity] = backup
					}
				}
			}
		}
		copy := *target
		copy.before = before
		snapshots[text(turn.user, "id")] = &copy
	}
	return nil
}
func applyLiteralEdit(content, old, new string, all bool) string {
	if new == "" && !strings.HasSuffix(old, "\n") && strings.Contains(content, old+"\n") {
		old += "\n"
	}
	count := 1
	if all {
		count = -1
	}
	return strings.Replace(content, old, new, count)
}
