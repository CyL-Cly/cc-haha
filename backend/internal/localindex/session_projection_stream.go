package localindex

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionmetadata"
)

func skeletonSelected(path []string) bool {
	if len(path) == 0 {
		return true
	}
	switch path[0] {
	case "repository", "worktreeSession", "forkedFrom":
		return true
	case "message":
		if len(path) == 1 {
			return true
		}
		switch path[1] {
		case "role", "id", "model":
			return len(path) == 2
		case "usage":
			return true
		case "content":
			if len(path) <= 3 {
				return true
			}
			if !containsString([]string{"type", "name", "id", "tool_use_id", "text", "input"}, path[3]) {
				return false
			}
			if len(path) == 4 {
				return true
			}
			return path[3] == "input" && len(path) == 5 && (path[4] == "skill" || path[4] == "command")
		}
		return false
	default:
		return len(path) == 1 && containsString([]string{"type", "subtype", "content", "isMeta", "isSidechain", "entrypoint", "timestamp", "cwd", "workDir", "permissionMode", "runtimeProviderId", "runtimeModelId", "effortLevel", "openAIFastMode", "customTitle", "aiTitle", "uuid", "parentUuid", "messageId", "parent_tool_use_id", "requestId", "version", "sessionId", "timeSavedMs"}, path[0])
	}
}
func containsString(values []string, value string) bool {
	for _, v := range values {
		if value == v {
			return true
		}
	}
	return false
}
func skeletonPreview(path []string) bool {
	if len(path) == 0 {
		return true
	}
	if len(path) == 1 {
		return path[0] == "content"
	}
	if path[0] != "message" || path[1] != "content" {
		return false
	}
	return len(path) == 2 || len(path) == 3 || len(path) == 4 && (path[3] == "text" || path[3] == "input") || len(path) == 5 && path[3] == "input" && path[4] == "command"
}
func (p *SessionProjector) build(ctx context.Context, c SessionCandidate, start int64, seed *TranscriptProjection, subagent bool, cached retainedProjection) (retainedProjection, []EntryLocator, *Fingerprint, ProjectionWork, error) {
	work := ProjectionWork{}
	before, e := CaptureFingerprint(ctx, c.Path, start, p.o.ParserVersion)
	if e != nil {
		return retainedProjection{}, nil, nil, work, e
	}
	if e = p.active(ctx); e != nil {
		return retainedProjection{}, nil, nil, work, e
	}
	if start == 0 {
		metadata, e := p.o.SourceMetadata(c.Path)
		if e != nil {
			return retainedProjection{}, nil, nil, work, e
		}
		if !FingerprintMatchesPath(c.Path, *before) {
			return retainedProjection{}, nil, nil, work, errSourceChanged
		}
		candidate := c
		candidate.FallbackCreatedAt = metadata.BirthtimeISO
		candidate.FallbackModifiedAt = metadata.MtimeISO
		seed = initialSessionProjection(candidate)
		cached = retainedProjection{}
	}
	file, e := os.Open(c.Path)
	if e != nil {
		return retainedProjection{}, nil, nil, work, e
	}
	defer file.Close()
	if start > before.Size || !FingerprintMatchesFile(file, *before) {
		return retainedProjection{}, nil, nil, work, errSourceChanged
	}
	position, lineStart := start, start
	pending := []byte{}
	chunks := []TranscriptChunk{}
	chunkBytes := 0
	var oversized *sessionmetadata.BoundedStream
	oversizedBytes := int64(0)
	records, metadataBytes := cached.records, cached.metadataBytes
	projection := seed
	locators := []EntryLocator{}
	o := p.o.Reduction
	o.IsSubagent = &subagent
	o.ValidateRetainedMetadata = func(entry protocol.JSONObject) error { return retainedMetadata(entry, &metadataBytes) }
	flush := func() error {
		if len(chunks) == 0 {
			return nil
		}
		work.MaxBufferedChunks = max(work.MaxBufferedChunks, len(chunks))
		work.MaxBufferedBytes = max(work.MaxBufferedBytes, chunkBytes)
		reduced, entries, e := ReduceTranscript(chunks, projection, o)
		if e != nil {
			return e
		}
		projection = reduced
		locators = append(locators, entries...)
		chunks = nil
		chunkBytes = 0
		return nil
	}
	discard := func() {
		if oversized != nil {
			oversized.Discard()
			oversized = nil
		}
	}
	defer discard()
	buffer := make([]byte, 64*1024)
	for position < before.Size {
		if e = p.active(ctx); e != nil {
			return retainedProjection{}, nil, nil, work, e
		}
		requested := min(int64(len(buffer)), before.Size-position)
		n, readErr := file.ReadAt(buffer[:requested], position)
		if readErr != nil && readErr != io.EOF {
			return retainedProjection{}, nil, nil, work, readErr
		}
		if n == 0 {
			return retainedProjection{}, nil, nil, work, errSourceChanged
		}
		part := buffer[:n]
		cursor := 0
		for cursor < len(part) {
			newline := bytes.IndexByte(part[cursor:], '\n')
			end := len(part)
			complete := newline >= 0
			if complete {
				end = cursor + newline + 1
			}
			segment := part[cursor:end]
			if oversized == nil && len(pending)+len(segment) > p.o.RecordStreamingThreshold {
				oversized, e = sessionmetadata.NewBoundedStream(sessionmetadata.BoundedProfile{Selected: skeletonSelected, Preview: skeletonPreview, MetadataBytes: 1024 * 1024, PreviewChars: 4096, LimitError: sourceLimitError})
				if e != nil {
					return retainedProjection{}, nil, nil, work, e
				}
				if e = oversized.Write(pending); e != nil {
					return retainedProjection{}, nil, nil, work, e
				}
				oversizedBytes = int64(len(pending))
				pending = nil
			}
			if !complete {
				if oversized != nil {
					if e = oversized.Write(segment); e != nil {
						return retainedProjection{}, nil, nil, work, e
					}
					oversizedBytes += int64(len(segment))
				} else {
					pending = append(pending, segment...)
					work.MaxBufferedBytes = max(work.MaxBufferedBytes, chunkBytes+len(pending))
				}
				break
			}
			records++
			if records > MaxProjectionRecords {
				return retainedProjection{}, nil, nil, work, sourceLimitError()
			}
			var text string
			var recordBytes int64
			charge := 0
			if oversized != nil {
				if e = oversized.Write(segment); e != nil {
					return retainedProjection{}, nil, nil, work, e
				}
				recordBytes = oversizedBytes + int64(len(segment))
				raw, e := oversized.Finish()
				if e != nil {
					return retainedProjection{}, nil, nil, work, e
				}
				oversized = nil
				oversizedBytes = 0
				text = string(raw)
				charge = len(utf16.Encode([]rune(text)))
			} else {
				pending = append(pending, segment...)
				recordBytes = int64(len(pending))
				text = string(protocol.DecodeJavaScriptUTF8(pending))
				charge = len(pending)
				pending = nil
			}
			bytes := recordBytes
			chunks = append(chunks, TranscriptChunk{Text: text, ByteStart: lineStart, ByteLength: &bytes, CompleteLine: true})
			chunkBytes += charge
			lineStart += recordBytes
			work.MaxBufferedChunks = max(work.MaxBufferedChunks, len(chunks))
			work.MaxBufferedBytes = max(work.MaxBufferedBytes, chunkBytes)
			cursor = end
			if len(chunks) >= 256 || chunkBytes >= 1024*1024 {
				if e = flush(); e != nil {
					return retainedProjection{}, nil, nil, work, e
				}
			}
		}
		position += int64(n)
	}
	if e = flush(); e != nil {
		return retainedProjection{}, nil, nil, work, e
	}
	if oversized != nil {
		oversized.Discard()
		oversized = nil
		length := oversizedBytes
		reduced, _, e := ReduceTranscript([]TranscriptChunk{{Text: "", ByteStart: lineStart, ByteLength: &length, CompleteLine: false}}, projection, o)
		if e != nil {
			return retainedProjection{}, nil, nil, work, e
		}
		projection = reduced
	} else if len(pending) > 0 {
		work.MaxBufferedChunks = max(work.MaxBufferedChunks, 1)
		work.MaxBufferedBytes = max(work.MaxBufferedBytes, len(pending))
		length := int64(len(pending))
		reduced, _, e := ReduceTranscript([]TranscriptChunk{{Text: string(protocol.DecodeJavaScriptUTF8(pending)), ByteStart: lineStart, ByteLength: &length, CompleteLine: false}}, projection, o)
		if e != nil {
			return retainedProjection{}, nil, nil, work, e
		}
		projection = reduced
	}
	if !FingerprintMatchesFile(file, *before) || !FingerprintMatchesPath(c.Path, *before) {
		return retainedProjection{}, nil, nil, work, errSourceChanged
	}
	if e = p.active(ctx); e != nil {
		return retainedProjection{}, nil, nil, work, e
	}
	commit, e := CaptureFingerprint(ctx, c.Path, projection.IndexedBytes, p.o.ParserVersion)
	if e != nil {
		return retainedProjection{}, nil, nil, work, e
	}
	if !sameSnapshot(FileSnapshot{before.Size, before.MtimeMs, before.CtimeMs, before.FileIdentity}, FileSnapshot{commit.Size, commit.MtimeMs, commit.CtimeMs, commit.FileIdentity}) || before.FirstWindowHash != commit.FirstWindowHash || before.LastWindowHash != commit.LastWindowHash {
		return retainedProjection{}, nil, nil, work, errSourceChanged
	}
	if e = p.active(ctx); e != nil {
		return retainedProjection{}, nil, nil, work, e
	}
	if projection == nil {
		return retainedProjection{}, nil, nil, work, errors.New("Session projection unavailable")
	}
	return retainedProjection{projection, records, metadataBytes}, locators, commit, work, nil
}
