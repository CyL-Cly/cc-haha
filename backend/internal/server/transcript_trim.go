package server

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionrewind"
)

// Stop drains the shared application runtime, including tool and child futures.
// The transport's shared closing channel makes concurrent callers wait for the
// first Close even though the application itself has an idempotent fast path.
func (server *Server) stopSessionAndWait(ctx context.Context, id string) error {
	if err := server.StopTeamWorkers(ctx, id); err != nil {
		return err
	}
	server.mu.Lock()
	runtime := server.runtimes[id]
	server.mu.Unlock()
	if runtime == nil {
		return nil
	}
	if err := closeRuntime(ctx, runtime); err != nil {
		return err
	}
	server.mu.Lock()
	if server.runtimes[id] == runtime {
		delete(server.runtimes, id)
	}
	server.mu.Unlock()
	return nil
}

// Node readline recognizes CR, LF and CRLF. Retain each surviving original
// decoded line, with the Source writer's normalized trailing LF.
func sourceTranscriptLines(ctx context.Context, input io.Reader, consume func([]byte) error) error {
	reader := bufio.NewReader(input)
	buffer := make([]byte, 64*1024)
	line := []byte{}
	afterCR := false
	for {
		n, err := reader.Read(buffer)
		for _, character := range buffer[:n] {
			if err := ctx.Err(); err != nil {
				return err
			}
			switch character {
			case '\r':
				if err := consume(protocol.DecodeJavaScriptUTF8(line)); err != nil {
					return err
				}
				line = line[:0]
				afterCR = true
			case '\n':
				if !afterCR {
					if err := consume(protocol.DecodeJavaScriptUTF8(line)); err != nil {
						return err
					}
				}
				line = line[:0]
				afterCR = false
			default:
				line = append(line, character)
				afterCR = false
			}
		}
		if err != nil {
			if err != io.EOF {
				return err
			}
			break
		}
	}
	if len(line) > 0 {
		return consume(protocol.DecodeJavaScriptUTF8(line))
	}
	return nil
}
func (server *Server) trimSessionMessagesFrom(ctx context.Context, id, target string) (sessionrewind.TrimResult, error) {
	if server.options.PersistSession == nil {
		return sessionrewind.TrimResult{}, unsupported("rewind full settings persistence policy binding")
	}
	info, err := server.findSession(ctx, id)
	if err != nil {
		return sessionrewind.TrimResult{}, err
	}
	entries, _, _, err := server.rawEvidenceTranscript(ctx, info.Path)
	if err != nil {
		return sessionrewind.TrimResult{}, err
	}
	messages, _ := projectHistory(entries)
	start := -1
	for index, raw := range messages {
		if stringField(historyRecord(raw), "id") == target {
			start = index
			break
		}
	}
	if start < 0 {
		return sessionrewind.TrimResult{}, badRequest("Message not found in active session chain: " + target)
	}
	removed := []string{}
	removedSet, remainingSet := map[string]bool{}, map[string]bool{}
	for index, raw := range messages {
		messageID := stringField(historyRecord(raw), "id")
		if index < start {
			remainingSet[messageID] = true
		} else {
			removed = append(removed, messageID)
			removedSet[messageID] = true
		}
	}
	if len(removed) == 0 {
		return sessionrewind.TrimResult{RemovedMessageIDs: []string{}}, nil
	}
	input, err := os.Open(info.Path)
	if err != nil {
		return sessionrewind.TrimResult{}, err
	}
	defer input.Close()
	stat, err := input.Stat()
	if err != nil {
		return sessionrewind.TrimResult{}, err
	}
	token, err := uuid()
	if err != nil {
		return sessionrewind.TrimResult{}, err
	}
	temporary := info.Path + ".rewind-" + token + ".tmp"
	output, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, stat.Mode().Perm())
	if err != nil {
		return sessionrewind.TrimResult{}, err
	}
	defer func() { output.Close(); _ = os.Remove(temporary) }()
	err = sourceTranscriptLines(ctx, input, func(line []byte) error {
		keep := true
		if strings.TrimSpace(string(line)) != "" {
			entry := historyRecord(line)
			rawID := entry["uuid"]
			var messageID string
			if rawID != nil && string(rawID) != "null" && json.Unmarshal(rawID, &messageID) == nil {
				if removedSet[messageID] {
					keep = false
				} else if sourceJSONTruthy(historyRecord(entry["message"])["role"]) {
					if kind := stringField(entry, "type"); kind == "user" || kind == "assistant" || kind == "system" {
						keep = remainingSet[messageID]
					}
				}
			}
		}
		if !keep {
			return nil
		}
		if _, err := output.Write(line); err != nil {
			return err
		}
		_, err := output.Write([]byte{'\n'})
		return err
	})
	if err != nil {
		return sessionrewind.TrimResult{}, err
	}
	if err := output.Close(); err != nil {
		return sessionrewind.TrimResult{}, err
	}
	if err := input.Close(); err != nil {
		return sessionrewind.TrimResult{}, err
	}
	persist, err := server.options.PersistSession(ctx)
	if err != nil {
		return sessionrewind.TrimResult{}, err
	}
	if !persist {
		return sessionrewind.TrimResult{RemovedMessageIDs: []string{}}, nil
	}
	if err := ctx.Err(); err != nil {
		return sessionrewind.TrimResult{}, err
	}
	if err := os.Rename(temporary, info.Path); err != nil {
		return sessionrewind.TrimResult{}, err
	}
	server.noteSessionMutation(info.Path)
	server.invalidateRecentProjects()
	return sessionrewind.TrimResult{RemovedCount: len(removed), RemovedMessageIDs: removed}, nil
}
