package teamreader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/localindex"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamsapi"
)

type TeamProjection struct {
	Messages      []protocol.JSONObject
	Complete      bool
	SourceVersion string
	ReadBytes     int64
}
type teamProjectionState struct {
	TeamProjection
	completeRecords, teamSeen                bool
	identity, mtime, firstHash, boundaryHash string
	size, indexedBytes, bytes                int64
	toolIDs                                  map[string]bool
}
type projectionCall struct {
	done  chan struct{}
	value TeamProjection
	err   error
}

func cloneMessages(messages []protocol.JSONObject) []protocol.JSONObject {
	out := make([]protocol.JSONObject, len(messages))
	for i, message := range messages {
		out[i] = message.Clone()
	}
	return out
}
func cloneProjection(value TeamProjection) TeamProjection {
	value.Messages = cloneMessages(value.Messages)
	return value
}
func nanosecondsText(info os.FileInfo) string {
	value := new(big.Int).Mul(big.NewInt(info.ModTime().Unix()), big.NewInt(1_000_000_000))
	return value.Add(value, big.NewInt(int64(info.ModTime().Nanosecond()))).String()
}
func (s *Service) ReadTeamProjection(ctx context.Context, path string) (TeamProjection, error) {
	s.projectionMu.Lock()
	existing := s.projectionPending[path]
	if existing != nil {
		s.projectionMu.Unlock()
		select {
		case <-existing.done:
			return cloneProjection(existing.value), existing.err
		case <-ctx.Done():
			return TeamProjection{}, context.Cause(ctx)
		}
	}
	call := &projectionCall{done: make(chan struct{})}
	s.projectionPending[path] = call
	s.projectionMu.Unlock()
	run := func() error { var err error; call.value, err = s.readTeamProjection(ctx, path); return err }
	if s.options.HistoryBudget == nil {
		call.err = &teamsapi.CapabilityError{Boundary: "shared Team history projection admission budget"}
	} else {
		call.err = s.options.HistoryBudget.With(ctx, sessionstore.MetadataLane, run)
	}
	s.projectionMu.Lock()
	delete(s.projectionPending, path)
	close(call.done)
	s.projectionMu.Unlock()
	return cloneProjection(call.value), call.err
}
func (s *Service) retainProjection(path string, state *teamProjectionState) {
	s.projectionMu.Lock()
	defer s.projectionMu.Unlock()
	for i, existing := range s.projectionOrder {
		if existing == path {
			s.projectionOrder = append(s.projectionOrder[:i], s.projectionOrder[i+1:]...)
			break
		}
	}
	s.projectionOrder = append(s.projectionOrder, path)
	s.projections[path] = state
	for len(s.projectionOrder) > 4 {
		delete(s.projections, s.projectionOrder[0])
		s.projectionOrder = s.projectionOrder[1:]
	}
}
func windowHash(file *os.File, end int64) (string, error) {
	length := min(int64(4096), end)
	data := make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(io.NewSectionReader(file, end-length, length), data); err != nil {
			return "", errors.New("Team transcript changed during projection")
		}
	}
	return hash(data), nil
}
func (s *Service) readTeamProjection(ctx context.Context, path string) (TeamProjection, error) {
	file, err := os.Open(path)
	if err != nil {
		return TeamProjection{}, err
	}
	defer file.Close()
	before, err := localindex.NativeFileSnapshot(file)
	if err != nil {
		return TeamProjection{}, err
	}
	info, err := file.Stat()
	if err != nil {
		return TeamProjection{}, err
	}
	if before.FileIdentity == nil {
		return TeamProjection{}, &teamsapi.CapabilityError{Boundary: "native bigint transcript identity"}
	}
	identity, mtime := *before.FileIdentity, nanosecondsText(info)
	s.projectionMu.Lock()
	previous := s.projections[path]
	s.projectionMu.Unlock()
	if previous != nil && previous.identity == identity && previous.size == before.Size && previous.mtime == mtime {
		copy := cloneProjection(previous.TeamProjection)
		copy.ReadBytes = 0
		s.retainProjection(path, previous)
		return copy, nil
	}
	canAppend := false
	if previous != nil && previous.identity == identity && before.Size > previous.size {
		first, err := windowHash(file, min(4096, previous.size))
		if err != nil {
			return TeamProjection{}, err
		}
		boundary, err := windowHash(file, previous.indexedBytes)
		if err != nil {
			return TeamProjection{}, err
		}
		canAppend = first == previous.firstHash && boundary == previous.boundaryHash
	}
	state := &teamProjectionState{TeamProjection: TeamProjection{Messages: []protocol.JSONObject{}, Complete: true, SourceVersion: fmt.Sprintf("%s:%d:%s", identity, before.Size, mtime)}, completeRecords: true, identity: identity, mtime: mtime, size: before.Size, toolIDs: map[string]bool{}}
	if canAppend {
		state.Messages = cloneMessages(previous.Messages)
		state.Complete = previous.completeRecords
		state.completeRecords = previous.completeRecords
		state.teamSeen = previous.teamSeen
		state.bytes = previous.bytes
		state.indexedBytes = previous.indexedBytes
		for id := range previous.toolIDs {
			state.toolIDs[id] = true
		}
	}
	position := state.indexedBytes
	parts := []byte{}
	lineBytes := 0
	skipping := false
	chunk := make([]byte, 128*1024)
	for position < before.Size {
		if err := context.Cause(ctx); err != nil {
			return TeamProjection{}, err
		}
		n, err := file.ReadAt(chunk[:min(int64(len(chunk)), before.Size-position)], position)
		if n == 0 {
			return TeamProjection{}, errors.New("Team transcript changed during projection")
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return TeamProjection{}, err
		}
		state.ReadBytes += int64(n)
		start := 0
		for start < n {
			newline := bytes.IndexByte(chunk[start:n], '\n')
			end := n
			if newline >= 0 {
				end = start + newline
			}
			lineBytes += end - start
			if lineBytes > 1024*1024 {
				skipping = true
				parts = nil
			}
			if !skipping {
				parts = append(parts, chunk[start:end]...)
			}
			if end < n {
				if skipping {
					state.Complete = false
				} else if lineBytes > 0 {
					entry, err := protocol.ParseAndStringifyJavaScriptJSON(protocol.DecodeJavaScriptUTF8(parts))
					if err != nil {
						state.Complete = false
					} else if object := obj(entry); object.IsObject() {
						collectTeamEvidence(object, state)
					}
				}
				state.indexedBytes = position + int64(end) + 1
				parts = nil
				lineBytes = 0
				skipping = false
			}
			start = end + 1
		}
		position += int64(n)
	}
	state.completeRecords = state.Complete
	if lineBytes > 0 {
		state.Complete = false
	}
	after, err := localindex.NativeFileSnapshot(file)
	if err != nil {
		return TeamProjection{}, err
	}
	current, err := localindex.SnapshotPath(path)
	if err != nil {
		return TeamProjection{}, err
	}
	afterInfo, err := file.Stat()
	if err != nil {
		return TeamProjection{}, err
	}
	currentInfo, err := os.Stat(path)
	if err != nil {
		return TeamProjection{}, err
	}
	if after.FileIdentity == nil || current.FileIdentity == nil || *after.FileIdentity != identity || *current.FileIdentity != identity || after.Size < before.Size || current.Size < before.Size || after.Size == before.Size && nanosecondsText(afterInfo) != mtime || current.Size == before.Size && nanosecondsText(currentInfo) != mtime {
		return TeamProjection{}, errors.New("Team transcript changed during projection")
	}
	state.firstHash, err = windowHash(file, min(4096, before.Size))
	if err != nil {
		return TeamProjection{}, err
	}
	state.boundaryHash, err = windowHash(file, state.indexedBytes)
	if err != nil {
		return TeamProjection{}, err
	}
	s.retainProjection(path, state)
	return cloneProjection(state.TeamProjection), nil
}
func collectTeamEvidence(entry protocol.JSONObject, state *teamProjectionState) {
	if state.bytes >= 3*1024*1024 || len(state.Messages) >= 10000 {
		state.Complete = false
		return
	}
	message := obj(entry.Get("message"))
	if !message.IsObject() {
		return
	}
	blocks := rows(message.Get("content"))
	for _, block := range blocks {
		if block.String("type") == "tool_use" && block.String("name") == "TeamCreate" {
			state.teamSeen = true
		}
	}
	tools, results := []protocol.JSONObject{}, []protocol.JSONObject{}
	for _, block := range blocks {
		if block.String("type") == "tool_use" && state.teamSeen {
			switch block.String("name") {
			case "TeamCreate", "TeamDelete", "Agent", "TaskCreate", "TaskUpdate", "TaskList", "SendMessage":
				tools = append(tools, block)
				if stringRaw(block.Get("id")) {
					state.toolIDs[block.String("id")] = true
				}
			}
		}
	}
	for _, block := range blocks {
		if block.String("type") == "tool_result" && state.toolIDs[block.String("tool_use_id")] {
			results = append(results, block)
		}
	}
	text := transcriptText(message.Get("content"))
	teammate := message.String("role") == "user" && strings.Contains(text, "<teammate-message")
	var content []byte
	typ := "user"
	if len(tools) > 0 {
		content = raw(tools)
		typ = "tool_use"
	} else if len(results) > 0 {
		content = raw(results)
		typ = "tool_result"
	} else if teammate {
		content = message.Get("content")
	} else {
		return
	}
	id := entry.Get("uuid")
	if !stringRaw(id) {
		id = raw(fmt.Sprintf("team:%d:%d", state.indexedBytes, len(state.Messages)))
	}
	timestamp := entry.Get("timestamp")
	if !stringRaw(timestamp) {
		timestamp = raw("1970-01-01T00:00:00.000Z")
	}
	projected := protocol.NewJSONObject(protocol.JSONField{Name: "id", Value: id}, field("type", typ), protocol.JSONField{Name: "content", Value: content}, protocol.JSONField{Name: "timestamp", Value: timestamp})
	if stringRaw(entry.Get("cwd")) {
		projected.Set("cwd", entry.Get("cwd"))
	}
	projected.Set("toolUseResult", entry.Get("toolUseResult"))
	if stringRaw(entry.Get("parent_tool_use_id")) {
		projected.Set("parentToolUseId", entry.Get("parent_tool_use_id"))
	}
	length := int64(len(protocol.StringifyValidatedJavaScriptJSON(raw(projected))))
	if state.bytes+length > 3*1024*1024 {
		state.Complete = false
		state.bytes = 3 * 1024 * 1024
		return
	}
	state.bytes += length
	state.Messages = append(state.Messages, projected)
}
func (s *Service) readTeamHistory(ctx context.Context, id string) ([]protocol.JSONObject, error) {
	path := ""
	var err error
	if s.options.FindSessionFile != nil {
		path, err = s.options.FindSessionFile(ctx, id)
		if err != nil {
			return nil, err
		}
	} else {
		path = s.findTranscript(id)
	}
	if path == "" {
		return []protocol.JSONObject{}, nil
	}
	projection, err := s.ReadTeamProjection(ctx, path)
	if err != nil {
		return nil, &teamsapi.APIError{Status: 503, Code: "TEAM_HISTORY_INCOMPLETE", Message: "Team history is changing; retry shortly"}
	}
	if !projection.Complete {
		return nil, &teamsapi.APIError{Status: 503, Code: "TEAM_HISTORY_INCOMPLETE", Message: "Team history projection is incomplete; existing workbench state was preserved"}
	}
	return projection.Messages, nil
}
