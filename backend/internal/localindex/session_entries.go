package localindex

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const MaxTargetedBytes int64 = 16 * 1024 * 1024

type TargetedEntryRead struct {
	Entries    []protocol.JSONObject `json:"entries"`
	BytesRead  int64                 `json:"bytesRead"`
	RangesRead int                   `json:"rangesRead"`
}
type locatorRange struct {
	start, length int64
	entries       []EntryLocator
}

func validLocators(page LocatorPage) ([]locatorRange, bool) {
	s := page.Source
	if s.Size < 0 || s.Size > 9007199254740991 || s.IndexedBytes < 0 || s.IndexedBytes > s.Size || s.ParserVersion < 2 || (s.State != "ready" && s.State != "pending") {
		return nil, false
	}
	ranges := []locatorRange{}
	var end, total, line int64
	ordinal := int64(-1)
	for _, entry := range page.Entries {
		next := entry.ByteStart + entry.ByteLength
		if entry.Ordinal < 0 || entry.Ordinal > 9007199254740991 || entry.Ordinal <= ordinal || entry.JSONLLine < 1 || entry.JSONLLine > 9007199254740991 || entry.JSONLLine <= line || entry.ByteStart < end || entry.ByteLength < 1 || next < entry.ByteStart || next > 9007199254740991 || next > s.IndexedBytes || entry.EntryType == "" {
			return nil, false
		}
		if len(ranges) > 0 && ranges[len(ranges)-1].start+ranges[len(ranges)-1].length == entry.ByteStart {
			last := &ranges[len(ranges)-1]
			last.length += entry.ByteLength
			last.entries = append(last.entries, entry)
		} else {
			ranges = append(ranges, locatorRange{entry.ByteStart, entry.ByteLength, []EntryLocator{entry}})
		}
		total += entry.ByteLength
		if total > MaxTargetedBytes {
			return nil, false
		}
		ordinal = entry.Ordinal
		line = entry.JSONLLine
		end = next
	}
	return ranges, true
}
func snapshotMatchesLocator(s FileSnapshot, page LocatorPage) bool {
	return s.Size == page.Source.Size && s.MtimeMs == page.Source.MtimeMs && sameIdentity(s.FileIdentity, page.Source.FileIdentity)
}
func locatorMatchesEntry(locator EntryLocator, entry protocol.JSONObject) bool {
	message, _ := decodeLocatorObject(entry.Get("message"))
	entryType := stringValue(entry.Get("type"))
	if len(entry.Get("type")) == 0 || entry.Get("type")[0] != '"' {
		entryType = "unknown"
	}
	id := nullableValue(entry.Get("uuid"))
	if id == nil {
		id = nullableValue(entry.Get("messageId"))
	}
	equal := func(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }
	return entryType == locator.EntryType && equal(id, locator.MessageID) && equal(nullableValue(message.Get("role")), locator.Role) && equal(nullableValue(entry.Get("timestamp")), locator.Timestamp) && equal(nullableValue(entry.Get("parent_tool_use_id")), locator.ParentToolUseID)
}

// ReadEntriesByLocator accepts only a direct canonical project transcript and
// verifies its actual bytes after range reads. A stale projection returns nil,
// allowing the caller to fall back to the authoritative transcript reader.
func ReadEntriesByLocator(ctx context.Context, path, projectsRoot, expectedProject string, page LocatorPage) *TargetedEntryRead {
	path, e := filepath.Abs(path)
	if e != nil || page.Source.Path != path {
		return nil
	}
	ranges, valid := validLocators(page)
	if !valid {
		return nil
	}
	fingerprint := DeserializeFingerprint(page.Source.Fingerprint)
	if fingerprint == nil || fingerprint.Size != page.Source.Size || fingerprint.MtimeMs != page.Source.MtimeMs || !sameNullable(fingerprint.FileIdentity, page.Source.FileIdentity) || fingerprint.IndexedBytes != page.Source.IndexedBytes || fingerprint.ParserVersion != page.Source.ParserVersion {
		return nil
	}
	link, e := os.Lstat(path)
	if e != nil || !link.Mode().IsRegular() || link.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	root, e := filepath.EvalSymlinks(projectsRoot)
	if e != nil {
		return nil
	}
	real, e := filepath.EvalSymlinks(path)
	if e != nil {
		return nil
	}
	parts := locatorPathParts(root, real)
	if len(parts) != 2 || parts[0] != expectedProject || !strings.HasSuffix(parts[1], ".jsonl") || filepath.Base(real) != parts[1] {
		return nil
	}
	f, e := os.Open(path)
	if e != nil {
		return nil
	}
	defer f.Close()
	before, e := fileSnapshot(f)
	if e != nil || !snapshotMatchesLocator(before, page) {
		return nil
	}
	out := &TargetedEntryRead{Entries: []protocol.JSONObject{}, RangesRead: len(ranges)}
	for _, r := range ranges {
		if ctx.Err() != nil {
			return nil
		}
		bytes := make([]byte, r.length)
		if _, e = io.ReadFull(io.NewSectionReader(f, r.start, r.length), bytes); e != nil {
			return nil
		}
		out.BytesRead += r.length
		for _, locator := range r.entries {
			start := locator.ByteStart - r.start
			data := bytes[start : start+locator.ByteLength]
			entry, e := decodeLocatorObject([]byte(trimJS(strings.ToValidUTF8(string(data), "\uFFFD"))))
			if e != nil || !entry.IsObject() || !locatorMatchesEntry(locator, entry) {
				return nil
			}
			out.Entries = append(out.Entries, entry)
		}
	}
	after, e := fileSnapshot(f)
	if e != nil || !snapshotMatchesLocator(after, page) {
		return nil
	}
	if VerifyFingerprint(ctx, path, *fingerprint).Kind != "unchanged" {
		return nil
	}
	// The original verifier reads first, last and boundary windows. Account for
	// the actual selected spans without counting stat/open operations as bytes.
	out.BytesRead += min(fingerprint.Size, int64(fingerprintWindow))*2 + min(fingerprint.IndexedBytes, int64(fingerprintWindow))
	current, e := os.Lstat(path)
	if e != nil || !current.Mode().IsRegular() || current.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	currentReal, e := filepath.EvalSymlinks(path)
	if e != nil || currentReal != real {
		return nil
	}
	currentSnapshot, e := fileSnapshot(f)
	if e != nil || !snapshotMatchesLocator(currentSnapshot, page) {
		return nil
	}
	if e = f.Close(); e != nil {
		return nil
	}
	return out
}
func sameNullable(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }
func locatorPathParts(root, path string) []string {
	child, e := filepath.Rel(root, path)
	if e != nil || child == "." || !contained(root, path) {
		return nil
	}
	return strings.Split(child, string(filepath.Separator))
}

// DecodeRawEntry is shared by targeted metadata callers that retain exact
// JavaScript JSON member ordering.
func DecodeRawEntry(data json.RawMessage) (protocol.JSONObject, error) {
	return decodeLocatorObject(data)
}

func decodeLocatorObject(data []byte) (protocol.JSONObject, error) {
	var out protocol.JSONObject
	e := json.Unmarshal(data, &out)
	return out, e
}
