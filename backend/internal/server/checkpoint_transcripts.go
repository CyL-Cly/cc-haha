package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"runtime"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

// Source's rewind evidence reader retains the actual records. A display page
// cannot stand in for this read: it can hide a failed or nested tool result.
func (server *Server) rawEvidenceTranscript(ctx context.Context, path string) ([]sessionstore.Entry, bool, bool, error) {
	entries := []sessionstore.Entry{}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return entries, false, false, nil
	}
	if err != nil {
		return nil, false, false, err
	}
	defer file.Close()
	complete := true
	err = sourceTranscriptLines(ctx, file, func(line []byte) error {
		raw := json.RawMessage(trimJS(string(line)))
		if len(raw) == 0 {
			return nil
		}
		if !json.Valid(raw) {
			complete = false
			return nil
		}
		if raw[0] == '{' {
			entries = append(entries, sessionstore.Entry{Entry: append(json.RawMessage(nil), raw...)})
		}
		return nil
	})
	return entries, true, complete, err
}

type checkpointAgentLink struct {
	parent, id string
	depth      int
	ancestry   map[string]bool
}

var checkpointAgentID = regexp.MustCompile(`(?:^|\n)` + subagentJSSpace + `*agentId:` + subagentJSSpace + `*([A-Za-z0-9_-]+)`)

func checkpointResultText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	values := []string{}
	for _, block := range contentBlocks(raw) {
		var text string
		if value := block["text"]; len(value) > 0 && string(value) != "null" && json.Unmarshal(value, &text) == nil {
			values = append(values, text)
		}
	}
	return strings.Join(values, "\n")
}
func checkpointAgentLinks(messages []json.RawMessage) ([]checkpointAgentLink, bool) {
	toolIDs := []string{}
	links := map[string]string{}
	order := []string{}
	for _, raw := range messages {
		entry := historyRecord(raw)
		for _, block := range contentBlocks(entry["content"]) {
			if stringField(entry, "type") == "tool_use" && stringField(block, "type") == "tool_use" && (stringField(block, "name") == "Agent" || stringField(block, "name") == "Task") {
				if id := stringField(block, "id"); id != "" {
					toolIDs = append(toolIDs, id)
				}
			}
		}
	}
	toolSet := map[string]bool{}
	for _, id := range toolIDs {
		toolSet[id] = true
	}
	for _, raw := range messages {
		entry := historyRecord(raw)
		if stringField(entry, "type") != "tool_result" {
			continue
		}
		for _, block := range contentBlocks(entry["content"]) {
			if stringField(block, "type") != "tool_result" {
				continue
			}
			id := stringField(block, "tool_use_id")
			if !toolSet[id] {
				continue
			}
			if match := checkpointAgentID.FindStringSubmatch(checkpointResultText(block["content"])); match != nil {
				if _, exists := links[id]; !exists {
					order = append(order, id)
				}
				links[id] = match[1]
			}
		}
	}
	complete := true
	for _, id := range toolIDs {
		if _, found := links[id]; !found {
			complete = false
		}
	}
	result := []checkpointAgentLink{}
	for _, parent := range order {
		result = append(result, checkpointAgentLink{parent: parent, id: links[parent], depth: 1, ancestry: map[string]bool{}})
	}
	return result, complete
}
func namespaceCheckpointToolMessages(entries []sessionstore.Entry, parent, id string) []json.RawMessage {
	result := []json.RawMessage{}
	namespace := parent + "/" + id
	for _, item := range entries {
		entry := historyRecord(item.Entry)
		if string(entry["isMeta"]) == "true" {
			continue
		}
		message := historyRecord(entry["message"])
		var content []json.RawMessage
		if json.Unmarshal(message["content"], &content) == nil {
			for i, raw := range content {
				block := historyRecord(raw)
				field := ""
				if stringField(block, "type") == "tool_use" {
					field = "id"
				} else if stringField(block, "type") == "tool_result" {
					field = "tool_use_id"
				}
				if rawID := stringField(block, field); field != "" && rawID != "" {
					block[field] = jsonString(namespace + "/" + rawID)
					block["original_tool_use_id"] = jsonString(rawID)
					content[i], _ = json.Marshal(block)
				}
			}
			message["content"], _ = json.Marshal(content)
			entry["message"], _ = json.Marshal(message)
		}
		projected, ok := messageEntryWithOwner(entry, parent)
		if !ok {
			continue
		}
		if kind := stringField(historyRecord(projected), "type"); kind == "tool_use" || kind == "tool_result" {
			result = append(result, projected)
		}
	}
	return result
}
func (server *Server) appendCheckpointChildren(ctx context.Context, info sessionInfo, messages []json.RawMessage) ([]json.RawMessage, bool, error) {
	pending, complete := checkpointAgentLinks(messages)
	all := append([]json.RawMessage{}, messages...)
	loaded := map[string]bool{}
	transcriptCount, messageCount := 0, 0
	for len(pending) > 0 {
		batch := pending
		pending = nil
		for _, link := range batch {
			if err := ctx.Err(); err != nil {
				return nil, false, err
			}
			identity := subagentPath(info, link.id)
			if runtime.GOOS == "windows" {
				identity = strings.ToLower(identity)
			}
			if link.ancestry[identity] {
				continue
			}
			if link.depth > 16 || transcriptCount >= 128 {
				complete = false
				continue
			}
			key := link.parent + "\x00" + link.id
			if loaded[key] {
				continue
			}
			loaded[key] = true
			transcriptCount++
			entries, exists, parseComplete, err := server.rawEvidenceTranscript(ctx, subagentPath(info, link.id))
			if err != nil {
				return nil, false, err
			}
			if !exists || !parseComplete {
				complete = false
			}
			child := namespaceCheckpointToolMessages(entries, link.parent, link.id)
			remaining := 20000 - messageCount
			if remaining <= 0 {
				complete = false
				break
			}
			if len(child) > remaining {
				child = child[:remaining]
				complete = false
			}
			messageCount += len(child)
			all = append(all, child...)
			ancestry := map[string]bool{}
			for key, value := range link.ancestry {
				ancestry[key] = value
			}
			ancestry[identity] = true
			children, childComplete := checkpointAgentLinks(child)
			if !childComplete {
				complete = false
			}
			for _, next := range children {
				next.depth = link.depth + 1
				next.ancestry = ancestry
				pending = append(pending, next)
			}
		}
	}
	return all, complete, nil
}
