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
	"os/exec"
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
}
type Options struct {
	Home, ConfigDir, Platform string
	Environment               map[string]string
	ConfigDirExplicit         bool
	Now                       func() time.Time
	// RunSecurity is only the native security(1) I/O primitive. Tests use it to
	// avoid every lookup or mutation of the developer's actual Keychain.
	RunSecurity func(context.Context, Command) ([]byte, error)
}
type Store struct {
	mu                     sync.Mutex
	options                Options
	service, account, path string
	cachedJSON             json.RawMessage
	cachedAt               time.Time
	generation             uint64
	readInFlight           *sourceReadFlight
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
	if o.RunSecurity == nil {
		o.RunSecurity = func(ctx context.Context, request Command) ([]byte, error) {
			command := exec.CommandContext(ctx, "/usr/bin/security", request.Args...)
			command.Env = []string{}
			for k, v := range environment {
				command.Env = append(command.Env, k+"="+v)
			}
			command.Dir = o.Home
			if request.Input != "" {
				command.Stdin = strings.NewReader(request.Input)
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
	return &Store{options: o, service: ServiceName(o.ConfigDir, o.ConfigDirExplicit, environment), account: account, path: filepath.Join(o.ConfigDir, ".credentials.json")}, nil
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
func (s *Store) primary(ctx context.Context) *protocol.JSONObject {
	return parse(s.primarySourceJSON(ctx))
}
func (s *Store) read(ctx context.Context) *protocol.JSONObject {
	if s.options.Platform == "darwin" {
		if value := s.primary(ctx); value != nil {
			return value
		}
	}
	data, err := os.ReadFile(s.path)
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
	_, err := s.options.RunSecurity(ctx, Command{Args: []string{"delete-generic-password", "-a", s.account, "-s", s.service}})
	return err == nil
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
		line := "add-generic-password -U -a \"" + s.account + "\" -s \"" + s.service + "\" -X \"" + payload + "\"\n"
		command := Command{Args: []string{"-i"}, Input: line}
		if len(utf16.Encode([]rune(line))) > 4032 {
			command = Command{Args: []string{"add-generic-password", "-U", "-a", s.account, "-s", s.service, "-X", payload}}
		}
		if _, err := s.options.RunSecurity(ctx, command); err == nil {
			s.cachedJSON, s.cachedAt = append(json.RawMessage(nil), data...), s.options.Now()
			if before == nil {
				_ = os.Remove(s.path)
			}
			return Result{Success: true}
		}
	}
	// Source mkdir is one level, accepts EEXIST, then writes/chmods the
	// ordinary credential file. A failed save never reports success.
	if err := os.Mkdir(s.options.ConfigDir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return Result{}
	}
	if err := os.WriteFile(s.path, data, 0600); err != nil {
		return Result{}
	}
	if err := os.Chmod(s.path, 0600); err != nil {
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
	err := os.Remove(s.path)
	return primary || err == nil || errors.Is(err, os.ErrNotExist)
}
func (s *Store) Clear() { s.mu.Lock(); defer s.mu.Unlock(); s.clear() }
