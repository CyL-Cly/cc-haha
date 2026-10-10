package teamreader

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/localindex"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamsapi"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

type TranscriptFile struct{ Path, OwnerAgentID string }
type transcriptBytes struct {
	Data         []byte
	Stat         localindex.FileSnapshot
	OwnerAgentID string
}

func readLegacyFiles(ctx context.Context, paths []TranscriptFile) (result []transcriptBytes, err error) {
	if len(paths) > 128 {
		return nil, &teamsapi.APIError{Status: 413, Code: "TEAM_TRANSCRIPT_TOO_LARGE", Message: "Too many transcript fragments for legacy history"}
	}
	handles := []*os.File{}
	defer func() {
		for _, handle := range handles {
			_ = handle.Close()
		}
	}()
	result = make([]transcriptBytes, 0, len(paths))
	total := int64(0)
	for _, source := range paths {
		if err = context.Cause(ctx); err != nil {
			return nil, err
		}
		handle, err := os.Open(source.Path)
		if err != nil {
			return nil, err
		}
		handles = append(handles, handle)
		stat, err := localindex.NativeFileSnapshot(handle)
		if err != nil {
			return nil, err
		}
		total += stat.Size
		if total > 8*1024*1024 {
			return nil, &teamsapi.APIError{Status: 413, Code: "TEAM_TRANSCRIPT_TOO_LARGE", Message: "Transcript exceeds the legacy history budget; use paged session history"}
		}
		result = append(result, transcriptBytes{Stat: stat, OwnerAgentID: source.OwnerAgentID})
	}
	for i, source := range result {
		data := make([]byte, int(source.Stat.Size))
		position := 0
		for position < len(data) {
			if err = context.Cause(ctx); err != nil {
				return nil, err
			}
			n, err := handles[i].ReadAt(data[position:position+min(128*1024, len(data)-position)], int64(position))
			if n == 0 {
				return nil, &teamsapi.APIError{Status: 409, Code: "HISTORY_CHANGED", Message: "Transcript changed while reading"}
			}
			if err != nil && !errors.Is(err, io.EOF) {
				return nil, err
			}
			position += n
		}
		result[i].Data = data
	}
	return result, nil
}
func entryIdentities(data []byte) []string {
	identities := []string{}
	for _, line := range strings.Split(string(protocol.DecodeJavaScriptUTF8(data)), "\n") {
		if protocol.TrimJavaScriptString(line) == "" {
			continue
		}
		value, err := protocol.ParseAndStringifyJavaScriptJSON([]byte(line))
		if err != nil {
			continue
		}
		entry := obj(value)
		// Deleting a property of null throws in the original. Its replacer
		// property list filters nested objects as well as the root object.
		if string(value) == "null" {
			continue
		}
		keys := []string{}
		if entry.IsObject() {
			for _, key := range []string{"agentId", "slug", "cwd", "promptId"} {
				entry.Delete(key)
			}
			value = raw(entry)
			for _, p := range entry.OwnProperties() {
				keys = append(keys, string(p.NameJSON))
			}
		} else if bytes.HasPrefix(value, []byte("[")) {
			var array []json.RawMessage
			_ = json.Unmarshal(value, &array)
			for i := range array {
				keys = append(keys, string(raw(strconv.Itoa(i))))
			}
		}
		sort.Slice(keys, func(i, j int) bool { return utf16Less([]byte(keys[i]), []byte(keys[j])) })
		identities = append(identities, hash(stringifyReplacer(value, keys)))
	}
	return identities
}

func stringifyReplacer(value json.RawMessage, keys []string) json.RawMessage {
	if bytes.HasPrefix(value, []byte("[")) {
		var array []json.RawMessage
		_ = json.Unmarshal(value, &array)
		for i := range array {
			array[i] = stringifyReplacer(array[i], keys)
		}
		return raw(array)
	}
	entry := obj(value)
	if !entry.IsObject() {
		return value
	}
	var out bytes.Buffer
	out.WriteByte('{')
	first := true
	for _, key := range keys {
		for _, property := range entry.OwnProperties() {
			if string(property.NameJSON) != key {
				continue
			}
			if !first {
				out.WriteByte(',')
			}
			first = false
			out.Write(property.NameJSON)
			out.WriteByte(':')
			out.Write(stringifyReplacer(property.Value, keys))
			break
		}
	}
	out.WriteByte('}')
	return out.Bytes()
}
func dropSuperseded(files []transcriptBytes) []transcriptBytes {
	if len(files) < 2 {
		return files
	}
	identities := [][]string{}
	for _, file := range files {
		identities = append(identities, entryIdentities(file.Data))
	}
	result := []transcriptBytes{}
	for i, file := range files {
		superseded := false
		for j, other := range identities {
			if i == j || len(identities[i]) >= len(other) {
				continue
			}
			prefix := true
			for k, id := range identities[i] {
				prefix = prefix && id == other[k]
			}
			if prefix {
				superseded = true
				break
			}
		}
		if !superseded {
			result = append(result, file)
		}
	}
	return result
}
func (s *Service) ParseTranscriptFiles(ctx context.Context, paths []TranscriptFile, options protocol.JSONObject) (protocol.JSONObject, error) {
	files, err := readLegacyFiles(ctx, paths)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if len(files) == 1 {
		return s.ParseTranscriptBuffer(files[0].Data, options, files[0].Stat.CtimeMs, files[0].Stat.FileIdentity, TranscriptProjection{OwnerAgentID: files[0].OwnerAgentID, OwnerAgentIDs: func() []string {
			if files[0].OwnerAgentID == "" {
				return []string{}
			}
			return []string{files[0].OwnerAgentID}
		}()}), nil
	}
	files = dropSuperseded(files)
	data := []byte{}
	byOrdinal := []string{}
	owners := []string{}
	seen := map[string]bool{}
	ctime := 0.0
	for i, file := range files {
		if file.Stat.CtimeMs > ctime {
			ctime = file.Stat.CtimeMs
		}
		for _, line := range strings.Split(string(protocol.DecodeJavaScriptUTF8(file.Data)), "\n") {
			if protocol.TrimJavaScriptString(line) == "" {
				continue
			}
			if _, err := protocol.ParseAndStringifyJavaScriptJSON([]byte(line)); err == nil {
				byOrdinal = append(byOrdinal, file.OwnerAgentID)
			}
		}
		data = append(data, file.Data...)
		if i < len(files)-1 {
			data = append(data, '\n')
		}
		if file.OwnerAgentID != "" && !seen[file.OwnerAgentID] {
			owners = append(owners, file.OwnerAgentID)
			seen[file.OwnerAgentID] = true
		}
	}
	return s.ParseTranscriptBuffer(data, options, ctime, nil, TranscriptProjection{OwnerByOrdinal: byOrdinal, OwnerAgentIDs: owners}), nil
}
