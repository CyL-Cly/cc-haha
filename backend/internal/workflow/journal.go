package workflow

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Journal serializes the original append-only resume log. It owns only native
// file I/O; agent state, scheduling and cancellation remain on the shared host.
type Journal struct {
	path           string
	files          JournalFiles
	mu             sync.Mutex
	queue          []*journalWrite
	running        bool
	last           *journalWrite
	chainError     error
	directoryReady bool
}
type journalWrite struct {
	line []byte
	done chan struct{}
	err  error
}
type JournalFiles struct {
	ReadFile   func(string) ([]byte, error)
	MkdirAll   func(string) error
	AppendFile func(string, []byte) error
}
type JournalAppend struct{ write *journalWrite }

func (a *JournalAppend) Done() <-chan struct{} { return a.write.done }
func (a *JournalAppend) Err() error {
	select {
	case <-a.write.done:
		return a.write.err
	default:
		return errors.New("Workflow journal append has not settled")
	}
}

type CachedAgentResult struct{ AgentID, Result json.RawMessage }
type JournalSnapshot struct {
	Results map[string]CachedAgentResult
	Started map[string][]json.RawMessage
}

func NativeJournalFiles() JournalFiles {
	return JournalFiles{ReadFile: os.ReadFile, MkdirAll: func(path string) error { return os.MkdirAll(path, 0777) }, AppendFile: func(path string, data []byte) error {
		file, e := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0666)
		if e != nil {
			return e
		}
		_, e = file.Write(data)
		closeError := file.Close()
		if e != nil {
			return e
		}
		return closeError
	}}
}
func NewJournal(path string, files JournalFiles) (*Journal, error) {
	if !filepath.IsAbs(path) || files.ReadFile == nil || files.MkdirAll == nil || files.AppendFile == nil {
		return nil, errors.New("Workflow journal requires an absolute path and actual native file primitives")
	}
	done := make(chan struct{})
	close(done)
	return &Journal{path: path, files: files, last: &journalWrite{done: done}}, nil
}
func (j *Journal) Path() string { return j.path }

// BeginAppend serializes on the caller before the native write starts. The
// first failed write poisons the original promise chain; later entries retain
// that failure rather than writing past a missing transition.
func (j *Journal) BeginAppend(entry protocol.JSONObject) (*JournalAppend, error) {
	data, e := entry.MarshalJSON()
	if e != nil {
		return nil, e
	}
	data, e = protocol.ParseAndStringifyJavaScriptJSON(data)
	if e != nil {
		return nil, e
	}
	write := &journalWrite{line: append(data, '\n'), done: make(chan struct{})}
	j.mu.Lock()
	j.queue = append(j.queue, write)
	j.last = write
	if !j.running {
		j.running = true
		go j.drain()
	}
	j.mu.Unlock()
	return &JournalAppend{write: write}, nil
}
func (j *Journal) drain() {
	for {
		j.mu.Lock()
		if len(j.queue) == 0 {
			j.running = false
			j.mu.Unlock()
			return
		}
		write := j.queue[0]
		j.queue = j.queue[1:]
		failure := j.chainError
		j.mu.Unlock()
		if failure == nil && !j.directoryReady {
			failure = j.files.MkdirAll(filepath.Dir(j.path))
			j.directoryReady = failure == nil
		}
		if failure == nil {
			failure = j.files.AppendFile(j.path, write.line)
		}
		j.mu.Lock()
		if j.chainError == nil {
			j.chainError = failure
		}
		write.err = failure
		close(write.done)
		j.mu.Unlock()
	}
}

// CacheKey is the original chained call identity; order and UTF-16 prompt
// contents are preserved rather than replaced with a hash or object sorting.
func CacheKey(prompt, options, previous json.RawMessage) (json.RawMessage, error) {
	if _, e := protocol.DecodeJavaScriptJSONString(prompt); e != nil {
		return nil, e
	}
	if _, e := protocol.DecodeJavaScriptJSONString(previous); e != nil {
		return nil, e
	}
	if options == nil || string(options) == "null" {
		options = json.RawMessage("null")
	}
	serialized, e := protocol.ParseAndStringifyJavaScriptJSON(options)
	if e != nil {
		return nil, e
	}
	text := protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(string(serialized))))
	return protocol.JoinJavaScriptValuesJSON([]json.RawMessage{previous, json.RawMessage(`"|"`), prompt, json.RawMessage(`"|"`), text}, "")
}

// PendingFlush captures the original current chain. Future appends are a new
// tail; callers must use their existing owner's cooperative wait for Done.
func (j *Journal) PendingFlush() (<-chan struct{}, func() error) {
	j.mu.Lock()
	last := j.last
	done := last.done
	j.mu.Unlock()
	return done, func() error {
		select {
		case <-done:
			return last.err
		default:
			return errors.New("Workflow journal flush has not settled")
		}
	}
}
func (j *Journal) Load() (JournalSnapshot, error) {
	result := JournalSnapshot{Results: map[string]CachedAgentResult{}, Started: map[string][]json.RawMessage{}}
	data, e := j.files.ReadFile(j.path)
	if e != nil {
		return result, nil
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry protocol.JSONObject
		if e := entry.UnmarshalJSON([]byte(line)); e != nil {
			continue
		}
		if string(entryRaw(entry)) == "null" {
			return result, errors.New("null is not an object (evaluating 'entry.type')")
		}
		if !entry.IsObject() {
			continue
		}
		key := journalKey(entry.Get("key"))
		switch entry.String("type") {
		case "result":
			result.Results[key] = CachedAgentResult{AgentID: append(json.RawMessage(nil), entry.Get("agentId")...), Result: append(json.RawMessage(nil), entry.Get("result")...)}
			delete(result.Started, key)
		case "started":
			if _, exists := result.Results[key]; !exists {
				result.Started[key] = append(result.Started[key], append(json.RawMessage(nil), entry.Get("agentId")...))
			}
		}
	}
	return result, nil
}
func entryRaw(value protocol.JSONObject) json.RawMessage { data, _ := value.MarshalJSON(); return data }
func journalKey(value json.RawMessage) string {
	if value == nil {
		return "undefined"
	}
	return string(protocol.StringifyValidatedJavaScriptJSON(value))
}
func JournalStringKey(value string) string { data, _ := json.Marshal(value); return journalKey(data) }
