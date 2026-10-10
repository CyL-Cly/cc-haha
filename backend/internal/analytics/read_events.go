package analytics

import (
	"path/filepath"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func Extension(path string) *string {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" || ext == "." {
		return nil
	}
	ext = ext[1:]
	if len(utf16.Encode([]rune(ext))) > 10 {
		ext = "other"
	}
	return &ext
}
func ReadDedupEvent(path string) Event {
	metadata := protocol.NewJSONObject()
	if ext := Extension(path); ext != nil {
		metadata.Set("ext", scalar(*ext))
	}
	return Event{Name: "tengu_file_read_dedup", Metadata: metadata}
}

type TextRead struct {
	Path, ConfigDir                                      string
	Content                                              []uint16
	TotalLines, ReadLines, TotalBytes, ReadBytes, Offset int
	Limit                                                *int
	MessageID                                            *string
}

func ReadEvents(input TextRead) []Event {
	metadata := protocol.NewJSONObject(field("totalLines", input.TotalLines), field("readLines", input.ReadLines), field("totalBytes", input.TotalBytes), field("readBytes", input.ReadBytes), field("offset", input.Offset))
	if input.Limit != nil {
		metadata.Set("limit", scalar(*input.Limit))
	}
	if ext := Extension(input.Path); ext != nil {
		metadata.Set("ext", scalar(*ext))
	}
	if input.MessageID != nil {
		metadata.Set("messageID", scalar(*input.MessageID))
	}
	inside := strings.HasPrefix(input.Path, input.ConfigDir)
	normalized := strings.ReplaceAll(input.Path, "\\", "/")
	metadata.Set("is_session_memory", scalar(inside && strings.Contains(normalized, "/session-memory/") && strings.HasSuffix(normalized, ".md")))
	metadata.Set("is_session_transcript", scalar(inside && strings.Contains(normalized, "/projects/") && strings.HasSuffix(normalized, ".jsonl")))
	return []Event{FileOperationEvent(FileOperation{Operation: "read", Tool: "FileReadTool", Path: utf16.Encode([]rune(input.Path)), Content: &input.Content}), {Name: "tengu_session_file_read", Metadata: metadata}}
}
