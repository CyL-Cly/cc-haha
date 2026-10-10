// Package uipreferences owns cc-haha desktop UI preferences and profile avatar
// persistence. It does not manage voice inference, pet execution or API auth.
package uipreferences

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/migrations"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

type Options struct {
	Store *storage.Store
	// Upgrader must be constructed from the same shared Store. Its real run is
	// required; there is no callback default or completed-migration placeholder.
	Upgrader  *migrations.Upgrader
	ConfigDir func() string
	Access    func(string) error
	Now       func() time.Time
	Random    io.Reader
	PID       int
}
type Service struct {
	options Options
	fs      storage.FileSystem
	rngMu   sync.Mutex
}
type ReadResult struct {
	Preferences Preferences `json:"preferences"`
	Exists      bool        `json:"exists"`
}
type Avatar struct {
	Bytes       []byte
	ContentType string
}
type avatarType struct{ mime, extension string }

var avatarTypes = []avatarType{{"image/png", "png"}, {"image/jpeg", "jpg"}, {"image/webp", "webp"}}

func New(options Options) (*Service, error) {
	if options.Store == nil || options.Upgrader == nil || options.ConfigDir == nil {
		return nil, errors.New("desktop UI preferences require a shared store, actual upgrader, and explicit config selector")
	}
	if options.Access == nil {
		options.Access = func(path string) error { _, err := os.Stat(path); return err }
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	if options.PID == 0 {
		options.PID = os.Getpid()
	}
	return &Service{options: options, fs: options.Store.FileSystem()}, nil
}
func badRequest(message string) error {
	return &settings.APIError{StatusCode: 400, Code: "BAD_REQUEST", Message: message}
}
func internal(message string, err error) error {
	return &settings.APIError{StatusCode: 500, Code: "INTERNAL_ERROR", Message: message + ": Error: " + err.Error()}
}
func (service *Service) configDir() (string, error) {
	config := service.options.ConfigDir()
	if config == "" {
		return "", errors.New("desktop UI preferences require an explicit config directory")
	}
	return config, nil
}
func (service *Service) preferencesPath() (string, error) {
	config, err := service.configDir()
	return filepath.Join(config, "cc-haha", "desktop-ui.json"), err
}
func (service *Service) profileDir() (string, error) {
	config, err := service.configDir()
	return filepath.Join(config, "cc-haha", "profile"), err
}
func (service *Service) exists(path string) (bool, error) {
	err := service.options.Access(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, internal("Failed to access desktop UI preferences", err)
}

func (service *Service) ReadPreferences(ctx context.Context) (ReadResult, error) {
	config, err := service.configDir()
	if err != nil {
		return ReadResult{}, err
	}
	if _, err := service.options.Upgrader.Ensure(ctx, config); err != nil {
		return ReadResult{}, err
	}
	path, err := service.preferencesPath()
	if err != nil {
		return ReadResult{}, err
	}
	before, err := service.exists(path)
	if err != nil {
		return ReadResult{}, err
	}
	preferences, err := storage.ReadRecoverableJSON(service.options.Store, storage.ReadOptions[Preferences]{Path: path, Label: "cc-haha desktop UI preferences", Default: defaults(), Normalize: normalize})
	if err != nil {
		var readError *storage.FileError
		if errors.As(err, &readError) {
			return ReadResult{}, internal(fmt.Sprintf("Failed to read %s from %s", readError.Label, readError.Path), readError.Err)
		}
		return ReadResult{}, err
	}
	after, err := service.exists(path)
	if err != nil {
		return ReadResult{}, err
	}
	return ReadResult{preferences, before && after}, nil
}
func (service *Service) tempPath(path string) (string, error) {
	timestamp := service.options.Now().UnixMilli()
	random := make([]byte, 6)
	service.rngMu.Lock()
	_, err := io.ReadFull(service.options.Random, random)
	service.rngMu.Unlock()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s.tmp.%d.%d.%s", path, service.options.PID, timestamp, hex.EncodeToString(random)), nil
}
func (service *Service) writePreferences(preferences Preferences) error {
	path, err := service.preferencesPath()
	if err != nil {
		return err
	}
	canonical, err := preferences.MarshalJSON()
	if err != nil {
		return err
	}
	var data bytes.Buffer
	if err := json.Indent(&data, canonical, "", "  "); err != nil {
		return err
	}
	data.WriteByte('\n')
	temporary, err := service.tempPath(path)
	if err != nil {
		return err
	}
	if err := service.fs.MkdirAll(filepath.Dir(path), 0777); err != nil {
		return err
	}
	if err := service.replace(temporary, path, data.Bytes()); err != nil {
		return internal("Failed to write desktop-ui.json", err)
	}
	return nil
}
func (service *Service) replace(temporary, path string, data []byte) error {
	err := service.fs.WriteFile(temporary, data, 0666)
	if err == nil {
		err = service.fs.Rename(temporary, path)
	}
	if err != nil {
		_ = service.fs.Remove(temporary)
	}
	return err
}

func (service *Service) update(ctx context.Context, task func(object) (object, error)) (Preferences, error) {
	path, err := service.preferencesPath()
	if err != nil {
		return nil, err
	}
	var result Preferences
	err = service.options.Store.WithWriteLock(path, func() error {
		read, err := service.ReadPreferences(ctx)
		if err != nil {
			return err
		}
		current, _ := parseObject(json.RawMessage(read.Preferences))
		next, err := task(current)
		if err != nil {
			return err
		}
		result = Preferences(next.raw())
		return service.writePreferences(result)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
func (service *Service) UpdateSidebarPreferences(ctx context.Context, patch json.RawMessage) (Preferences, error) {
	return service.update(ctx, func(current object) (object, error) {
		value, _ := parseObject(patch)
		previous, _ := parseObject(current.get("sidebar"))
		current.set("sidebar", normalizeSidebar(merge(previous, value).raw()).raw())
		return current, nil
	})
}
func (service *Service) UpdatePetPreferences(ctx context.Context, patch json.RawMessage) (Preferences, error) {
	return service.update(ctx, func(current object) (object, error) {
		value, _ := parseObject(patch)
		previous, _ := parseObject(current.get("pet"))
		current.set("pet", normalizePet(merge(previous, value).raw()).raw())
		return current, nil
	})
}
func (service *Service) UpdateVoiceInputPreferences(ctx context.Context, patch json.RawMessage) (Preferences, error) {
	return service.update(ctx, func(current object) (object, error) {
		value, _ := parseObject(patch)
		previous, _ := parseObject(current.get("voiceInput"))
		current.set("voiceInput", normalizeVoice(merge(previous, value).raw()).raw())
		return current, nil
	})
}
func validateProfile(raw json.RawMessage) (object, error) {
	patch, valid := parseObject(raw)
	if !valid {
		return nil, badRequest("Profile preferences must be an object")
	}
	for _, field := range []struct {
		name string
		max  int
	}{{"displayName", 80}, {"subtitle", 160}} {
		candidate := patch.get(field.name)
		if candidate == nil {
			continue
		}
		value, valid := units(candidate)
		if !valid {
			return nil, badRequest(field.name + " must be a string")
		}
		value = collapse(value)
		if len(value) == 0 || len(value) > field.max {
			return nil, badRequest(fmt.Sprintf("%s must be between 1 and %d characters", field.name, field.max))
		}
	}
	return patch, nil
}
func (service *Service) UpdateProfilePreferences(ctx context.Context, raw json.RawMessage) (Preferences, error) {
	return service.update(ctx, func(current object) (object, error) {
		patch, err := validateProfile(raw)
		if err != nil {
			return nil, err
		}
		profile := normalizeProfile(current.get("profile"))
		for _, key := range []string{"displayName", "subtitle"} {
			if value := patch.get(key); value != nil {
				profile.set(key, value)
			}
		}
		current.set("profile", normalizeProfile(profile.raw()).raw())
		return current, nil
	})
}
func validateProjectName(raw json.RawMessage) (json.RawMessage, json.RawMessage, error) {
	patch, valid := parseObject(raw)
	if !valid {
		return nil, nil, badRequest("Project display name update must be an object")
	}
	key := patch.get("projectKey")
	if !validProjectKey(key) {
		return nil, nil, badRequest("projectKey must be a non-empty string up to 4096 characters")
	}
	name := patch.get("displayName")
	if strings.TrimSpace(string(name)) == "null" {
		return key, json.RawMessage("null"), nil
	}
	normalized, valid := displayName(name)
	if !valid {
		return nil, nil, badRequest("displayName must be a non-empty string up to 80 characters")
	}
	return key, normalized, nil
}
func (service *Service) UpdateProjectDisplayName(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	key, name, err := validateProjectName(raw)
	if err != nil {
		return nil, err
	}
	keyUnits, _ := units(key)
	quotedKey := string(quotedUnits(keyUnits))
	_, err = service.update(ctx, func(current object) (object, error) {
		names := normalizeProjectNames(current.get("projectDisplayNames"))
		if string(name) == "null" {
			names.removeQuoted(quotedKey)
		} else {
			found := false
			for _, item := range names {
				if item.key == quotedKey {
					found = true
					break
				}
			}
			if !found && len(names) >= 2000 {
				return nil, badRequest("Project display names cannot exceed 2000 entries")
			}
			names.setQuoted(quotedKey, name)
		}
		current.set("projectDisplayNames", names.raw())
		return current, nil
	})
	if err != nil {
		return nil, err
	}
	result := object{}
	result.set("projectKey", json.RawMessage(quotedKey))
	result.set("displayName", name)
	return result.raw(), nil
}

func (service *Service) UpdateProfileAvatar(ctx context.Context, data []byte, contentType string) (Preferences, error) {
	mime := strings.TrimFunc(strings.SplitN(contentType, ";", 2)[0], func(value rune) bool { return value <= 0xffff && jsWhitespace(uint16(value)) })
	// Only ASCII case changes can produce one of the three supported MIME
	// strings. Go's simple Unicode fold would accept dotted capital I where
	// JS lowercasing produces i + combining dot and rejects the MIME type.
	mime = strings.Map(func(value rune) rune {
		if value >= 'A' && value <= 'Z' {
			return value + ('a' - 'A')
		}
		return value
	}, mime)
	var selected *avatarType
	for index := range avatarTypes {
		if avatarTypes[index].mime == mime {
			selected = &avatarTypes[index]
			break
		}
	}
	// The source MIME table is a plain JS object. These inherited keys reach
	// its later avatar-path validation after a read; they never permit an image.
	// Retain the error and recovery ordering instead of accepting either type.
	inheritedType := mime == "constructor" || mime == "__proto__"
	if selected == nil && !inheritedType {
		return nil, badRequest("Unsupported avatar type")
	}
	if len(data) > 2000000 {
		return nil, badRequest("Avatar image is too large")
	}
	return service.update(ctx, func(current object) (object, error) {
		dir, err := service.profileDir()
		if err != nil {
			return nil, err
		}
		if inheritedType {
			return nil, badRequest("Invalid avatar file path")
		}
		file := "profile/avatar." + selected.extension
		path := filepath.Join(dir, "avatar."+selected.extension)
		temporary, err := service.tempPath(path)
		if err != nil {
			return nil, err
		}
		if err := service.fs.MkdirAll(dir, 0777); err != nil {
			return nil, err
		}
		if err := service.replace(temporary, path, data); err != nil {
			return nil, internal("Failed to write profile avatar", err)
		}
		for _, candidate := range avatarTypes {
			if candidate.extension != selected.extension {
				_ = service.fs.Remove(filepath.Join(dir, "avatar."+candidate.extension))
			}
		}
		profile := normalizeProfile(current.get("profile"))
		profile.set("avatarFile", quoted(file))
		profile.set("avatarUpdatedAt", quoted(service.options.Now().UTC().Format("2006-01-02T15:04:05.000Z")))
		current.set("profile", profile.raw())
		return current, nil
	})
}
func (service *Service) ClearProfileAvatar(ctx context.Context) (Preferences, error) {
	return service.update(ctx, func(current object) (object, error) {
		dir, err := service.profileDir()
		if err != nil {
			return nil, err
		}
		for _, candidate := range avatarTypes {
			_ = service.fs.Remove(filepath.Join(dir, "avatar."+candidate.extension))
		}
		profile := normalizeProfile(current.get("profile"))
		profile.set("avatarFile", json.RawMessage("null"))
		profile.set("avatarUpdatedAt", json.RawMessage("null"))
		current.set("profile", profile.raw())
		return current, nil
	})
}
func (service *Service) ReadProfileAvatar(ctx context.Context) (*Avatar, error) {
	read, err := service.ReadPreferences(ctx)
	if err != nil {
		return nil, err
	}
	current, _ := parseObject(json.RawMessage(read.Preferences))
	profile := normalizeProfile(current.get("profile"))
	file := normalizedAvatar(profile.get("avatarFile"))
	if string(file) == "null" {
		return nil, nil
	}
	var path string
	_ = json.Unmarshal(file, &path)
	extension := filepath.Ext(path)[1:]
	var mime string
	for _, candidate := range avatarTypes {
		if candidate.extension == extension {
			mime = candidate.mime
			break
		}
	}
	if mime == "" {
		return nil, nil
	}
	config, err := service.configDir()
	if err != nil {
		return nil, err
	}
	data, err := service.fs.ReadFile(filepath.Join(config, "cc-haha", filepath.FromSlash(path)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, internal("Failed to read profile avatar", err)
	}
	return &Avatar{data, mime}, nil
}
