package teamreader

import (
	"context"
	"io"
	"os"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/localindex"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

func sameFileSnapshot(a, b localindex.FileSnapshot) bool {
	return a.Size == b.Size && a.MtimeMs == b.MtimeMs && a.CtimeMs == b.CtimeMs && (a.FileIdentity == nil || b.FileIdentity == nil || *a.FileIdentity == *b.FileIdentity)
}
func filePreservesPrefix(path string, previous protocol.JSONObject) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	stat, err := localindex.NativeFileSnapshot(file)
	if err != nil {
		return false
	}
	size := int64(numeric(previous.Get("size")))
	if stat.Size <= size || string(previous.Get("fileIdentity")) != "null" && stat.FileIdentity != nil && previous.String("fileIdentity") != *stat.FileIdentity {
		return false
	}
	first := make([]byte, min(size, 64*1024))
	last := make([]byte, min(size, 64*1024))
	if len(first) > 0 {
		if _, err = io.ReadFull(io.NewSectionReader(file, 0, int64(len(first))), first); err != nil {
			return false
		}
	}
	if len(last) > 0 {
		if _, err = io.ReadFull(io.NewSectionReader(file, max(0, size-64*1024), int64(len(last))), last); err != nil {
			return false
		}
	}
	return hash(first) == previous.String("firstWindowHash") && hash(last) == previous.String("lastWindowHash")
}

// A stale, incomplete or unavailable index deliberately uses the authoritative
// bounded file parser, matching the original gateway's fallback contract.
func (s *Service) readIndexedTranscript(ctx context.Context, path string, options protocol.JSONObject, projection TranscriptProjection) *protocol.JSONObject {
	index := s.options.LocalIndex
	if index == nil || index.Mode() != "on" || !index.SessionScopeReady(ctx) {
		return nil
	}
	page, err := index.Locators(ctx, path, nil)
	if err != nil || page == nil || page.Source.State != "ready" || page.Source.IndexedBytes != page.Source.Size {
		return nil
	}
	fingerprint := localindex.DeserializeFingerprint(page.Source.Fingerprint)
	if fingerprint == nil {
		return nil
	}
	current, err := localindex.SnapshotPath(path)
	if err != nil {
		return nil
	}
	currentAfter := int64(-1)
	if len(page.Entries) > 0 {
		currentAfter = page.Entries[len(page.Entries)-1].Ordinal
	}
	signature := hash([]byte(page.Source.Fingerprint))
	previous := decodeCursor(options.String("cursor"))
	after := int64(-1)
	if options.Get("afterOrdinal") != nil {
		after = int64(numeric(options.Get("afterOrdinal")))
	}
	reset := false
	if options.Get("cursor") != nil && previous == nil || after >= 0 && (options.String("signature") == "" || previous == nil) || after > currentAfter || previous != nil && int64(numeric(previous.Get("afterOrdinal"))) != after {
		after, reset = -1, true
	} else if options.String("signature") != "" {
		if options.String("signature") == signature {
			matches := previous != nil && numeric(previous.Get("size")) == float64(current.Size) && numeric(previous.Get("ctimeMs")) == current.CtimeMs && (string(previous.Get("fileIdentity")) == "null" || current.FileIdentity == nil || previous.String("fileIdentity") == *current.FileIdentity)
			if !matches {
				after, reset = -1, true
			}
		} else if previous == nil || !filePreservesPrefix(path, *previous) {
			after, reset = -1, true
		}
	}
	if current.Size != fingerprint.Size {
		return nil
	}
	selected := localindex.LocatorPage{Source: page.Source, Entries: []localindex.EntryLocator{}}
	for _, entry := range page.Entries {
		if entry.Ordinal > after && (entry.EntryType == "user" || entry.EntryType == "assistant" || entry.EntryType == "system" || entry.EntryType == "tool_use" || entry.EntryType == "tool_result" || entry.EntryType == "cc-haha-task-notification") {
			selected.Entries = append(selected.Entries, entry)
		}
	}
	var result *localindex.TargetedEntryRead
	err = s.options.HistoryBudget.With(ctx, sessionstore.MetadataLane, func() error {
		result = localindex.ReadEntriesByLocator(ctx, path, filepath.Join(s.options.ConfigDir, "projects"), filepath.Base(filepath.Dir(path)), selected)
		return nil
	})
	if err != nil || result == nil {
		return nil
	}
	afterRead, err := localindex.SnapshotPath(path)
	if err != nil || !sameFileSnapshot(current, afterRead) {
		return nil
	}
	messages, notifications := []protocol.JSONObject{}, []protocol.JSONObject{}
	indices := map[string]int{}
	for _, entry := range result.Entries {
		if message := s.transcriptMessage(entry, projection.OwnerAgentID); message != nil {
			messages = append(messages, *message)
		}
		if notification := taskNotification(entry, projection.OwnerAgentID); notification != nil {
			key := string(notification.Get("toolUseId"))
			if i, ok := indices[key]; ok {
				notifications[i] = *notification
			} else {
				indices[key] = len(notifications)
				notifications = append(notifications, *notification)
			}
		}
	}
	owners := projection.OwnerAgentIDs
	if owners == nil {
		owners = []string{}
	}
	cursor := protocol.NewJSONObject(field("version", 2), field("size", fingerprint.Size), field("ctimeMs", current.CtimeMs), field("fileIdentity", current.FileIdentity), field("firstWindowHash", fingerprint.FirstWindowHash), field("lastWindowHash", fingerprint.LastWindowHash), field("afterOrdinal", currentAfter))
	out := protocol.NewJSONObject(field("messages", messages), field("ownerAgentIds", owners), field("taskNotifications", notifications), field("signature", signature), field("cursor", encodeCursor(cursor)), field("afterOrdinal", currentAfter))
	if reset {
		out.Set("reset", raw(true))
	}
	return &out
}
