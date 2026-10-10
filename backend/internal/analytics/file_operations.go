package analytics

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Event struct {
	Name     string               `json:"name"`
	Metadata protocol.JSONObject  `json:"metadata"`
	Context  *protocol.JSONObject `json:"-"` // same-owner attribution, never event metadata
}
type FileOperation struct {
	Operation, Tool string
	Path            []uint16
	Content         *[]uint16
	Type            *string
}

func scalar(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }
func field(name string, value any) protocol.JSONField {
	return protocol.JSONField{Name: name, Value: scalar(value)}
}
func hash(units []uint16) string {
	sum := sha256.Sum256(utf8Bytes(units))
	return hex.EncodeToString(sum[:])
}

// FileOperationEvent preserves JS UTF16 length and Node UTF8 replacement before
// hashing. Paths and file contents themselves never enter the event payload.
func FileOperationEvent(input FileOperation) Event {
	metadata := protocol.NewJSONObject(field("operation", input.Operation), field("tool", input.Tool), field("filePathHash", hash(input.Path)[:16]))
	if input.Content != nil && len(*input.Content) <= 100*1024 {
		metadata.Set("contentHash", scalar(hash(*input.Content)))
	}
	if input.Type != nil {
		metadata.Set("type", scalar(*input.Type))
	}
	return Event{Name: "tengu_file_operation", Metadata: metadata}
}

func WriteEvents(path string, kind string) []Event {
	events := []Event{}
	if strings.HasSuffix(path, string(filepath.Separator)+"CLAUDE.md") {
		events = append(events, Event{Name: "tengu_write_claudemd", Metadata: protocol.NewJSONObject()})
	}
	events = append(events, FileOperationEvent(FileOperation{Operation: "write", Tool: "FileWriteTool", Path: utf16.Encode([]rune(path)), Type: &kind}))
	return events
}

func EditEvents(path string, old, replacement []uint16, replaceAll bool) []Event {
	events := []Event{}
	if strings.HasSuffix(path, string(filepath.Separator)+"CLAUDE.md") {
		events = append(events, Event{Name: "tengu_write_claudemd", Metadata: protocol.NewJSONObject()})
	}
	events = append(events, FileOperationEvent(FileOperation{Operation: "edit", Tool: "FileEditTool", Path: utf16.Encode([]rune(path))}), Event{Name: "tengu_edit_string_lengths", Metadata: protocol.NewJSONObject(field("oldStringBytes", len(utf8Bytes(old))), field("newStringBytes", len(utf8Bytes(replacement))), field("replaceAll", replaceAll))})
	return events
}

func StripProtoFields(metadata protocol.JSONObject) protocol.JSONObject {
	result := metadata.Clone()
	for _, property := range metadata.OwnProperties() {
		var name string
		_ = json.Unmarshal(property.NameJSON, &name)
		if strings.HasPrefix(name, "_PROTO_") {
			_ = result.DeletePropertyJSON(property.NameJSON)
		}
	}
	return result
}

func utf8Bytes(units []uint16) []byte { return []byte(string(utf16.Decode(units))) }

// Sample follows shouldSampleEvent: nil means no sample_rate field, zero means
// drop. The randomness belongs to the process primitive, not a model outcome.
func Sample(rate *float64, random func() float64) *float64 {
	if rate == nil || *rate < 0 || *rate > 1 || *rate >= 1 {
		return nil
	}
	if *rate <= 0 {
		zero := 0.0
		return &zero
	}
	if random() < *rate {
		value := *rate
		return &value
	}
	zero := 0.0
	return &zero
}
