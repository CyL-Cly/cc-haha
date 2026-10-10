// Package diagnostics ports the synchronous diagnostic segment writer. One
// Process instance is shared by all callers in a process. It owns no session,
// scheduler, goroutine, cleanup callback, or output consumer.
package diagnostics

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const maxSegmentBytes = 1024 * 1024
const maxCompletedSegments = 4

type FileSystem interface {
	MkdirAll(string, fs.FileMode) error
	Lstat(string) (fs.FileInfo, error)
	Stat(string) (fs.FileInfo, error)
	Exists(string) bool
	Chmod(string, fs.FileMode) error
	Append(string, []byte, fs.FileMode) error
	Rename(string, string) error
	ReadDir(string) ([]string, error)
	Remove(string) error
}

type Options struct {
	// Environment is consulted on every log call, before clocks or filesystem.
	Environment func(string) string
	Now         func() time.Time
	NowMillis   func() int64
	// ReadNowMillis represents the same process clock when the host clock can
	// return an original error. Nil preserves the native NowMillis boundary.
	ReadNowMillis func() (float64, error)
	// Stringify is the process JSON serializer boundary. A nil value uses the
	// structured JavaScript JSON codec; failures precede the storage catch.
	Stringify func(protocol.JSONObject) ([]byte, error)
	PID       int
	Platform  string
	Files     FileSystem
}

type Process struct {
	options  Options
	mu       sync.Mutex
	sequence uint64
}

func NewProcess(options Options) (*Process, error) {
	if options.Environment == nil || options.Now == nil || options.NowMillis == nil || options.PID <= 0 || options.Platform == "" || options.Files == nil {
		return nil, errors.New("diagnostics requires explicit process environment, clocks, PID, platform and filesystem")
	}
	return &Process{options: options}, nil
}

func quoted(value string) json.RawMessage {
	return protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(value)))
}

// isoDate retains Date.toISOString's UTC milliseconds and expanded year.
func isoDate(value time.Time) (string, error) {
	if millis := value.UnixMilli(); millis < -8640000000000000 || millis > 8640000000000000 {
		return "", &InvalidDateError{}
	}
	value = value.UTC()
	year := value.Year()
	text := fmt.Sprintf("%04d", year)
	if year < 0 {
		text = fmt.Sprintf("-%06d", -year)
	} else if year > 9999 {
		text = fmt.Sprintf("+%06d", year)
	}
	return fmt.Sprintf("%s-%02d-%02dT%02d:%02d:%02d.%03dZ", text, value.Month(), value.Day(), value.Hour(), value.Minute(), value.Second(), value.Nanosecond()/1000000), nil
}

// InvalidDateError retains Date.toISOString's error class and message. Each
// invalid call creates a distinct object, including the failed-log replacement.
type InvalidDateError struct{ marker byte }

func (*InvalidDateError) Error() string     { return "Invalid Date" }
func (*InvalidDateError) ErrorName() string { return "RangeError" }

// Environment reads the same live process environment as Log. Transport
// activity signals borrow it rather than caching a separate environment.
func (process *Process) Environment(name string) string {
	return process.options.Environment(name)
}

// StringifyEntry is the default structured-data process serializer. It does not
// claim arbitrary JavaScript prototypes, cycles, accessors or toJSON execution.
func StringifyEntry(entry protocol.JSONObject) ([]byte, error) {
	raw, err := entry.MarshalJSON()
	if err != nil {
		return nil, err
	}
	return protocol.ParseAndStringifyJavaScriptJSON(raw)
}

// Log returns serialization/date errors, which Source raises before entering
// its storage try/catch. Storage failures are retried once and then swallowed.
// No diagnostic content is sent to stdout/stderr or the model output stream.
func (process *Process) Log(level, event string, data *protocol.JSONObject) error {
	base := process.options.Environment("CLAUDE_CODE_DIAGNOSTICS_FILE")
	if base == "" {
		return nil
	}
	timestamp, err := isoDate(process.options.Now())
	if err != nil {
		return err
	}
	value := protocol.NewJSONObject()
	if data != nil && data.IsObject() {
		value = *data
	}
	rawData, err := value.MarshalJSON()
	if err != nil {
		return err
	}
	entry := protocol.NewJSONObject(
		protocol.JSONField{Name: "timestamp", Value: quoted(timestamp)},
		protocol.JSONField{Name: "level", Value: quoted(level)},
		protocol.JSONField{Name: "event", Value: quoted(event)},
		protocol.JSONField{Name: "data", Value: rawData},
	)
	stringify := process.options.Stringify
	if stringify == nil {
		stringify = StringifyEntry
	}
	raw, err := stringify(entry)
	if err != nil {
		return err
	}
	line := append(raw, '\n')
	active := base + "." + strconv.Itoa(process.options.PID) + ".current.jsonl"
	process.mu.Lock()
	defer process.mu.Unlock()
	for attempt := 0; attempt < 2; attempt++ {
		if process.append(base, active, line) == nil {
			break
		}
	}
	return nil
}

func (process *Process) append(base, active string, line []byte) error {
	if err := process.prepare(active); err != nil {
		return err
	}
	if err := process.rotate(base, active, len(line)); err != nil {
		return err
	}
	return process.options.Files.Append(active, line, 0600)
}

func (process *Process) prepare(active string) error {
	files := process.options.Files
	directory := filepath.Dir(active)
	if err := files.MkdirAll(directory, 0700); err != nil {
		return err
	}
	info, err := files.Lstat(directory)
	if err != nil {
		return err
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("refusing non-directory or symbolic link diagnostic directory")
	}
	if process.options.Platform != "win32" {
		if err := files.Chmod(directory, 0700); err != nil {
			return err
		}
	}
	if !files.Exists(active) {
		return nil
	}
	info, err = files.Lstat(active)
	if err != nil {
		return err
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("refusing non-regular or symbolic link diagnostic file")
	}
	if process.options.Platform != "win32" {
		return files.Chmod(active, 0600)
	}
	return nil
}

func (process *Process) rotate(base, active string, incoming int) error {
	files := process.options.Files
	if !files.Exists(active) {
		return nil
	}
	info, err := files.Stat(active)
	if err != nil {
		return err
	}
	if info.Size()+int64(incoming) <= maxSegmentBytes {
		return nil
	}
	process.sequence++
	completed := fmt.Sprintf("%s.%d.%d-%d.jsonl", base, process.options.PID, process.options.NowMillis(), process.sequence)
	if err := files.Rename(active, completed); err != nil {
		return err
	}
	names, err := files.ReadDir(filepath.Dir(base))
	if err != nil {
		return err
	}
	prefix := filepath.Base(base) + "." + strconv.Itoa(process.options.PID) + "."
	var owned []string
	for _, name := range names {
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, ".jsonl") && !strings.HasSuffix(name, ".current.jsonl") {
			owned = append(owned, name)
		}
	}
	sort.Slice(owned, func(left, right int) bool {
		return slices.Compare(utf16.Encode([]rune(owned[left])), utf16.Encode([]rune(owned[right]))) < 0
	})
	for index := 0; index < len(owned)-maxCompletedSegments; index++ {
		if err := files.Remove(filepath.Join(filepath.Dir(base), owned[index])); err != nil {
			return err
		}
	}
	return nil
}
