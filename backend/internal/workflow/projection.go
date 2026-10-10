package workflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

type TranscriptProjection struct {
	Messages      []json.RawMessage `json:"messages"`
	Complete      bool              `json:"complete"`
	SourceVersion string            `json:"sourceVersion"`
	ReadBytes     int64             `json:"readBytes"`
}
type cachedProjection struct {
	TranscriptProjection
	completeRecords                          bool
	identity, mtime, firstHash, boundaryHash string
	size, indexedBytes                       int64
	bytes                                    int
}
type projectionFuture struct {
	done   chan struct{}
	result TranscriptProjection
	err    error
}
type ProjectionReader struct {
	files   sessionstore.FileSystem
	budget  *sessionstore.Budget
	mu      sync.Mutex
	cache   map[string]cachedProjection
	order   []string
	pending map[string]*projectionFuture
}

func NewProjectionReader(files sessionstore.FileSystem, budget *sessionstore.Budget) *ProjectionReader {
	if files == nil {
		files = sessionstore.OSFileSystem{}
	}
	if budget == nil {
		panic("Workflow projection requires shared metadata admission")
	}
	return &ProjectionReader{files: files, budget: budget, cache: map[string]cachedProjection{}, pending: map[string]*projectionFuture{}}
}
func cloneProjection(value TranscriptProjection) TranscriptProjection {
	value.Messages = append([]json.RawMessage{}, value.Messages...)
	for i, raw := range value.Messages {
		value.Messages[i] = append(json.RawMessage(nil), raw...)
	}
	return value
}
func (r *ProjectionReader) Read(ctx context.Context, path string) (TranscriptProjection, error) {
	r.mu.Lock()
	if pending := r.pending[path]; pending != nil {
		r.mu.Unlock()
		select {
		case <-ctx.Done():
			return TranscriptProjection{}, ctx.Err()
		case <-pending.done:
			return cloneProjection(pending.result), pending.err
		}
	}
	future := &projectionFuture{done: make(chan struct{})}
	r.pending[path] = future
	r.mu.Unlock()
	err := r.budget.With(ctx, sessionstore.MetadataLane, func() error { var e error; future.result, e = r.read(ctx, path); return e })
	r.mu.Lock()
	future.err = err
	delete(r.pending, path)
	close(future.done)
	r.mu.Unlock()
	return cloneProjection(future.result), err
}
func identity(stat sessionstore.Snapshot) string { return stat.Dev + ":" + stat.Ino }
func changedProjection() error                   { return errors.New("Workflow transcript changed during projection") }
func projectionHash(file sessionstore.File, end int64) (string, error) {
	size := min(int64(4096), end)
	data := make([]byte, size)
	n, e := file.ReadAt(data, end-size)
	if n != len(data) || e != nil && e != io.EOF {
		return "", changedProjection()
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
func (r *ProjectionReader) remember(path string, state cachedProjection) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, key := range r.order {
		if key == path {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	r.cache[path] = state
	r.order = append(r.order, path)
	for len(r.order) > 4 {
		delete(r.cache, r.order[0])
		r.order = r.order[1:]
	}
}
func workflowRelevant(raw json.RawMessage) (bool, bool) {
	entry := jsonObject(raw)
	relevant := entry.String("type") == "cc-haha-task-notification"
	pending := []json.RawMessage{raw}
	nodes := 0
	for len(pending) > 0 && !relevant && nodes < 16384 {
		nodes++
		value := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if units, e := protocol.DecodeJavaScriptJSONString(value); e == nil {
			text := utf16Text(units)
			relevant = bytes.Contains([]byte(text), []byte("<task-notification>")) || bytes.Contains([]byte(text), []byte("local_workflow")) || bytes.Contains([]byte(text), []byte("async_launched"))
		} else if object := jsonObject(value); object.IsObject() {
			for _, field := range object.OwnProperties() {
				pending = append(pending, field.Value)
			}
		} else {
			var values []json.RawMessage
			if json.Unmarshal(value, &values) == nil {
				pending = append(pending, values...)
			}
		}
	}
	return relevant, len(pending) == 0 || relevant
}
func (r *ProjectionReader) read(ctx context.Context, path string) (result TranscriptProjection, err error) {
	file, e := r.files.Open(path)
	if e != nil {
		return result, e
	}
	defer func() {
		if e := file.Close(); e != nil {
			result = TranscriptProjection{}
			err = e
		}
	}()
	before, e := file.Stat()
	if e != nil {
		return result, e
	}
	r.mu.Lock()
	previous, exists := r.cache[path]
	r.mu.Unlock()
	id := identity(before)
	if exists && previous.identity == id && previous.size == before.Size && previous.mtime == before.MtimeNS {
		previous.ReadBytes = 0
		r.remember(path, previous)
		return previous.TranscriptProjection, nil
	}
	appendOnly := exists && previous.identity == id && before.Size > previous.size
	if appendOnly {
		first, e := projectionHash(file, min(int64(4096), previous.size))
		if e != nil {
			return result, e
		}
		boundary, e := projectionHash(file, previous.indexedBytes)
		if e != nil {
			return result, e
		}
		appendOnly = first == previous.firstHash && boundary == previous.boundaryHash
	}
	state := cachedProjection{TranscriptProjection: TranscriptProjection{Messages: []json.RawMessage{}, Complete: true, SourceVersion: fmt.Sprintf("%s:%d:%s", id, before.Size, before.MtimeNS)}, completeRecords: true, identity: id, size: before.Size, mtime: before.MtimeNS}
	if appendOnly {
		state.Messages = append([]json.RawMessage{}, previous.Messages...)
		state.Complete = previous.completeRecords
		state.completeRecords = previous.completeRecords
		state.bytes = previous.bytes
		state.indexedBytes = previous.indexedBytes
	}
	position := state.indexedBytes
	chunk := make([]byte, 128*1024)
	parts := [][]byte{}
	lineBytes := 0
	skipping := false
	for position < before.Size {
		if e := ctx.Err(); e != nil {
			return result, e
		}
		n, e := file.ReadAt(chunk[:min(int64(len(chunk)), before.Size-position)], position)
		if n == 0 || e != nil && e != io.EOF {
			return result, changedProjection()
		}
		state.ReadBytes += int64(n)
		start := 0
		for start < n {
			relative := bytes.IndexByte(chunk[start:n], 10)
			end := n
			if relative >= 0 {
				end = start + relative
			}
			lineBytes += end - start
			if lineBytes > 1024*1024 {
				skipping = true
				parts = nil
			}
			if !skipping {
				parts = append(parts, append([]byte(nil), chunk[start:end]...))
			}
			if end < n {
				if skipping {
					state.Complete = false
				} else if lineBytes > 0 {
					raw := bytes.Join(parts, nil)
					if !json.Valid(raw) {
						state.Complete = false
					} else {
						trimmed := bytes.TrimSpace(raw)
						if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
							relevant, complete := workflowRelevant(trimmed)
							if !complete {
								state.Complete = false
							}
							if relevant {
								serialized, e := protocol.ParseAndStringifyJavaScriptJSON(trimmed)
								if e != nil {
									return result, e
								}
								if state.bytes+len(serialized) > 3*1024*1024 || len(state.Messages) >= 10000 {
									state.Complete = false
								} else {
									state.bytes += len(serialized)
									state.Messages = append(state.Messages, serialized)
								}
							}
						}
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
	after, e := file.Stat()
	if e != nil {
		return result, e
	}
	current, e := r.files.Open(path)
	if e != nil {
		return result, e
	}
	now, e := current.Stat()
	closeErr := current.Close()
	if e != nil {
		return result, e
	}
	if closeErr != nil {
		return result, closeErr
	}
	if identity(after) != id || identity(now) != id || after.Size < before.Size || now.Size < before.Size || after.Size == before.Size && after.MtimeNS != before.MtimeNS || now.Size == before.Size && now.MtimeNS != before.MtimeNS {
		return result, changedProjection()
	}
	state.firstHash, e = projectionHash(file, min(int64(4096), before.Size))
	if e != nil {
		return result, e
	}
	state.boundaryHash, e = projectionHash(file, state.indexedBytes)
	if e != nil {
		return result, e
	}
	r.remember(path, state)
	return state.TranscriptProjection, nil
}
