package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

type subagentLookupCache struct {
	version    string
	transcript subagentTranscript
}

func subagentDirectory(info sessionInfo) string {
	return filepath.Join(filepath.Dir(info.Path), info.ID, "subagents")
}
func subagentPath(info sessionInfo, id string) string {
	if !strings.HasPrefix(id, "agent-") {
		id = "agent-" + id
	}
	return filepath.Join(subagentDirectory(info), id+".jsonl")
}
func subagentJSONBytes(value any) (int, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return 0, err
	}
	raw, err = protocol.ParseAndStringifyJavaScriptJSON(raw)
	return len(raw), err
}
func (server *Server) subagentLookup(ctx context.Context, info sessionInfo, toolRef, parentAgent string) (subagentTranscript, error) {
	path := info.Path
	if parentAgent != "" {
		path = subagentPath(info, parentAgent)
	}
	file, err := (sessionstore.OSFileSystem{}).Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return emptySubagentTranscript(), nil
	}
	if err != nil {
		return subagentTranscript{}, err
	}
	stat, err := file.Stat()
	closeErr := file.Close()
	if err != nil {
		return subagentTranscript{}, err
	}
	if closeErr != nil {
		return subagentTranscript{}, closeErr
	}
	version := fmt.Sprintf("%s:%s:%d:%s", stat.Dev, stat.Ino, stat.Size, stat.MtimeNS)
	keyJSON, _ := json.Marshal([]string{path, toolRef})
	key := string(keyJSON)
	server.mu.Lock()
	cached, found := server.subagentLookups[key]
	server.mu.Unlock()
	if found && cached.version == version {
		return cached.transcript, nil
	}
	result := emptySubagentTranscript()
	err = server.budget.With(ctx, sessionstore.MetadataLane, func() error {
		entries := []sessionstore.Entry{}
		retained := 0
		incomplete := false
		leaf := toolRef[strings.LastIndex(toolRef, "/")+1:]
		ids := map[string]bool{toolRef: true, leaf: true}
		maxRecord := int64(sessionstore.HistorySemanticRecordBytes)
		scan, err := server.reader.Stream(ctx, path, func(raw json.RawMessage, _ bool, _ int64) error {
			entry := historyRecord(raw)
			message := historyRecord(entry["message"])
			var blocks []json.RawMessage
			_ = json.Unmarshal(message["content"], &blocks)
			content := []json.RawMessage{}
			for _, raw := range blocks {
				block := historyRecord(raw)
				kind := stringField(block, "type")
				if kind == "tool_use" && ids[stringField(block, "id")] || kind == "tool_result" && ids[stringField(block, "tool_use_id")] {
					content = append(content, raw)
				}
			}
			notices := []historyObject{}
			if notice := taskNotification(entry); notice != nil && ids[stringField(notice, "toolUseId")] {
				notices = append(notices, notice)
			}
			var selected json.RawMessage
			if len(content) > 0 {
				message["content"], _ = json.Marshal(content)
				entry["message"], _ = json.Marshal(message)
				raw, _ := json.Marshal(entry)
				selected, err = sessionstore.DisplayPreview(raw)
				if err != nil {
					return err
				}
				if string(historyRecord(selected)["bodyTruncated"]) == "true" {
					incomplete = true
				}
			}
			selectedBytes := 0
			if selected != nil {
				value, err := protocol.ParseAndStringifyJavaScriptJSON(selected)
				if err != nil {
					return err
				}
				selectedBytes += len(value)
			}
			if len(notices) > 0 {
				size, err := subagentJSONBytes(notices)
				if err != nil {
					return err
				}
				selectedBytes += size
			}
			additional := len(notices)
			if selected != nil {
				additional++
			}
			if retained+selectedBytes > 2*1024*1024 || len(entries)+len(result.notifications)+additional > 2048 {
				incomplete = true
				return nil
			}
			retained += selectedBytes
			if selected != nil {
				entries = append(entries, sessionstore.Entry{Entry: selected})
			}
			result.notifications = append(result.notifications, notices...)
			return nil
		}, sessionstore.StreamOptions{MaxRecordBytes: &maxRecord})
		result.notifications = coalesceSubagentTaskUpdates(result.notifications)
		result.messages, _ = projectHistory(entries)
		result.historyComplete = !incomplete && scan.OmittedRecords == 0
		return err
	})
	if err != nil {
		return subagentTranscript{}, err
	}
	server.mu.Lock()
	if server.subagentLookups == nil {
		server.subagentLookups = map[string]subagentLookupCache{}
	}
	for i, value := range server.subagentLookupKeys {
		if value == key {
			server.subagentLookupKeys = append(server.subagentLookupKeys[:i], server.subagentLookupKeys[i+1:]...)
			break
		}
	}
	server.subagentLookups[key] = subagentLookupCache{version, result}
	server.subagentLookupKeys = append(server.subagentLookupKeys, key)
	for len(server.subagentLookupKeys) > 4 {
		oldest := server.subagentLookupKeys[0]
		server.subagentLookupKeys = server.subagentLookupKeys[1:]
		delete(server.subagentLookups, oldest)
	}
	server.mu.Unlock()
	return result, nil
}
func (server *Server) readSubagentTranscript(ctx context.Context, info sessionInfo, id, toolRef string) (subagentTranscript, error) {
	if agentIDHint(id) == "" {
		return emptySubagentTranscript(), nil
	}
	if toolRef != "" {
		return server.subagentLookup(ctx, info, toolRef, id)
	}
	path := subagentPath(info, id)
	stat, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return emptySubagentTranscript(), nil
	}
	if err != nil {
		return subagentTranscript{}, err
	}
	result := emptySubagentTranscript()
	if stat.Size() > 1536*1024 {
		page, err := server.reader.ReadPage(ctx, path, sessionstore.PageOptions{})
		if errors.Is(err, os.ErrNotExist) {
			return result, nil
		}
		if err != nil {
			return subagentTranscript{}, err
		}
		result.messages, result.notifications = projectHistory(page.Entries)
		result.historyComplete = page.Page.HistoryComplete
		return result, nil
	}
	err = server.budget.With(ctx, sessionstore.PageLane, func() error {
		entries := []sessionstore.Entry{}
		bytes := 0
		maxRecord := int64(sessionstore.HistorySemanticRecordBytes)
		scan, err := server.reader.Stream(ctx, path, func(raw json.RawMessage, _ bool, _ int64) error {
			value, err := protocol.ParseAndStringifyJavaScriptJSON(raw)
			if err != nil {
				return err
			}
			bytes += len(value)
			if bytes > 2*1024*1024 {
				return &apiError{413, "SUBAGENT_RECORD_LIMIT", "Agent transcript changed beyond its viewing budget"}
			}
			if len(entries) >= 10000 {
				return &apiError{413, "SUBAGENT_RECORD_LIMIT", "Agent transcript exceeds its record budget"}
			}
			entries = append(entries, sessionstore.Entry{Entry: raw})
			return nil
		}, sessionstore.StreamOptions{MaxRecordBytes: &maxRecord})
		result.messages, result.notifications = projectHistory(entries)
		result.historyComplete = scan.OmittedRecords == 0
		return err
	})
	if errors.Is(err, os.ErrNotExist) {
		return emptySubagentTranscript(), nil
	}
	return result, err
}
func subagentMetadata(path string) (historyObject, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 64*1024+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > 64*1024 {
		return nil, &apiError{413, "SUBAGENT_METADATA_LIMIT", "Agent metadata exceeds its viewing budget"}
	}
	var object historyObject
	if !json.Valid(raw) || strings.TrimSpace(string(raw)) == "null" {
		return nil, fmt.Errorf("invalid Agent metadata object")
	}
	if trimmed := strings.TrimSpace(string(raw)); !strings.HasPrefix(trimmed, "{") {
		// Source property reads on primitive/array JSON yield undefined; null
		// raises and invalidates the legacy uniqueness proof above.
		return historyObject{}, nil
	}
	err = json.Unmarshal(raw, &object)
	return object, err
}
func subagentMetadataFiles(info sessionInfo) ([]string, error) {
	entries, err := os.ReadDir(subagentDirectory(info))
	if err != nil {
		return []string{}, nil
	}
	if len(entries) > 4096 {
		return nil, &apiError{413, "SUBAGENT_METADATA_LIMIT", "Agent directory exceeds its viewing budget"}
	}
	names := []string{}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".meta.json") {
			names = append(names, entry.Name())
		}
	}
	return names, nil
}
func (server *Server) metadataAgentID(ctx context.Context, info sessionInfo, ref, owner string) (string, error) {
	names, err := subagentMetadataFiles(info)
	if err != nil {
		return "", err
	}
	type candidate struct{ id, owner string }
	candidates := []candidate{}
	complete := true
	for _, name := range names {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		metadata, err := subagentMetadata(filepath.Join(subagentDirectory(info), name))
		if err != nil {
			var api *apiError
			if errors.As(err, &api) {
				return "", err
			}
			complete = false
			continue
		}
		if stringField(metadata, "toolUseId") == ref {
			candidates = append(candidates, candidate{strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".meta.json"), stringField(metadata, "ownerAgentId")})
		}
	}
	exact := []candidate{}
	for _, candidate := range candidates {
		if candidate.owner == owner {
			exact = append(exact, candidate)
		}
	}
	if len(exact) == 1 {
		return exact[0].id, nil
	}
	if len(exact) > 1 {
		return "", nil
	}
	if owner != "" && complete && len(candidates) == 1 && candidates[0].owner == "" {
		return candidates[0].id, nil
	}
	return "", nil
}
func (server *Server) subagentFragments(ctx context.Context, info sessionInfo, agentType, toolRef string) ([]subagentFragment, error) {
	names, err := subagentMetadataFiles(info)
	if err != nil {
		return nil, err
	}
	fragments := []subagentFragment{}
	for _, name := range names {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		metadata, err := subagentMetadata(filepath.Join(subagentDirectory(info), name))
		if err != nil {
			var api *apiError
			if errors.As(err, &api) {
				return nil, err
			}
			continue
		}
		if stringField(metadata, "agentType") != agentType {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".meta.json")
		if agentIDHint(id) == "" {
			continue
		}
		transcript, err := server.readSubagentTranscript(ctx, info, id, toolRef)
		if err != nil {
			var own *apiError
			var history *sessionstore.APIError
			if errors.As(err, &own) || errors.As(err, &history) {
				return nil, err
			}
			continue
		}
		stat, err := os.Stat(subagentPath(info, id))
		if err != nil {
			continue
		}
		transcript.agentID = id
		fragments = append(fragments, subagentFragment{transcript, sourceMtimeMillis(stat)})
		serial := []map[string]any{}
		for _, fragment := range fragments {
			serial = append(serial, map[string]any{"agentId": fragment.agentID, "messages": fragment.messages, "taskNotifications": fragment.notifications, "historyComplete": fragment.historyComplete, "modifiedAt": fragment.modifiedAt})
		}
		size, err := subagentJSONBytes(serial)
		if err != nil {
			return nil, err
		}
		if len(fragments) > 16 || size > 4*1024*1024 {
			return nil, &apiError{413, "SUBAGENT_FRAGMENTS_LIMIT", "Agent fragments exceed their viewing budget"}
		}
	}
	comparator := collate.New(language.English)
	sort.SliceStable(fragments, func(i, j int) bool {
		if fragments[i].modifiedAt != fragments[j].modifiedAt {
			return fragments[i].modifiedAt < fragments[j].modifiedAt
		}
		return comparator.CompareString(fragments[i].agentID, fragments[j].agentID) < 0
	})
	return fragments, nil
}
func (server *Server) resolveSubagentTranscript(ctx context.Context, info sessionInfo, candidates ...string) (subagentTranscript, error) {
	seen := map[string]bool{}
	for _, candidate := range candidates {
		id := agentIDHint(candidate)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		transcript, err := server.readSubagentTranscript(ctx, info, id, "")
		if err != nil {
			return subagentTranscript{}, err
		}
		if len(transcript.messages) > 0 || len(transcript.notifications) > 0 || !transcript.historyComplete {
			transcript.agentID = id
			return transcript, nil
		}
	}
	return emptySubagentTranscript(), nil
}

// A resumed task reports completion again under the same task and tool identity.
// Distinct task ids remain separate so ambiguous reused tool refs stay guarded.
// This affects the read-only Activity projection, never the Harness transcript.
func coalesceSubagentTaskUpdates(notices []historyObject) []historyObject {
	result := make([]historyObject, 0, len(notices))
	positions := map[string]int{}
	for _, notice := range notices {
		key, _ := json.Marshal([]json.RawMessage{notice["ownerAgentId"], notice["toolUseId"], notice["taskId"]})
		identity := string(key)
		if at, found := positions[identity]; found {
			result[at] = notice
		} else {
			positions[identity] = len(result)
			result = append(result, notice)
		}
	}
	return result
}
