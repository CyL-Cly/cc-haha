// Package securestorage owns the original credentials record. macOS uses the
// same Keychain identity and plaintext fallback; other platforms use the
// existing .credentials.json. Consumers preserve unrelated account fields.
package securestorage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Command struct {
	Args  []string
	Input string
	// Only the concrete prefetch producer uses execFile's bounded stdout/
	// stderr and killed receipt. Ordinary storage commands keep their transport.
	prefetch            bool
	prefetchDirectory   string
	prefetchEnvironment map[string]string
	// ShellScript preserves the original synchronous shell forms. Async reads
	// and payload writes retain their argv/stdin transport.
	ShellScript string
}
type Options struct {
	Home, ConfigDir, Platform string
	Environment               map[string]string
	ConfigDirExplicit         bool
	Now                       func() time.Time
	// NativePlaintextLocation supplies the actual native file operands. An
	// explicitly empty storage directory stays empty for Source mkdir semantics.
	NativePlaintextLocation func() (directory, path string)
	// NativeKeychainServiceName reads the current process directory identity.
	// Directory changes do not invalidate the Source's shared 30-second cache.
	NativeKeychainServiceName func() string
	// NativeKeychainUser reads the current raw USER scalar. The credentials
	// account falls back to the pinned Bun userInfo launch snapshot.
	NativeKeychainUser func() (string, bool)
	// NativeSecurityContext supplies the actual current process environment
	// and cwd. Synchronous shell expansion occurs at this native I/O boundary.
	NativeSecurityContext func() (string, map[string]string)
	// RunSecurity is only the native security(1) I/O primitive. Tests use it to
	// avoid every lookup or mutation of the developer's actual Keychain.
	RunSecurity func(context.Context, Command) ([]byte, error)
	// StartSecurity is only the actual asynchronous prefetch launch primitive.
	// Its concrete result requires Cmd.Start; it accepts no cached read value.
	StartSecurity func(context.Context, Command) (*KeychainPrefetchProcess, error)
}
type Store struct {
	mu                        sync.Mutex
	options                   Options
	service, account, path    string
	cachedJSON                json.RawMessage
	cachedAt                  time.Time
	generation                uint64
	readInFlight              *sourceReadFlight
	securityPrimitiveProvided bool
}
type Result struct {
	Success bool
	Warning string
}

func ServiceName(config string, explicit bool, environment map[string]string) string {
	suffix := ""
	if environment["CLAUDE_CODE_CUSTOM_OAUTH_URL"] != "" {
		suffix = "-custom-oauth"
	} else if environment["USER_TYPE"] == "ant" {
		truthy := func(s string) bool {
			switch strings.ToLower(strings.TrimSpace(s)) {
			case "1", "true", "yes", "on":
				return true
			}
			return false
		}
		if truthy(environment["USE_LOCAL_OAUTH"]) {
			suffix = "-local-oauth"
		} else if truthy(environment["USE_STAGING_OAUTH"]) {
			suffix = "-staging-oauth"
		}
	}
	name := "Claude Code" + suffix + "-credentials"
	if explicit {
		sum := sha256.Sum256([]byte(config))
		name += "-" + hex.EncodeToString(sum[:4])
	}
	return name
}
func New(o Options) (*Store, error) {
	if !filepath.IsAbs(o.Home) || !filepath.IsAbs(o.ConfigDir) || o.Environment == nil {
		return nil, errors.New("secure storage requires actual home, config and process environment")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	environment := map[string]string{}
	for k, v := range o.Environment {
		environment[k] = v
	}
	o.Environment = environment
	securityPrimitiveProvided := o.RunSecurity != nil
	if o.RunSecurity == nil {
		o.RunSecurity = func(ctx context.Context, request Command) ([]byte, error) {
			directory, currentEnvironment := o.Home, environment
			if request.prefetch {
				directory, currentEnvironment = request.prefetchDirectory, request.prefetchEnvironment
			} else if o.NativeSecurityContext != nil {
				directory, currentEnvironment = o.NativeSecurityContext()
			}
			if !filepath.IsAbs(directory) || currentEnvironment == nil {
				return nil, errors.New("secure storage requires actual current process context")
			}
			command := newSecurityCommand(ctx, request, directory, currentEnvironment)
			if request.prefetch {
				return runKeychainPrefetchCommand(ctx, command)
			}
			return command.Output()
		}
	}
	account := environment["USER"]
	if account == "" {
		if current, err := user.Current(); err == nil {
			account = current.Username
		} else {
			account = "claude-code-user"
		}
	}
	return &Store{options: o, service: ServiceName(o.ConfigDir, o.ConfigDirExplicit, environment), account: account, path: filepath.Join(o.ConfigDir, ".credentials.json"), securityPrimitiveProvided: securityPrimitiveProvided}, nil
}
func parse(data []byte) *protocol.JSONObject {
	var record protocol.JSONObject
	if record.UnmarshalJSON(protocol.DecodeJavaScriptUTF8(data)) != nil || !record.IsObject() {
		return nil
	}
	return &record
}
func copy(record *protocol.JSONObject) *protocol.JSONObject {
	if record == nil {
		return nil
	}
	value := record.Clone()
	return &value
}
func (s *Store) plaintextLocation() (directory, path string) {
	if s.options.NativePlaintextLocation == nil {
		return s.options.ConfigDir, s.path
	}
	directory, path = s.options.NativePlaintextLocation()
	if !filepath.IsAbs(path) || directory != "" && !filepath.IsAbs(directory) {
		return "", ""
	}
	return directory, path
}
func (s *Store) keychainServiceName() string {
	if s.options.NativeKeychainServiceName != nil {
		return s.options.NativeKeychainServiceName()
	}
	return s.service
}
func (s *Store) keychainUsername() string {
	if s.options.NativeKeychainUser == nil {
		return s.account
	}
	if name, _ := s.options.NativeKeychainUser(); name != "" {
		return name
	}
	// Bun 1.3.14 userInfo().username captures USER at process launch. Unlike
	// Go's os/user lookup it remains that value after process.env changes,
	// preserving an empty launch value and using "unknown" only if absent.
	if name, present := s.options.Environment["USER"]; present {
		return name
	}
	return "unknown"
}
func (s *Store) keychainRawUser() string {
	if s.options.NativeKeychainUser != nil {
		name, _ := s.options.NativeKeychainUser()
		return name
	}
	return s.options.Environment["USER"]
}
func (s *Store) primary(ctx context.Context) *protocol.JSONObject {
	return parse(s.primarySourceJSON(ctx))
}
func (s *Store) read(ctx context.Context) *protocol.JSONObject {
	if s.options.Platform == "darwin" {
		if value := s.primary(ctx); value != nil {
			return value
		}
	}
	_, path := s.plaintextLocation()
	data, err := os.ReadFile(path)
	if err == nil {
		if value := parse(data); value != nil {
			return value
		}
	}
	if s.options.Platform == "darwin" {
		value := protocol.NewJSONObject()
		return &value
	}
	return nil
}
func (s *Store) Read(ctx context.Context) (*protocol.JSONObject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return copy(s.read(ctx)), ctx.Err()
}
func (s *Store) clear() {
	s.cachedJSON = nil
	s.cachedAt = time.Time{}
	s.generation++
	s.readInFlight = nil
}
func (s *Store) deletePrimary(ctx context.Context) bool {
	s.clear()
	account, service := s.keychainUsername(), s.keychainServiceName()
	_, err := s.options.RunSecurity(ctx, Command{Args: []string{"delete-generic-password", "-a", account, "-s", service}, ShellScript: `security delete-generic-password -a "` + account + `" -s "` + service + `"`})
	return nativeProcessReceipt(err)
}
func (s *Store) update(ctx context.Context, record protocol.JSONObject) Result {
	data, err := record.MarshalJSON()
	if err != nil {
		return Result{}
	}
	data, err = protocol.ParseAndStringifyJavaScriptJSON(data)
	if err != nil {
		return Result{}
	}
	return s.updateSourceJSON(ctx, data)
}
func (s *Store) updateSourceJSON(ctx context.Context, data json.RawMessage) Result {
	var before json.RawMessage
	if s.options.Platform == "darwin" {
		before = s.primarySourceJSON(ctx)
		s.clear()
		payload := hex.EncodeToString(data)
		service := s.keychainServiceName()
		account := s.keychainUsername()
		line := "add-generic-password -U -a \"" + account + "\" -s \"" + service + "\" -X \"" + payload + "\"\n"
		command := Command{Args: []string{"-i"}, Input: line}
		if len(utf16.Encode([]rune(line))) > 4032 {
			command = Command{Args: []string{"add-generic-password", "-U", "-a", account, "-s", service, "-X", payload}}
		}
		if _, err := s.options.RunSecurity(ctx, command); err == nil {
			s.cachedJSON, s.cachedAt = append(json.RawMessage(nil), data...), s.options.Now()
			if before == nil {
				_, path := s.plaintextLocation()
				_ = os.Remove(path)
			}
			return Result{Success: true}
		}
	}
	// Source mkdir is one level, accepts EEXIST, then writes/chmods the
	// ordinary credential file. A failed save never reports success.
	directory, path := s.plaintextLocation()
	if err := os.Mkdir(directory, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return Result{}
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return Result{}
	}
	if err := os.Chmod(path, 0600); err != nil {
		return Result{}
	}
	if before != nil {
		s.deletePrimary(ctx)
	}
	return Result{Success: true, Warning: "Warning: Storing credentials in plaintext."}
}
func (s *Store) Update(ctx context.Context, record protocol.JSONObject) Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() != nil {
		return Result{}
	}
	return s.update(ctx, record.Clone())
}

// Change serializes a complete read/modify/write of the shared record so an
// option save cannot overwrite an unrelated OAuth token within this process.
func (s *Store) Change(ctx context.Context, change func(protocol.JSONObject) error) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	value := s.read(ctx)
	if value == nil {
		empty := protocol.NewJSONObject()
		value = &empty
	}
	if err := change(*value); err != nil {
		return Result{}, err
	}
	return s.update(ctx, *value), nil
}
func (s *Store) Delete(ctx context.Context) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	primary := false
	if s.options.Platform == "darwin" {
		primary = s.deletePrimary(ctx)
	}
	_, path := s.plaintextLocation()
	err := os.Remove(path)
	return primary || err == nil || errors.Is(err, os.ErrNotExist)
}
func (s *Store) Clear() { s.mu.Lock(); defer s.mu.Unlock(); s.clear() }
