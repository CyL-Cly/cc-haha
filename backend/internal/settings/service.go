// Package settings owns the desktop server's user, project and local settings
// business rules. Runtime managed-env filtering is a separate boundary.
package settings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

var validPermissionModes = []string{"default", "acceptEdits", "plan", "bypassPermissions", "dontAsk", "auto"}

func ValidPermissionModes() []string { return append([]string(nil), validPermissionModes...) }

func IsValidPermissionMode(mode string) bool {
	for _, valid := range validPermissionModes {
		if mode == valid {
			return true
		}
	}
	return false
}

// APIError retains the existing status/code/message contract for route callers.
type APIError struct {
	StatusCode int
	Code       string
	Message    string
}

func (err *APIError) Error() string { return err.Message }

func badRequest(message string) error {
	return &APIError{StatusCode: 400, Code: "BAD_REQUEST", Message: message}
}

type Options struct {
	Store       *storage.Store
	HomeDir     string
	ConfigDir   string
	ProjectRoot string
	// Environment returns an explicitly selected process environment. The
	// service never consults os.Getenv or discovers the current user's home.
	Environment func() map[string]string
	// EnsureMigrated is mandatory. The application bootstrap must supply the
	// real upgrader; constructing this service does not complete migrations.
	EnsureMigrated   func(context.Context, string) error
	ShellEnvironment func(context.Context) (map[string]string, error)
	InvalidateCache  func()
	GitIgnore        func(context.Context, string, string) error
	GitExecutable    string
	LogError         func(error)
}

type Service struct {
	options    Options
	background sync.WaitGroup
}

func New(options Options) (*Service, error) {
	if err := validateBootstrap(options); err != nil {
		return nil, err
	}
	if options.ShellEnvironment == nil {
		return nil, errors.New("settings bootstrap requires shared storage, explicit home/environment, migration and shell-environment callbacks")
	}
	if options.LogError == nil {
		options.LogError = func(err error) { fmt.Fprintln(os.Stderr, err) }
	}
	if options.GitIgnore == nil {
		gitIgnore, err := NewGitIgnore(GitIgnoreOptions{HomeDir: options.HomeDir, Environment: options.Environment, Executable: options.GitExecutable})
		if err != nil {
			return nil, err
		}
		options.GitIgnore = gitIgnore.AddFileGlobRule
	}
	return &Service{options: options}, nil
}

func validateBootstrap(options Options) error {
	if options.Store == nil || options.HomeDir == "" || options.Environment == nil || options.EnsureMigrated == nil {
		return errors.New("settings bootstrap requires shared storage, explicit home/environment and migration callback")
	}
	return nil
}

func (service *Service) configDir() string {
	if service.options.ConfigDir != "" {
		return service.options.ConfigDir
	}
	if configured := environmentValue(service.options.Environment(), "CLAUDE_CONFIG_DIR"); configured != "" {
		return configured
	}
	return filepath.Join(service.options.HomeDir, ".claude")
}

func (service *Service) projectRoot(root, scope string) (string, error) {
	if root == "" {
		root = service.options.ProjectRoot
	}
	if root == "" {
		return "", badRequest("Project root is required for " + scope + " settings")
	}
	return root, nil
}

func (service *Service) readJSON(ctx context.Context, path, configDir string) (storage.Object, error) {
	if err := service.options.EnsureMigrated(ctx, configDir); err != nil {
		return nil, err
	}
	return storage.ReadRecoverableJSON(service.options.Store, storage.ReadOptions[storage.Object]{Path: path, Label: "settings", Default: storage.Object{}, Normalize: storage.NormalizeObject})
}

func (service *Service) writeJSON(path string, value storage.Object) error {
	serialized, err := marshalJavaScriptSettings(value)
	if err != nil {
		return err
	}
	if err := service.options.Store.AtomicWriteJSON(path, "settings", serialized, storage.WriteOptions{RetryMissingParent: true}); err != nil {
		return err
	}
	if service.options.InvalidateCache != nil {
		service.options.InvalidateCache()
	}
	return nil
}

// JSON.parse in the legacy services produces IEEE-754 numbers; JSON.stringify
// rounds them and emits null for overflow. Apply that rule only at the save
// boundary, retaining raw values during decisions (Infinity is still truthy).
func marshalJavaScriptSettings(value storage.Object) (json.RawMessage, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	serialized, err := protocol.ParseAndStringifyJavaScriptJSON(raw)
	return json.RawMessage(serialized), err
}

func (service *Service) GetUserSettings(ctx context.Context) (storage.Object, error) {
	config := service.configDir()
	return service.readJSON(ctx, filepath.Join(config, "settings.json"), config)
}

func (service *Service) GetProjectSettings(ctx context.Context, root string) (storage.Object, error) {
	root, err := service.projectRoot(root, "project")
	if err != nil {
		return nil, err
	}
	return service.readJSON(ctx, filepath.Join(root, ".claude", "settings.json"), service.configDir())
}

func (service *Service) GetLocalSettings(ctx context.Context, root string) (storage.Object, error) {
	root, err := service.projectRoot(root, "local")
	if err != nil {
		return nil, err
	}
	return service.readJSON(ctx, filepath.Join(root, ".claude", "settings.local.json"), service.configDir())
}

// GetSettings intentionally merges only user and project settings. Local
// settings are included explicitly by callers such as the output-style route.
func (service *Service) GetSettings(ctx context.Context, root string) (storage.Object, error) {
	user, err := service.GetUserSettings(ctx)
	if err != nil {
		return nil, err
	}
	project, err := service.GetProjectSettings(ctx, root)
	if err != nil {
		return user, nil
	}
	return shallowMerge(user, project), nil
}

func (service *Service) GetAgentTeamsEnabled(ctx context.Context) (bool, error) {
	user, err := service.GetUserSettings(ctx)
	if err != nil {
		return false, err
	}
	if flag := bytes.TrimSpace(user["agentTeamsEnabled"]); bytes.Equal(flag, []byte("true")) || bytes.Equal(flag, []byte("false")) {
		return bytes.Equal(flag, []byte("true")), nil
	}
	config := service.configDir()
	managed, err := service.readJSON(ctx, filepath.Join(config, "cc-haha", "settings.json"), config)
	if err != nil {
		return false, err
	}
	// TS awaits shell env even when an earlier source contains a legacy value.
	inherited, err := service.options.ShellEnvironment(ctx)
	if err != nil {
		return false, err
	}
	key := "CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS"
	legacy := objectValue(managed["env"])[key]
	if isNullish(legacy) {
		legacy = objectValue(user["env"])[key]
	}
	if isNullish(legacy) {
		if value, exists := inherited[key]; exists {
			legacy = stringJSON(value)
		}
	}
	var value string
	if len(legacy) != 0 && legacy[0] == '"' && json.Unmarshal(legacy, &value) == nil {
		return isEnvTruthy(value), nil
	}
	return true, nil
}

func (service *Service) UpdateUserSettings(ctx context.Context, patch storage.Object) error {
	if err := validatePatch(patch); err != nil {
		return err
	}
	if value, exists := patch["agentTeamsEnabled"]; exists {
		flag := bytes.TrimSpace(value)
		if !bytes.Equal(flag, []byte("true")) && !bytes.Equal(flag, []byte("false")) {
			return badRequest("agentTeamsEnabled must be a boolean")
		}
	}
	config := service.configDir()
	path := filepath.Join(config, "settings.json")
	return service.options.Store.WithWriteLock(path, func() error {
		current, err := service.readJSON(ctx, path, config)
		if err != nil {
			return err
		}
		merged := shallowMerge(current, patch)
		oldTerminal, newTerminal := objectValue(current["desktopTerminal"]), objectValue(patch["desktopTerminal"])
		if oldTerminal != nil && newTerminal != nil {
			merged["desktopTerminal"] = objectJSON(shallowMerge(oldTerminal, newTerminal))
		}
		return service.writeJSON(path, merged)
	})
}

// UpdateEnabledPlugin owns the read/merge/save transaction used by plugin
// operations. Nil deletes only the selected key; an explicit false remains a
// disabled plugin. It shares the same settings writer and invalidation hook as
// ordinary API settings changes, without a second user-state file.
func (service *Service) UpdateEnabledPlugin(ctx context.Context, id string, enabled *bool) error {
	config := service.configDir()
	path := filepath.Join(config, "settings.json")
	return service.options.Store.WithWriteLock(path, func() error {
		current, err := service.readJSON(ctx, path, config)
		if err != nil {
			return err
		}
		plugins := shallowMerge(objectValue(current["enabledPlugins"]))
		if enabled == nil {
			delete(plugins, id)
		} else {
			plugins[id] = json.RawMessage("false")
			if *enabled {
				plugins[id] = json.RawMessage("true")
			}
		}
		current["enabledPlugins"] = objectJSON(plugins)
		return service.writeJSON(path, current)
	})
}

type ModelMapping struct {
	Main   string `json:"main"`
	Fable  string `json:"fable,omitempty"`
	Haiku  string `json:"haiku"`
	Sonnet string `json:"sonnet"`
	Opus   string `json:"opus"`
}

func (service *Service) UpdateOfficialModelMapping(ctx context.Context, models ModelMapping) error {
	config := service.configDir()
	path := filepath.Join(config, "settings.json")
	return service.options.Store.WithWriteLock(path, func() error {
		current, err := service.readJSON(ctx, path, config)
		if err != nil {
			return err
		}
		env := shallowMerge(objectValue(current["env"]), storage.Object{
			"ANTHROPIC_MODEL": stringJSON(models.Main), "ANTHROPIC_DEFAULT_HAIKU_MODEL": stringJSON(models.Haiku),
			"ANTHROPIC_DEFAULT_SONNET_MODEL": stringJSON(models.Sonnet), "ANTHROPIC_DEFAULT_OPUS_MODEL": stringJSON(models.Opus),
		})
		if models.Fable != "" {
			env["ANTHROPIC_DEFAULT_FABLE_MODEL"] = stringJSON(models.Fable)
		} else {
			delete(env, "ANTHROPIC_DEFAULT_FABLE_MODEL")
		}
		next := shallowMerge(current, storage.Object{"model": stringJSON(models.Main), "env": objectJSON(env)})
		delete(next, "modelContext")
		return service.writeJSON(path, next)
	})
}

func (service *Service) updateProjectFile(ctx context.Context, root, name string, patch storage.Object) error {
	config := service.configDir()
	path := filepath.Join(root, ".claude", name)
	return service.options.Store.WithWriteLock(path, func() error {
		current, err := service.readJSON(ctx, path, config)
		if err != nil {
			return err
		}
		return service.writeJSON(path, shallowMerge(current, patch))
	})
}

func (service *Service) UpdateProjectSettings(ctx context.Context, patch storage.Object, root string) error {
	if err := validatePatch(patch); err != nil {
		return err
	}
	root, err := service.projectRoot(root, "project")
	if err != nil {
		return err
	}
	return service.updateProjectFile(ctx, root, "settings.json", patch)
}

func (service *Service) UpdateLocalSettings(ctx context.Context, patch storage.Object, root string) error {
	if err := validatePatch(patch); err != nil {
		return err
	}
	root, err := service.projectRoot(root, "local")
	if err != nil {
		return err
	}
	if err := service.updateProjectFile(ctx, root, "settings.local.json", patch); err != nil {
		return err
	}
	service.background.Add(1)
	go func() {
		defer service.background.Done()
		if err := service.options.GitIgnore(context.WithoutCancel(ctx), ".claude/settings.local.json", root); err != nil {
			service.options.LogError(err)
		}
	}()
	return nil
}

// DrainBackground is a lifecycle hook for tests and shutdown after callers stop
// submitting writes. The settings save itself never awaits gitignore work.
func (service *Service) DrainBackground() { service.background.Wait() }

// A nil patch clears only model/effort; an omitted field is unchanged; JSON null
// clears that field. Future per-agent fields and sibling entries are preserved.
func (service *Service) UpdateBuiltInAgentOverride(ctx context.Context, agentType string, patch storage.Object) error {
	if err := validatePatch(patch); err != nil {
		return err
	}
	config := service.configDir()
	path := filepath.Join(config, "settings.json")
	return service.options.Store.WithWriteLock(path, func() error {
		current, err := service.readJSON(ctx, path, config)
		if err != nil {
			return err
		}
		overrides := shallowMerge(objectValue(current["builtInAgentOverrides"]))
		entry := shallowMerge(objectValue(overrides[agentType]))
		for _, field := range []string{"model", "effort"} {
			if patch == nil {
				delete(entry, field)
			} else if value, exists := patch[field]; exists {
				if isNullish(value) {
					delete(entry, field)
				} else {
					entry[field] = value
				}
			}
		}
		if len(entry) == 0 {
			delete(overrides, agentType)
		} else {
			overrides[agentType] = objectJSON(entry)
		}
		merged := shallowMerge(current)
		if len(overrides) == 0 {
			delete(merged, "builtInAgentOverrides")
		} else {
			merged["builtInAgentOverrides"] = objectJSON(overrides)
		}
		return service.writeJSON(path, merged)
	})
}

func (service *Service) GetPermissionMode(ctx context.Context) (string, error) {
	value, err := service.GetUserSettings(ctx)
	if err != nil {
		return "", err
	}
	var mode string
	if json.Unmarshal(value["defaultMode"], &mode) == nil && IsValidPermissionMode(mode) {
		return mode, nil
	}
	return "default", nil
}

func (service *Service) SetPermissionMode(ctx context.Context, mode string) error {
	if !IsValidPermissionMode(mode) {
		return badRequest(fmt.Sprintf("Invalid permission mode: \"%s\". Valid modes: %s", mode, strings.Join(validPermissionModes, ", ")))
	}
	return service.UpdateUserSettings(ctx, storage.Object{"defaultMode": stringJSON(mode)})
}

func shallowMerge(objects ...storage.Object) storage.Object {
	merged := storage.Object{}
	for _, object := range objects {
		for key, value := range object {
			if value == nil {
				// A nil RawMessage is the Go caller's explicit undefined value;
				// JSON null remains a distinct persisted value.
				delete(merged, key)
			} else {
				merged[key] = value
			}
		}
	}
	return merged
}

func validatePatch(patch storage.Object) error {
	for key, value := range patch {
		if value != nil && !json.Valid(value) {
			return fmt.Errorf("settings patch contains invalid JSON for %s", key)
		}
	}
	return nil
}

func objectValue(raw json.RawMessage) storage.Object {
	object, valid, err := storage.NormalizeObject(raw)
	if err != nil || !valid {
		return nil
	}
	return object
}

func objectJSON(object storage.Object) json.RawMessage {
	// All values originate in valid persisted JSON or a structured route patch.
	raw, _ := json.Marshal(object)
	return raw
}

func stringJSON(value string) json.RawMessage {
	raw, _ := json.Marshal(value)
	return raw
}

func isNullish(value json.RawMessage) bool {
	return len(value) == 0 || bytes.Equal(bytes.TrimSpace(value), []byte("null"))
}

func isEnvTruthy(value string) bool {
	trimmed := strings.TrimFunc(strings.ToLower(value), func(character rune) bool {
		return character >= '\t' && character <= '\r' || character == ' ' || character == '\u00a0' || character == '\u1680' ||
			character >= '\u2000' && character <= '\u200a' || character == '\u2028' || character == '\u2029' ||
			character == '\u202f' || character == '\u205f' || character == '\u3000' || character == '\ufeff'
	})
	return trimmed == "1" || trimmed == "true" || trimmed == "yes" || trimmed == "on"
}
