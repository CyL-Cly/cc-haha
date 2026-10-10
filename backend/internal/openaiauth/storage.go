// Package openaiauth owns the CLI's OpenAI OAuth credential slot and original
// file-backed marker. It receives the shared secure store and native I/O rather
// than creating another account store or performing model requests.
package openaiauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const FileEnvironmentKey = "OPENAI_CODEX_OAUTH_FILE"
const StorageKey = "openaiCodexOauth"
const MarkerFile = "openai-oauth-file-backed"

type Files interface {
	ReadFile(string) ([]byte, error)
	MkdirAll(string, fs.FileMode) error
	WriteFile(string, []byte, fs.FileMode) error
	Rename(string, string) error
	Remove(string) error
	Exists(string) bool
}

type Result struct {
	Success bool    `json:"success"`
	Warning *string `json:"warning,omitempty"`
}

type Options struct {
	Home, CWD       string
	WorkDir         func() string
	Environment     func() map[string]string
	Files           Files
	SecureRead      func(context.Context) (json.RawMessage, error)
	SecureReadAsync func(context.Context) (json.RawMessage, error)
	SecureUpdate    func(context.Context, json.RawMessage) (Result, error)
	// AwaitRead runs only immutable physical reads; parsing and application
	// state remain on the caller's owning continuation.
	AwaitRead             func(context.Context, func(context.Context) ([]byte, error)) ([]byte, error)
	Refresh               func(context.Context, json.RawMessage) (protocol.JSONObject, error)
	NowMillis             func() int64
	PID                   int
	LogError              func(error)
	IsAdministrativeError func(error) bool
}

type Service struct {
	options Options
	mu      sync.Mutex
	cache   map[string]*Value
}

// Value preserves the unvalidated secure-store value. For object values the
// original memoized reference is retained; file reads produce normalized fields.
type Value struct {
	Object protocol.JSONObject
	raw    json.RawMessage
}

func NewValue(raw json.RawMessage) (*Value, error) {
	if raw == nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	if !json.Valid(raw) {
		return nil, errors.New("invalid OpenAI OAuth JSON")
	}
	v := &Value{raw: append(json.RawMessage(nil), raw...)}
	if err := v.Object.UnmarshalJSON(raw); err != nil {
		v.Object = protocol.JSONObject{}
	}
	return v, nil
}

func ObjectValue(object protocol.JSONObject) *Value { return &Value{Object: object} }
func (v *Value) JSON() json.RawMessage {
	if v == nil {
		return json.RawMessage("null")
	}
	if v.Object.IsObject() {
		raw, _ := v.Object.MarshalJSON()
		return protocol.StringifyValidatedJavaScriptJSON(raw)
	}
	return protocol.StringifyValidatedJavaScriptJSON(v.raw)
}
func (v *Value) MarshalJSON() ([]byte, error) { return v.JSON(), nil }
func (v *Value) Get(key string) json.RawMessage {
	if v == nil {
		return nil
	}
	return v.Object.Get(key)
}

func New(o Options) (*Service, error) {
	if !filepath.IsAbs(o.Home) || !filepath.IsAbs(o.CWD) || o.Environment == nil || o.Files == nil || o.SecureRead == nil || o.SecureReadAsync == nil || o.SecureUpdate == nil {
		return nil, errors.New("CLI OpenAI OAuth requires explicit native paths, files, environment and shared secure storage")
	}
	if o.NowMillis == nil {
		o.NowMillis = func() int64 { return time.Now().UnixMilli() }
	}
	if o.PID == 0 {
		o.PID = os.Getpid()
	}
	if o.AwaitRead == nil {
		o.AwaitRead = func(ctx context.Context, work func(context.Context) ([]byte, error)) ([]byte, error) {
			return work(ctx)
		}
	}
	return &Service{options: o, cache: map[string]*Value{}}, nil
}

func (s *Service) resolve(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	cwd := s.options.CWD
	if s.options.WorkDir != nil {
		cwd = s.options.WorkDir()
	}
	return filepath.Join(cwd, path)
}
func (s *Service) file() string {
	return protocol.TrimJavaScriptString(s.options.Environment()[FileEnvironmentKey])
}
func (s *Service) MarkerPath() string {
	config := s.options.Environment()["CLAUDE_CONFIG_DIR"]
	if config == "" {
		config = filepath.Join(s.options.Home, ".claude")
	}
	return s.resolve(filepath.Join(config, "cc-haha", MarkerFile))
}
func (s *Service) log(err error) {
	if s.options.LogError != nil {
		s.options.LogError(err)
	}
}
func (s *Service) ClearCache() { s.mu.Lock(); s.cache = map[string]*Value{}; s.mu.Unlock() }

func NormalizeFile(data []byte) (*Value, error) {
	data = protocol.DecodeJavaScriptUTF8(data)
	if !json.Valid(data) {
		return nil, errors.New("invalid OpenAI OAuth JSON")
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' {
		return nil, nil
	}
	var object protocol.JSONObject
	if err := object.UnmarshalJSON(data); err != nil {
		return nil, err
	}
	if !object.IsObject() {
		return nil, nil
	}
	for _, key := range []string{"accessToken", "refreshToken"} {
		value := bytes.TrimSpace(object.Get(key))
		if len(value) == 0 || value[0] != '"' {
			return nil, nil
		}
	}
	expires := bytes.TrimSpace(object.Get("expiresAt"))
	if len(expires) == 0 || !(expires[0] == '-' || expires[0] >= '0' && expires[0] <= '9') {
		return nil, nil
	}
	result := protocol.NewJSONObject()
	for _, key := range []string{"accessToken", "refreshToken", "expiresAt"} {
		result.Set(key, object.Get(key))
	}
	for _, key := range []string{"idToken", "accountId", "email", "clientId"} {
		value := bytes.TrimSpace(object.Get(key))
		if len(value) > 0 && value[0] == '"' {
			result.Set(key, object.Get(key))
		}
	}
	return ObjectValue(result), nil
}
func (s *Service) readFile(path string, asynchronous bool, ctx context.Context) (*Value, error) {
	physical := s.resolve(path)
	var data []byte
	var err error
	if asynchronous {
		data, err = s.options.AwaitRead(ctx, func(context.Context) ([]byte, error) { return s.options.Files.ReadFile(physical) })
	} else {
		data, err = s.options.Files.ReadFile(physical)
	}
	if err != nil {
		if s.administrative(err) {
			return nil, err
		}
		if !errors.Is(err, fs.ErrNotExist) {
			s.log(err)
		}
		return nil, nil
	}
	value, err := NormalizeFile(data)
	if err != nil {
		s.log(err)
	}
	return value, nil
}
func secureValue(data json.RawMessage) (*Value, error) {
	var record protocol.JSONObject
	if err := record.UnmarshalJSON(data); err != nil || !record.IsObject() {
		return nil, nil
	}
	return NewValue(record.Get(StorageKey))
}
func (s *Service) GetCached(ctx context.Context) *Value {
	file := s.file()
	key := "secure-storage"
	if file != "" {
		key = "file:" + file
	}
	s.mu.Lock()
	cached, present := s.cache[key]
	s.mu.Unlock()
	if present {
		return cached
	}
	var value *Value
	if file != "" {
		value, _ = s.readFile(file, false, ctx)
	} else if !s.options.Files.Exists(s.MarkerPath()) {
		data, err := s.options.SecureRead(ctx)
		if err == nil {
			value, err = secureValue(data)
		}
		if err != nil {
			s.log(err)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if prior, exists := s.cache[key]; exists {
		return prior
	}
	s.cache[key] = value
	return value
}
func (s *Service) GetAsync(ctx context.Context) (*Value, error) {
	if file := s.file(); file != "" {
		return s.readFile(file, true, ctx)
	}
	if s.options.Files.Exists(s.MarkerPath()) {
		return nil, nil
	}
	data, err := s.options.SecureReadAsync(ctx)
	if err == nil {
		var value *Value
		value, err = secureValue(data)
		if err == nil {
			return value, nil
		}
	}
	if s.administrative(err) {
		return nil, err
	}
	s.log(err)
	return nil, nil
}
func (s *Service) mark() {
	path := s.MarkerPath()
	if err := s.options.Files.MkdirAll(filepath.Dir(path), 0777); err != nil {
		s.log(err)
		return
	}
	if err := s.options.Files.WriteFile(path, []byte("1\n"), 0600); err != nil {
		s.log(err)
	}
}
func (s *Service) clearMarker() {
	if err := s.options.Files.Remove(s.MarkerPath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		s.log(err)
	}
}
func (s *Service) writeFile(value *Value) (used bool, err error) {
	file := s.file()
	if file == "" {
		return false, nil
	}
	file = s.resolve(file)
	if err = s.options.Files.MkdirAll(filepath.Dir(file), 0777); err != nil {
		return false, err
	}
	tmp := fmt.Sprintf("%s.tmp.%d.%d", file, s.options.PID, s.options.NowMillis())
	renamed := false
	defer func() {
		if !renamed {
			if cleanup := s.options.Files.Remove(tmp); cleanup != nil && !errors.Is(cleanup, fs.ErrNotExist) {
				s.log(cleanup)
			}
		}
	}()
	var pretty bytes.Buffer
	if err = json.Indent(&pretty, value.JSON(), "", "  "); err != nil {
		return false, err
	}
	pretty.WriteByte('\n')
	if err = s.options.Files.WriteFile(tmp, pretty.Bytes(), 0600); err != nil {
		return false, err
	}
	if err = s.options.Files.Rename(tmp, file); err != nil {
		return false, err
	}
	renamed = true
	return true, nil
}
func saveRecord(data json.RawMessage, tokens *Value) (json.RawMessage, error) {
	if len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		data = json.RawMessage("{}")
	}
	var object protocol.JSONObject
	if err := object.UnmarshalJSON(data); err == nil && object.IsObject() {
		object.Set(StorageKey, tokens.JSON())
		raw, err := object.MarshalJSON()
		return raw, err
	}
	if len(bytes.TrimSpace(data)) > 0 && bytes.TrimSpace(data)[0] == '[' {
		return data, nil
	}
	return nil, errors.New("Attempted to assign to readonly property.")
}
func (s *Service) Save(ctx context.Context, tokens protocol.JSONObject) Result {
	return s.SaveValue(ctx, ObjectValue(tokens))
}
func (s *Service) SaveValue(ctx context.Context, tokens *Value) Result {
	result, _ := s.save(ctx, tokens)
	return result
}
func (s *Service) save(ctx context.Context, tokens *Value) (Result, error) {
	used, err := s.writeFile(tokens)
	if err == nil && used {
		s.mark()
		s.ClearCache()
		return Result{Success: true}, nil
	}
	if err == nil {
		var data json.RawMessage
		data, err = s.options.SecureRead(ctx)
		if err == nil {
			data, err = saveRecord(data, tokens)
		}
		if err == nil {
			var result Result
			result, err = s.options.SecureUpdate(ctx, data)
			if err == nil {
				if result.Success {
					s.clearMarker()
				}
				s.ClearCache()
				return result, nil
			}
		}
	}
	if s.administrative(err) {
		return Result{}, err
	}
	s.log(err)
	warning := "Failed to save OpenAI OAuth tokens: " + err.Error()
	return Result{Warning: &warning}, nil
}
func (s *Service) Delete(ctx context.Context) bool {
	var err error
	if file := s.file(); file != "" {
		err = s.options.Files.Remove(s.resolve(file))
		if errors.Is(err, fs.ErrNotExist) {
			err = nil
		}
		if err == nil {
			s.mark()
			s.ClearCache()
			return true
		}
	} else {
		var data json.RawMessage
		data, err = s.options.SecureRead(ctx)
		if err == nil {
			if len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
				data = json.RawMessage("{}")
			}
			var object protocol.JSONObject
			if object.UnmarshalJSON(data) == nil && object.IsObject() {
				object.Delete(StorageKey)
				data, err = object.MarshalJSON()
			}
			if err == nil {
				var result Result
				result, err = s.options.SecureUpdate(ctx, data)
				if err == nil {
					s.ClearCache()
					return result.Success
				}
			}
		}
	}
	s.log(err)
	return false
}

// EnsureFresh matches the exported CLI helper: clear cache, read current state,
// and return nil after refresh or persistence failure. It differs deliberately
// from the interactive OAuth service method, which throws refresh failures.
func (s *Service) EnsureFresh(ctx context.Context) (*Value, error) {
	s.ClearCache()
	tokens, err := s.GetAsync(ctx)
	if err != nil {
		return nil, err
	}
	if tokens == nil || !valueTruthy(tokens) {
		return nil, nil
	}
	if !desktopoauth.OpenAITokenExpired(tokens.Get("expiresAt"), s.options.NowMillis()) {
		return tokens, nil
	}
	if s.options.Refresh == nil {
		return nil, nil
	}
	refreshed, err := s.options.Refresh(ctx, tokens.Get("refreshToken"))
	if err != nil {
		if s.administrative(err) {
			return nil, err
		}
		return nil, nil
	}
	updated := desktopoauth.WithRefreshedOpenAIAccessToken(tokens.Object, refreshed, s.options.NowMillis())
	result, err := s.save(ctx, ObjectValue(updated))
	if err != nil {
		return nil, err
	}
	if !result.Success {
		return nil, nil
	}
	return ObjectValue(updated), nil
}
func (s *Service) administrative(err error) bool {
	return err != nil && s.options.IsAdministrativeError != nil && s.options.IsAdministrativeError(err)
}
func valueTruthy(value *Value) bool {
	if value == nil {
		return false
	}
	if value.Object.IsObject() {
		return true
	}
	raw := bytes.TrimSpace(value.JSON())
	if bytes.Equal(raw, []byte("false")) || bytes.Equal(raw, []byte("null")) || bytes.Equal(raw, []byte(`""`)) || bytes.Equal(raw, []byte("0")) {
		return false
	}
	return true
}

type PhysicalFiles struct{}

func (PhysicalFiles) ReadFile(path string) ([]byte, error)         { return os.ReadFile(path) }
func (PhysicalFiles) MkdirAll(path string, mode fs.FileMode) error { return os.MkdirAll(path, mode) }
func (PhysicalFiles) WriteFile(path string, data []byte, mode fs.FileMode) error {
	return os.WriteFile(path, data, mode)
}
func (PhysicalFiles) Rename(from, to string) error { return os.Rename(from, to) }
func (PhysicalFiles) Remove(path string) error     { return os.Remove(path) }
func (PhysicalFiles) Exists(path string) bool      { _, err := os.Stat(path); return err == nil }
