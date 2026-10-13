package errorlog

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/analytics"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Files interface {
	MkdirAll(string) error
	AppendFile(string, []byte) error
	Remove(string) error
	Symlink(string, string) error
}
type Metadata struct {
	Environment                                        map[string]string
	CWD, Home, ConfigDir, Platform, SessionID, Version string
}
type NativeSinkOptions struct {
	Files     Files
	Metadata  func() Metadata
	Now       func() time.Time
	Arguments []string
	Stderr    func(string)
	Formatted bool
	// Defer is the same deferred overflow cutpoint as Source setImmediate.
	// Tests may retain callbacks; normal native use schedules independent work.
	Defer func(func())
}
type nativeBuffer struct {
	lines, pending           []string
	timer                    *time.Timer
	deferred, debug, ensured bool
}
type NativeSink struct {
	mu        sync.Mutex
	options   NativeSinkOptions
	date      string
	buffers   map[string]*nativeBuffer
	order     []string
	closed    bool
	latest    bool
	debugMode bool
	minDebug  string
}

func NewNativeSink(o NativeSinkOptions) (*NativeSink, error) {
	if o.Files == nil || o.Metadata == nil || o.Now == nil {
		return nil, errors.New("native error sink requires actual files, metadata and clock")
	}
	if o.Defer == nil {
		o.Defer = func(f func()) { go f() }
	}
	sink := &NativeSink{options: o, date: strings.NewReplacer(":", "-", ".", "-").Replace(timestamp(o.Now())), buffers: map[string]*nativeBuffer{}}
	m := o.Metadata()
	sink.debugMode = analytics.EnvTruthy(m.Environment["DEBUG"]) || analytics.EnvTruthy(m.Environment["DEBUG_SDK"])
	for _, arg := range o.Arguments {
		if arg == "--debug" || arg == "-d" || arg == "-d2e" || arg == "--debug-to-stderr" || arg == "--debug-file" || strings.HasPrefix(arg, "--debug=") || strings.HasPrefix(arg, "--debug-file=") {
			sink.debugMode = true
		}
	}
	sink.minDebug = strings.ToLower(protocol.TrimJavaScriptString(m.Environment["CLAUDE_CODE_DEBUG_LOG_LEVEL"]))
	if e := sink.debugLog("Error log sink initialized", "DEBUG"); e != nil {
		return nil, e
	}
	return sink, nil
}

// DebugMode is the same process debug decision sampled by RemoteIO's bridge
// constructor. Formatted SDK output alone does not enable debug echoing.
func (s *NativeSink) DebugMode() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.debugMode }
func (s *NativeSink) Write(err error) {
	m := s.options.Metadata()
	text := Text(err)
	details := Details(err)
	name := details.Name
	if name == "" {
		name = "Error"
	}
	debug := name + ": " + text
	if e := s.debugLog(debug, "ERROR"); e != nil {
		return
	}
	if m.Environment["USER_TYPE"] == "ant" {
		base := m.Environment["CLAUDE_CONFIG_DIR"]
		if base != "" {
			base = filepath.Join(base, "Cache")
		} else {
			switch m.Platform {
			case "darwin":
				base = filepath.Join(m.Home, "Library", "Caches", "claude-cli-nodejs")
			case "win32", "windows":
				base = m.Environment["LOCALAPPDATA"]
				if base == "" {
					base = filepath.Join(m.Home, "AppData", "Local")
				}
				base = filepath.Join(base, "claude-cli-nodejs", "Cache")
			default:
				base = m.Environment["XDG_CACHE_HOME"]
				if base == "" {
					base = filepath.Join(m.Home, ".cache")
				}
				base = filepath.Join(base, "claude-cli-nodejs")
			}
		}
		row := protocol.NewJSONObject(protocol.JSONField{Name: "timestamp", Value: quoted(timestamp(s.options.Now()))}, protocol.JSONField{Name: "error", Value: quoted(text)}, protocol.JSONField{Name: "cwd", Value: quoted(m.CWD)}, protocol.JSONField{Name: "userType", Value: quoted(m.Environment["USER_TYPE"])}, protocol.JSONField{Name: "sessionId", Value: quoted(m.SessionID)}, protocol.JSONField{Name: "version", Value: quoted(m.Version)})
		data, e := row.MarshalJSON()
		if e == nil {
			s.write(filepath.Join(base, sanitize(m.CWD), "errors", s.date+".jsonl"), string(data)+"\n", false, 50, false)
		}
	}
}

// Debug uses the same Source debug writer as attached error reporting. It does
// not create a second logger or touch the SDK output queue.
func (s *NativeSink) Debug(message, level string) error {
	return s.debugLog(message, strings.ToUpper(level))
}

var debugLevels = map[string]int{"VERBOSE": 0, "DEBUG": 1, "INFO": 2, "WARN": 3, "ERROR": 4}

func (s *NativeSink) debugLog(debug, level string) error {
	m := s.options.Metadata()
	minimum, ok := debugLevels[strings.ToUpper(s.minDebug)]
	if !ok {
		minimum = debugLevels["DEBUG"]
	}
	if debugLevels[level] < minimum {
		return nil
	}
	toStderr := false
	file, fileFound := "", false
	for i, arg := range s.options.Arguments {
		if arg == "--debug-to-stderr" || arg == "-d2e" {
			toStderr = true
		}
		// getDebugFilePath returns the first raw argv match, even an empty string.
		if !fileFound && strings.HasPrefix(arg, "--debug-file=") {
			file = strings.TrimPrefix(arg, "--debug-file=")
			fileFound = true
		}
		if !fileFound && arg == "--debug-file" && i+1 < len(s.options.Arguments) {
			file = s.options.Arguments[i+1]
			fileFound = true
		}
	}
	if (m.Environment["NODE_ENV"] != "test" || toStderr) && (m.Environment["USER_TYPE"] == "ant" || s.debugMode) && debugAllowed(debug, s.options.Arguments) {
		if s.options.Formatted && strings.Contains(debug, "\n") {
			data, _ := json.Marshal(debug)
			debug = string(data)
		}
		debug = timestamp(s.options.Now()) + " [" + level + "] " + protocol.TrimJavaScriptString(debug) + "\n"
		if toStderr {
			if s.options.Stderr != nil {
				s.options.Stderr(debug)
			}
			return nil
		}
		if !fileFound {
			var found bool
			file, found = m.Environment["CLAUDE_CODE_DEBUG_LOGS_DIR"]
			if !found {
				file = filepath.Join(m.ConfigDir, "debug", m.SessionID+".txt")
			}
		}
		return s.write(file, debug, s.debugMode, 100, true)
	}
	return nil
}

func quoted(s string) json.RawMessage { b, _ := json.Marshal(s); return b }
func sanitize(text string) string {
	units := utf16.Encode([]rune(text))
	var out strings.Builder
	for _, unit := range units {
		if unit >= 'a' && unit <= 'z' || unit >= 'A' && unit <= 'Z' || unit >= '0' && unit <= '9' {
			out.WriteRune(rune(unit))
		} else {
			out.WriteByte('-')
		}
	}
	s := out.String()
	if len(s) <= 200 {
		return s
	}
	hash := int32(0)
	for _, unit := range units {
		hash = hash*31 + int32(unit)
	}
	value := int64(hash)
	if value < 0 {
		value = -value
	}
	return s[:200] + "-" + strconv.FormatInt(value, 36)
}
func (s *NativeSink) write(path, text string, immediate bool, limit int, debug bool) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	b := s.buffers[path]
	if b == nil {
		b = &nativeBuffer{debug: debug}
		s.buffers[path] = b
		s.order = append(s.order, path)
	}
	if immediate {
		e := s.append(path, text, b)
		s.mu.Unlock()
		return e
	}
	b.lines = append(b.lines, text)
	if b.timer == nil {
		b.timer = time.AfterFunc(time.Second, func() { s.mu.Lock(); defer s.mu.Unlock(); b.timer = nil; s.flushBuffer(path, b) })
	}
	schedule := false
	if len(b.lines) >= limit {
		b.pending = append(b.pending, b.lines...)
		b.lines = nil
		b.timer.Stop()
		b.timer = nil
		if !b.deferred {
			b.deferred = true
			schedule = true
		}
	}
	s.mu.Unlock()
	if schedule {
		s.options.Defer(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			b.deferred = false
			if len(b.pending) > 0 {
				s.append(path, strings.Join(b.pending, ""), b)
				b.pending = nil
			}
		})
	}
	return nil
}

func (s *NativeSink) append(path, text string, b *nativeBuffer) error {
	if b.debug {
		if !b.ensured {
			_ = s.options.Files.MkdirAll(filepath.Dir(path))
			b.ensured = true
		}
		if e := s.options.Files.AppendFile(path, []byte(text)); e != nil {
			return e
		}
		if !s.latest {
			s.latest = true
			link := filepath.Join(filepath.Dir(path), "latest")
			_ = s.options.Files.Remove(link)
			_ = s.options.Files.Symlink(path, link)
		}
		return nil
	}
	if e := s.options.Files.AppendFile(path, []byte(text)); e != nil {
		if e = s.options.Files.MkdirAll(filepath.Dir(path)); e != nil {
			return e
		}
		return s.options.Files.AppendFile(path, []byte(text))
	}
	return nil
}

func (s *NativeSink) flushBuffer(path string, b *nativeBuffer) {
	if len(b.pending) > 0 {
		s.append(path, strings.Join(b.pending, ""), b)
		b.pending = nil
	}
	if len(b.lines) > 0 {
		s.append(path, strings.Join(b.lines, ""), b)
		b.lines = nil
		if b.timer != nil {
			b.timer.Stop()
			b.timer = nil
		}
	}
}
func (s *NativeSink) Flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, path := range s.order {
		s.flushBuffer(path, s.buffers[path])
	}
}
func (s *NativeSink) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	for _, path := range s.order {
		b := s.buffers[path]
		s.flushBuffer(path, b)
		if b.timer != nil {
			b.timer.Stop()
			b.timer = nil
		}
	}
	s.closed = true
}

var debugMCP = regexp.MustCompile(`^MCP server ["']([^"']+)["']`)
var debugPrefix = regexp.MustCompile(`^([^:\[]+):`)
var debugBracket = regexp.MustCompile(`^\[([^\]]+)\]`)
var debugSecondary = regexp.MustCompile(`:\s*([^:]+?)(?:\s+(?:type|mode|status|event))?:`)

func debugAllowed(message string, args []string) bool {
	var pattern string
	for _, arg := range args {
		if strings.HasPrefix(arg, "--debug=") {
			pattern = strings.TrimPrefix(arg, "--debug=")
			break
		}
	}
	filters := []string{}
	inclusive, exclusive := false, false
	for _, f := range strings.Split(pattern, ",") {
		f = protocol.TrimJavaScriptString(f)
		if f != "" {
			filters = append(filters, strings.ToLower(strings.TrimPrefix(f, "!")))
			if strings.HasPrefix(f, "!") {
				exclusive = true
			} else {
				inclusive = true
			}
		}
	}
	if len(filters) == 0 || inclusive && exclusive {
		return true
	}
	categories := []string{}
	if match := debugMCP.FindStringSubmatch(message); len(match) > 0 {
		categories = append(categories, "mcp", strings.ToLower(match[1]))
	} else if match := debugPrefix.FindStringSubmatch(message); len(match) > 0 {
		categories = append(categories, strings.ToLower(protocol.TrimJavaScriptString(match[1])))
	}
	if match := debugBracket.FindStringSubmatch(message); len(match) > 0 {
		categories = append(categories, strings.ToLower(protocol.TrimJavaScriptString(match[1])))
	}
	if strings.Contains(strings.ToLower(message), "1p event:") {
		categories = append(categories, "1p")
	}
	if match := debugSecondary.FindStringSubmatch(message); len(match) > 0 {
		cat := strings.ToLower(protocol.TrimJavaScriptString(match[1]))
		if len(utf16.Encode([]rune(cat))) < 30 && !strings.Contains(cat, " ") {
			categories = append(categories, cat)
		}
	}
	if len(categories) == 0 {
		return false
	}
	for _, cat := range categories {
		for _, f := range filters {
			if cat == f {
				return !exclusive
			}
		}
	}
	return exclusive
}
