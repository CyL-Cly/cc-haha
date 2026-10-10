// Package memoryapi implements the original desktop Memory API over the native
// project memory directories. Writes retain the original Markdown format.
package memoryapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/text/unicode/norm"
)

const (
	MaxFileBytes      = 512 * 1024
	MaxFiles          = 500
	maxProjectIDBytes = 1024
	maxPathBytes      = 4096
	maxSegmentBytes   = 255
)

type Options struct {
	Home, ConfigDir, CWD string
	Environment          func(string) string
}
type Service struct{ options Options }
type Error struct {
	Status        int
	Code, Message string
}

func (e *Error) Error() string        { return e.Message }
func badRequest(message string) error { return &Error{400, "BAD_REQUEST", message} }
func notFound(message string) error   { return &Error{404, "NOT_FOUND", message} }
func conflict(message string) error   { return &Error{409, "CONFLICT", message} }
func methodNotAllowed(method string) error {
	return &Error{405, "METHOD_NOT_ALLOWED", "Method " + method + " not allowed"}
}

func New(options Options) (*Service, error) {
	if !filepath.IsAbs(options.Home) || !filepath.IsAbs(options.ConfigDir) || !filepath.IsAbs(options.CWD) || options.Environment == nil {
		return nil, errors.New("native Memory API requires explicit home, config directory, cwd and environment")
	}
	options.ConfigDir = norm.NFC.String(options.ConfigDir)
	return &Service{options}, nil
}

// HandleHTTP receives the same decoded path segments as the server router.
// The API owns its error projection, including unexpected filesystem failures.
func (s *Service) HandleHTTP(w http.ResponseWriter, r *http.Request, segments []string) {
	sub := "undefined"
	if len(segments) > 2 {
		sub = segments[2]
	}
	var result any
	var err error
	switch sub {
	case "projects":
		if r.Method != http.MethodGet {
			err = methodNotAllowed(r.Method)
			break
		}
		var projects []Project
		projects, err = s.Projects(r.URL.Query().Get("cwd"))
		result = struct {
			Projects []Project `json:"projects"`
		}{projects}
	case "files":
		if r.Method != http.MethodGet {
			err = methodNotAllowed(r.Method)
			break
		}
		id := r.URL.Query().Get("projectId")
		if id == "" {
			err = badRequest("Missing projectId")
			break
		}
		var files []File
		files, err = s.Files(id)
		result = struct {
			Files []File `json:"files"`
		}{files}
	case "file":
		result, err = s.handleFile(r)
	default:
		err = notFound("Unknown memory endpoint: " + sub)
	}
	status := http.StatusOK
	if err != nil {
		var api *Error
		if errors.As(err, &api) {
			status = api.Status
			result = map[string]string{"error": api.Code, "message": api.Message}
		} else {
			status = http.StatusInternalServerError
			result = map[string]string{"error": "INTERNAL_ERROR", "message": "An unexpected error occurred"}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(result)
}

type FileDetail struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	UpdatedAt string `json:"updatedAt"`
	Bytes     int64  `json:"bytes"`
}
type savedFile struct {
	Path      string `json:"path"`
	UpdatedAt string `json:"updatedAt"`
	Bytes     int64  `json:"bytes"`
}

func (s *Service) handleFile(r *http.Request) (any, error) {
	if r.Method == http.MethodGet {
		id := r.URL.Query().Get("projectId")
		if id == "" {
			return nil, badRequest("Missing projectId")
		}
		relative, err := requirePath(r.URL.Query().Get("path"))
		if err != nil {
			return nil, err
		}
		full, err := s.resolveFile(id, relative, true)
		if err != nil {
			return nil, err
		}
		f, err := os.Open(full)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		before, err := f.Stat()
		if err != nil {
			return nil, err
		}
		if before.Size() > MaxFileBytes {
			return nil, badRequest("Memory file is too large to edit: " + relative)
		}
		raw, err := io.ReadAll(f)
		if err != nil {
			return nil, err
		}
		after, err := f.Stat()
		if err != nil {
			return nil, err
		}
		content := string(protocol.DecodeJavaScriptUTF8(raw))
		if before.Size() != after.Size() || mtimeMillis(before.ModTime()) != mtimeMillis(after.ModTime()) || len(content) > MaxFileBytes {
			return nil, conflict("Memory file changed while it was being read: " + relative)
		}
		return struct {
			File FileDetail `json:"file"`
		}{FileDetail{relative, content, iso(after.ModTime()), after.Size()}}, nil
	}
	if r.Method != http.MethodPut {
		return nil, methodNotAllowed(r.Method)
	}
	body, err := parseBody(r)
	if err != nil {
		return nil, err
	}
	id, _ := rawString(body["projectId"])
	path, _ := rawString(body["path"])
	relative, err := requirePath(path)
	if err != nil {
		return nil, err
	}
	content, ok := rawString(body["content"])
	if !ok {
		return nil, badRequest("Missing or invalid \"content\" in request body")
	}
	if len(content) > MaxFileBytes {
		return nil, badRequest("Memory file content exceeds 512 KB")
	}
	expectedAt, atOK := rawString(body["expectedUpdatedAt"])
	expectedBytes, bytesOK := rawSafeBytes(body["expectedBytes"])
	_, atPresent := body["expectedUpdatedAt"]
	_, bytesPresent := body["expectedBytes"]
	if atPresent && !atOK || bytesPresent && !bytesOK || atOK != bytesOK {
		return nil, badRequest("\"expectedUpdatedAt\" and \"expectedBytes\" must be provided together as a valid file revision")
	}
	full, err := s.resolveFile(id, relative, false)
	if err != nil {
		return nil, err
	}
	memory, err := s.memoryDir(id, false)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		return nil, err
	}
	if err = assertWithin(filepath.Dir(full), memory, true); err != nil {
		return nil, err
	}
	if fileExists(full) {
		if err = assertWithin(full, memory, true); err != nil {
			return nil, err
		}
	}
	stat, err := queuedWrite(full, func() (os.FileInfo, error) {
		if atOK && bytesOK {
			current, e := os.Stat(full)
			if e != nil || iso(current.ModTime()) != expectedAt || current.Size() != expectedBytes {
				return nil, conflict("Memory file changed before it could be saved: " + relative)
			}
		}
		if e := writeAtomically(full, content); e != nil {
			return nil, e
		}
		return os.Stat(full)
	})
	if err != nil {
		return nil, err
	}
	return struct {
		OK   bool      `json:"ok"`
		File savedFile `json:"file"`
	}{true, savedFile{relative, iso(stat.ModTime()), stat.Size()}}, nil
}

func parseBody(r *http.Request) (map[string]json.RawMessage, error) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, badRequest("Invalid JSON body")
	}
	// Request.json() uses WHATWG UTF8 and permits one leading BOM.
	text := strings.TrimPrefix(string(protocol.DecodeJavaScriptUTF8(raw)), "\ufeff")
	var body map[string]json.RawMessage
	if !json.Valid([]byte(text)) {
		return nil, badRequest("Invalid JSON body")
	}
	if !strings.HasPrefix(strings.TrimSpace(text), "{") {
		return nil, badRequest("JSON body must be an object")
	}
	if err = json.Unmarshal([]byte(text), &body); err != nil {
		return nil, badRequest("Invalid JSON body")
	}
	for key := range body {
		switch key {
		case "projectId", "path", "content", "expectedUpdatedAt", "expectedBytes":
		default:
			return nil, badRequest("JSON body contains unsupported fields")
		}
	}
	return body, nil
}
func rawString(raw json.RawMessage) (string, bool) {
	units, err := protocol.DecodeJavaScriptJSONString(raw)
	if err != nil {
		return "", false
	}
	// Node Buffer encoding replaces unmatched UTF16 surrogates.
	return string(utf16.Decode(units)), true
}
func rawSafeBytes(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 || raw[0] == '"' || string(raw) == "null" || string(raw) == "true" || string(raw) == "false" {
		return 0, false
	}
	var n float64
	if json.Unmarshal(raw, &n) != nil || n < 0 || n > 9007199254740991 || math.Trunc(n) != n {
		return 0, false
	}
	return int64(n), true
}
func mtimeMillis(t time.Time) float64 { return float64(t.Unix())*1000 + float64(t.Nanosecond())/1e6 }
func iso(t time.Time) string {
	// Stats.mtime is a JavaScript Date: TimeClip truncates fractional
	// milliseconds toward zero, including timestamps immediately before epoch.
	t = time.UnixMilli(int64(mtimeMillis(t))).UTC()
	year := t.Year()
	if year >= 0 && year <= 9999 {
		return t.Format("2006-01-02T15:04:05.000Z")
	}
	sign := "+"
	if year < 0 {
		sign = "-"
		year = -year
	}
	return fmt.Sprintf("%s%06d-%s", sign, year, t.Format("01-02T15:04:05.000Z"))
}

type writeQueue struct {
	tail  chan struct{}
	count int
}

var writeQueues = struct {
	sync.Mutex
	paths map[string]*writeQueue
}{paths: map[string]*writeQueue{}}

func queuedWrite(path string, operation func() (os.FileInfo, error)) (os.FileInfo, error) {
	writeQueues.Lock()
	queue := writeQueues.paths[path]
	var previous chan struct{}
	if queue == nil {
		queue = &writeQueue{}
		writeQueues.paths[path] = queue
	} else {
		previous = queue.tail
	}
	current := make(chan struct{})
	queue.tail = current
	queue.count++
	writeQueues.Unlock()
	if previous != nil {
		<-previous
	}
	defer func() {
		writeQueues.Lock()
		close(current)
		queue.count--
		if queue.count == 0 {
			delete(writeQueues.paths, path)
		}
		writeQueues.Unlock()
	}()
	return operation()
}
func writeAtomically(path, content string) (err error) {
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"."+fmt.Sprint(os.Getpid())+".*.tmp")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer func() { _ = file.Close(); _ = os.Remove(temp) }()
	if _, err = file.WriteString(content); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(temp, path)
}
