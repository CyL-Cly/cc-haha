// Package climigrations implements main.tsx's v11 synchronous release sequence
// using the CLI persistence services. It does not initialize or dispatch CLI.
package climigrations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

const ExternalEmptyOAuthProfile = "external-classifier-empty-oauth-no-growthbook-v11"

type CapabilityError struct{ Capability string }

func (e *CapabilityError) Error() string {
	return "unsupported CLI migration capability: " + e.Capability
}

type Host struct {
	Profile, OriginalCWD string
	Environment          map[string]string
	EmptyCredentialStore bool
	ModelOverride        json.RawMessage
}
type Event map[string]any
type FileSystem interface {
	Stat(string) (os.FileInfo, error)
	MkdirAll(string) error
	WriteExclusive(string, []byte) error
}
type OSFileSystem struct{}

func (OSFileSystem) Stat(path string) (os.FileInfo, error) { return os.Stat(path) }
func (OSFileSystem) MkdirAll(path string) error            { return os.MkdirAll(path, 0777) }
func (OSFileSystem) WriteExclusive(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0666)
	if err != nil {
		return err
	}
	_, err = file.Write(data)
	closed := file.Close()
	if err == nil {
		err = closed
	}
	return err
}

type Options struct {
	Global     *cliglobalconfig.Service
	Settings   *clisettings.Service
	Host       Host
	ConfigHome string
	Now        func() float64
	FS         FileSystem
	Observe    func(Event)
}
type Service struct {
	options            Options
	native             *nativeConsumer
	mu                 sync.Mutex
	projectKey         string
	override           json.RawMessage
	disableAutoUpdater *string
}
type ChangelogTask struct {
	done chan struct{}
	err  error
}

func (task *ChangelogTask) Wait() error {
	if task == nil {
		return nil
	}
	<-task.done
	return task.err
}
func New(options Options) (*Service, error) {
	if options.Global == nil || options.Settings == nil || options.Now == nil || !filepath.IsAbs(options.ConfigHome) || !filepath.IsAbs(options.Host.OriginalCWD) {
		return nil, errors.New("CLI migrations require persistence, original paths and clock")
	}
	if options.Host.Profile != ExternalEmptyOAuthProfile || !options.Host.EmptyCredentialStore {
		return nil, &CapabilityError{"resolved external empty-credential host"}
	}
	for key := range options.Host.Environment {
		if key != "CLAUDE_CODE_DISABLE_LEGACY_MODEL_REMAP" && key != "CLAUDE_CODE_DISABLE_1M_CONTEXT" {
			return nil, &CapabilityError{"environment " + key}
		}
	}
	if raw := options.Host.ModelOverride; raw != nil {
		if !json.Valid(raw) || len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '"' && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil, &CapabilityError{"model override JSON string"}
		}
	}
	if options.FS == nil {
		options.FS = OSFileSystem{}
	}
	return &Service{options: options, override: append(json.RawMessage(nil), options.Host.ModelOverride...)}, nil
}
func (s *Service) ModelOverride() json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append(json.RawMessage(nil), s.override...)
}
func (s *Service) DisableAutoUpdater() *string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.disableAutoUpdater == nil {
		return nil
	}
	value := *s.disableAutoUpdater
	return &value
}
func (s *Service) emit(event Event) {
	if s.options.Observe != nil {
		s.options.Observe(event)
	}
	if s.native != nil && s.native.failure == nil {
		switch event["kind"] {
		case "event":
			name, _ := event["name"].(string)
			raw, err := json.Marshal(event["data"])
			metadata := protocol.NewJSONObject()
			if err == nil {
				err = metadata.UnmarshalJSON(raw)
			}
			if err == nil {
				err = s.native.options.Event(s.native.scope, name, metadata)
			}
			s.native.failure = err
		case "error":
			message, _ := event["message"].(string)
			s.native.failure = s.native.options.LogError(s.native.scope, errors.New(strings.TrimPrefix(message, "Error: ")))
		}
	}
}
func (s *Service) Run() (*ChangelogTask, error) {
	if s == nil || s.native != nil {
		return nil, &CapabilityError{"native migration requires owned RunNative"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.run()
}
func (s *Service) run() (*ChangelogTask, error) {
	config, err := s.options.Global.Get()
	if err != nil {
		return nil, err
	}
	if !numericEleven(config.Get("migrationVersion")) {
		steps := []struct {
			name string
			run  func() error
		}{
			{"migrateAutoUpdatesToSettings", s.autoUpdates}, {"migrateBypassPermissionsAcceptedToSettings", s.bypass},
			{"migrateEnableAllProjectMcpServersToSettings", s.mcp}, {"resetProToOpusDefault", s.proDefault},
			{"migrateSonnet1mToSonnet45", s.sonnet1m}, {"migrateLegacyOpusToCurrent", s.legacyOpus},
			{"migrateSonnet45ToSonnet46", s.sonnet45}, {"migrateOpusToOpus1m", s.opus1m},
			{"migrateReplBridgeEnabledToRemoteControlAtStartup", s.remoteControl}, {"resetAutoModeOptInForDefaultOffer", s.autoOffer},
		}
		for _, step := range steps {
			s.emit(Event{"kind": "migrationStart", "name": step.name})
			if err = step.run(); err != nil {
				return nil, err
			}
			if s.native != nil && s.native.failure != nil {
				return nil, s.native.failure
			}
			s.emit(Event{"kind": "migrationEnd", "name": step.name})
		}
		if err = s.save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
			if numericEleven(current.Get("migrationVersion")) {
				return current, nil
			}
			next := current.Clone()
			next.Set("migrationVersion", []byte("11"))
			return &next, nil
		}); err != nil {
			return nil, err
		}
	}
	return s.startChangelog()
}
func (s *Service) resolveProject() error {
	if s.projectKey != "" {
		return nil
	}
	if s.native != nil {
		key, err := s.native.options.ProjectKey(s.native.scope)
		if err != nil {
			return err
		}
		if !filepath.IsAbs(key) {
			return &CapabilityError{"native canonical project identity"}
		}
		s.projectKey = key
		return nil
	}
	path := filepath.Clean(s.options.Host.OriginalCWD)
	for current := path; ; current = filepath.Dir(current) {
		if info, err := s.options.FS.Stat(filepath.Join(current, ".git")); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			return &CapabilityError{"Git project canonicalization"}
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	s.projectKey = filepath.ToSlash(path)
	return nil
}

func numericEleven(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] == '"' {
		return false
	}
	value, err := strconv.ParseFloat(string(raw), 64)
	return err == nil && value == 11
}
func truthy(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) || bytes.Equal(raw, []byte("false")) {
		return false
	}
	if raw[0] == '"' {
		units, _ := protocol.DecodeJavaScriptJSONString(raw)
		return len(units) > 0
	}
	if raw[0] == '{' || raw[0] == '[' || bytes.Equal(raw, []byte("true")) {
		return true
	}
	value, _ := strconv.ParseFloat(string(raw), 64)
	return value != 0 && !math.IsNaN(value)
}
func equalString(raw json.RawMessage, text string) bool {
	encoded, _ := json.Marshal(text)
	return raw != nil && bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(raw), encoded)
}
func envTruthy(value string) bool {
	value = strings.TrimFunc(value, func(r rune) bool {
		return r == '\t' || r == '\n' || r == '\v' || r == '\f' || r == '\r' || r == ' ' || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
	})
	switch strings.ToLower(value) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
func objectWire(object *protocol.JSONObject) string {
	raw, _ := object.MarshalJSON()
	return string(protocol.StringifyValidatedJavaScriptJSON(raw))
}
func errorText(err error) string { return "Error: " + err.Error() }
func (s *Service) event(name string, data map[string]any) {
	s.emit(Event{"kind": "event", "name": name, "data": data})
}
func (s *Service) save(updater cliglobalconfig.Updater) error {
	s.emit(Event{"kind": "saveGlobal"})
	return s.options.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
		before := objectWire(current)
		next, err := updater(current)
		if err == nil {
			s.emit(Event{"kind": "updaterGlobal", "before": before, "after": objectWire(next), "same": next == current})
		}
		return next, err
	})
}
func (s *Service) saveProject(updater cliglobalconfig.Updater) error {
	s.emit(Event{"kind": "saveProject"})
	return s.options.Global.SaveProject(s.projectKey, func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
		before := objectWire(current)
		next, err := updater(current)
		if err == nil {
			s.emit(Event{"kind": "updaterProject", "before": before, "after": objectWire(next), "same": next == current})
		}
		return next, err
	})
}
func (s *Service) settings(source string) (*protocol.JSONObject, error) {
	if s.native != nil {
		value, err := s.native.options.Sources.Source(s.native.scope, source)
		if value == nil && err == nil {
			empty := protocol.NewJSONObject()
			value = &empty
		}
		return value, err
	}
	value, err := s.options.Settings.GetEditableSource(source, "")
	if value == nil && err == nil {
		empty := protocol.NewJSONObject()
		value = &empty
	}
	return value, err
}
func (s *Service) update(source string, patch protocol.JSONObject) error {
	undefined := []string{}
	for _, property := range patch.OwnProperties() {
		if property.Value == nil {
			var key string
			_ = json.Unmarshal(property.NameJSON, &key)
			undefined = append(undefined, key)
		}
	}
	s.emit(Event{"kind": "settingsUpdate", "source": source, "patch": objectWire(&patch), "undefinedKeys": undefined})
	var result clisettings.WriteResult
	var err error
	if s.native != nil {
		update := clisettings.Patch{Object: patch}
		for _, property := range patch.OwnProperties() {
			if property.Value == nil {
				update.UndefinedPaths = append(update.UndefinedPaths, []json.RawMessage{property.NameJSON})
			}
		}
		result, err = s.native.options.Sources.Update(s.native.scope, source, update, s.native.options.Writer)
	} else {
		result, err = s.options.Settings.UpdateEditableSource(source, "", clisettings.Patch{Object: patch})
	}
	if err != nil {
		return err
	}
	var message any
	if result.Error != nil {
		message = errorText(result.Error)
	}
	s.emit(Event{"kind": "settingsResult", "error": message})
	return nil
}
func (s *Service) caught(err error, prefix, event string, data map[string]any) error {
	var capability *clisettings.CapabilityError
	var own *CapabilityError
	var startup *clistartupenv.CapabilityError
	if errors.As(err, &capability) || errors.As(err, &own) || errors.As(err, &startup) || errors.Is(err, cooperative.ErrRestricted) || errors.Is(err, cooperative.ErrClosed) || errors.Is(err, context.Canceled) {
		return err
	}
	message := errorText(err)
	if prefix != "" {
		message = "Error: " + prefix + message
	}
	s.emit(Event{"kind": "error", "message": message})
	if event != "" {
		s.event(event, data)
	}
	return nil
}
func (s *Service) autoUpdates() error {
	config, err := s.options.Global.Get()
	if err != nil {
		return err
	}
	if !bytes.Equal(config.Get("autoUpdates"), []byte("false")) || bytes.Equal(config.Get("autoUpdatesProtectedForNative"), []byte("true")) {
		return nil
	}
	user, err := s.settings("userSettings")
	if err == nil {
		patch := user.Clone()
		env := protocol.NewJSONObject()
		if raw := user.Get("env"); raw != nil {
			_ = env.UnmarshalJSON(raw)
		}
		had := truthy(env.Get("DISABLE_AUTOUPDATER"))
		env.Set("DISABLE_AUTOUPDATER", []byte(`"1"`))
		encoded, _ := env.MarshalJSON()
		patch.Set("env", encoded)
		err = s.update("userSettings", patch)
		if err == nil {
			s.event("tengu_migrate_autoupdates_to_settings", map[string]any{"was_user_preference": true, "already_had_env_var": had})
			value := "1"
			s.disableAutoUpdater = &value
			err = s.save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
				next := current.Clone()
				next.Delete("autoUpdates")
				next.Delete("autoUpdatesProtectedForNative")
				return &next, nil
			})
		}
	}
	if err != nil {
		return s.caught(err, "Failed to migrate auto-updates: ", "tengu_migrate_autoupdates_error", map[string]any{"has_error": true})
	}
	return nil
}
func (s *Service) bypass() error {
	config, err := s.options.Global.Get()
	if err != nil {
		return err
	}
	if !truthy(config.Get("bypassPermissionsModeAccepted")) {
		return nil
	}
	accepted := false
	sources := []string{"userSettings", "localSettings"}
	if s.native != nil {
		sources = append(sources, "flagSettings", "policySettings")
	}
	for _, source := range sources {
		value, e := s.settings(source)
		if e != nil {
			return s.caught(e, "Failed to migrate bypass permissions accepted: ", "", nil)
		}
		if truthy(value.Get("skipDangerousModePermissionPrompt")) {
			accepted = true
			break
		}
	}
	// This profile has no flagSettings or managed policy; they are external
	// resolved capabilities, not arbitrary empty callbacks or merged settings.
	if !accepted {
		patch := protocol.NewJSONObject(protocol.JSONField{Name: "skipDangerousModePermissionPrompt", Value: []byte("true")})
		err = s.update("userSettings", patch)
	}
	if err == nil {
		s.event("tengu_migrate_bypass_permissions_accepted", map[string]any{})
		err = s.save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
			if current.Get("bypassPermissionsModeAccepted") == nil {
				return current, nil
			}
			next := current.Clone()
			next.Delete("bypassPermissionsModeAccepted")
			return &next, nil
		})
	}
	if err != nil {
		return s.caught(err, "Failed to migrate bypass permissions accepted: ", "", nil)
	}
	return nil
}
func (s *Service) proDefault() error {
	if s.native != nil {
		return s.nativeProDefault()
	}
	config, err := s.options.Global.Get()
	if err != nil {
		return err
	}
	if truthy(config.Get("opusProMigrationComplete")) {
		return nil
	}
	err = s.save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
		next := current.Clone()
		next.Set("opusProMigrationComplete", []byte("true"))
		return &next, nil
	})
	if err == nil {
		s.event("tengu_reset_pro_to_opus_default", map[string]any{"skipped": true})
	}
	return err
}
func (s *Service) sonnet1m() error {
	config, err := s.options.Global.Get()
	if err != nil {
		return err
	}
	if truthy(config.Get("sonnet1m45MigrationComplete")) {
		return nil
	}
	user, err := s.settings("userSettings")
	if err != nil {
		return err
	}
	if equalString(user.Get("model"), "sonnet[1m]") {
		err = s.update("userSettings", protocol.NewJSONObject(protocol.JSONField{Name: "model", Value: []byte(`"sonnet-4-5-20250929[1m]"`)}))
		if err != nil {
			return err
		}
	}
	if equalString(s.override, "sonnet[1m]") {
		s.override = []byte(`"sonnet-4-5-20250929[1m]"`)
	}
	return s.save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
		next := current.Clone()
		next.Set("sonnet1m45MigrationComplete", []byte("true"))
		return &next, nil
	})
}
func (s *Service) legacyOpus() error {
	environment := s.options.Host.Environment
	if s.native != nil {
		if !s.nativeFirstParty() {
			return nil
		}
		environment = s.native.options.Environment()
	}
	if envTruthy(environment["CLAUDE_CODE_DISABLE_LEGACY_MODEL_REMAP"]) {
		return nil
	}
	user, err := s.settings("userSettings")
	if err != nil {
		return err
	}
	model := user.Get("model")
	legacy := false
	for _, value := range []string{"claude-opus-4-20250514", "claude-opus-4-1-20250805", "claude-opus-4-0", "claude-opus-4-1"} {
		if equalString(model, value) {
			legacy = true
		}
	}
	if !legacy {
		return nil
	}
	if err = s.update("userSettings", protocol.NewJSONObject(protocol.JSONField{Name: "model", Value: []byte(`"opus"`)})); err != nil {
		return err
	}
	if err = s.save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
		next := current.Clone()
		next.Set("legacyOpusMigrationTimestamp", []byte(strconv.FormatFloat(s.options.Now(), 'f', -1, 64)))
		return &next, nil
	}); err != nil {
		return err
	}
	var name any
	_ = json.Unmarshal(model, &name)
	s.event("tengu_legacy_opus_migration", map[string]any{"from_model": name})
	return nil
}

// Empty native credential snapshot means all three subscriber predicates are
// false. The OAuth/subscriber execution branch is deliberately not admitted.
func (s *Service) sonnet45() error {
	if s.native != nil {
		return s.nativeSonnet45()
	}
	return nil
}
func (s *Service) opus1m() error {
	if s.native != nil {
		return s.nativeOpus1m()
	}
	if envTruthy(s.options.Host.Environment["CLAUDE_CODE_DISABLE_1M_CONTEXT"]) {
		return nil
	}
	user, err := s.settings("userSettings")
	if err != nil {
		return err
	}
	if !equalString(user.Get("model"), "opus") {
		return nil
	}
	if err = s.update("userSettings", protocol.NewJSONObject(protocol.JSONField{Name: "model", Value: []byte(`"opus[1m]"`)})); err != nil {
		return err
	}
	s.event("tengu_opus_to_opus1m_migration", map[string]any{})
	return nil
}
func (s *Service) remoteControl() error {
	return s.save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
		old := current.Get("replBridgeEnabled")
		if old == nil || current.Get("remoteControlAtStartup") != nil {
			return current, nil
		}
		next := current.Clone()
		raw := []byte("false")
		if truthy(old) {
			raw = []byte("true")
		}
		next.Set("remoteControlAtStartup", raw)
		next.Delete("replBridgeEnabled")
		return &next, nil
	})
}

// In the external fork getGrowthBookClientKey() is an empty literal, so the
// actual getter returns opt-in even if disk cache says enabled. No remote
// payload/config override is silently accepted as an enabled auto-mode host.
func (s *Service) autoOffer() error {
	if s.native != nil {
		return s.nativeAutoOffer()
	}
	return nil
}
