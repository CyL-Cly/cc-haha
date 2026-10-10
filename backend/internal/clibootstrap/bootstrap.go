// Package clibootstrap implements the settings flags processed before Commander
// registration and init. It does not run init, merge settings, or run handlers.
package clibootstrap

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type State struct {
	FlagSettingsPath      *string  `json:"flagSettingsPath"`
	AllowedSettingSources []string `json:"allowedSettingSources"`
	EnabledSettingSources []string `json:"enabledSettingSources"`
}
type Event struct {
	Operation string `json:"operation"`
	Value     any    `json:"value,omitempty"`
}
type Options struct {
	// WorkingDir and TempDir are explicit runtime paths. Production callers use
	// the invocation cwd and os.TempDir(); fixtures supply private directories.
	WorkingDir   string
	TempDir      string
	InitialState *State
}
type Result struct {
	State    State   `json:"state"`
	Events   []Event `json:"events"`
	ExitCode int     `json:"exitCode"`
	Stdout   string  `json:"stdout"`
	Stderr   string  `json:"stderr"`
}

func DefaultState() State {
	allowed := []string{"userSettings", "projectSettings", "localSettings", "flagSettings", "policySettings"}
	return State{AllowedSettingSources: allowed, EnabledSettingSources: enabledSources(allowed)}
}

// EagerValue intentionally takes the first matching flag anywhere in argv. The
// production TS scanner neither skips the executable nor respects --, and a
// following flag can be consumed as its value. Commander later has other rules.
func EagerValue(argv []string, name string) (string, bool) {
	for index, arg := range argv {
		if strings.HasPrefix(arg, name+"=") {
			return arg[len(name)+1:], true
		}
		if arg == name && index+1 < len(argv) {
			return argv[index+1], true
		}
	}
	return "", false
}

// Prepare preserves the fixed startup order: settings path/write, then source
// selection, with one cache reset after each successful setter. Failure leaves
// earlier completed effects intact and prevents later initialization.
func Prepare(argv []string, options Options) Result {
	state := DefaultState()
	if options.InitialState != nil {
		state = *options.InitialState
		state.AllowedSettingSources = append([]string{}, state.AllowedSettingSources...)
	}
	result := Result{State: state, Events: []Event{}}
	finish := func() Result {
		result.State.EnabledSettingSources = enabledSources(result.State.AllowedSettingSources)
		return result
	}
	fail := func(message string) Result {
		result.ExitCode = 1
		result.Stderr = message + "\n"
		return finish()
	}
	if value, found := EagerValue(argv, "--settings"); found && value != "" {
		trimmed := jsTrim(value)
		path := value
		if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
			if !json.Valid([]byte(trimmed)) {
				return fail("Error: Invalid JSON provided to --settings")
			}
			sum := sha256.Sum256([]byte(trimmed))
			path = filepath.Join(options.TempDir, "claude-settings-"+hex.EncodeToString(sum[:])[:16]+".json")
			if err := os.WriteFile(path, []byte(trimmed), 0666); err != nil {
				return fail("Error processing settings: " + nodeFileError(err, "open", path))
			}
		} else {
			physical := value
			if !filepath.IsAbs(physical) {
				physical = filepath.Join(options.WorkingDir, physical)
			}
			// safeResolvePath returns the original spelling after any failure or
			// for special files / UNC paths, before readFileSync validates it.
			if !strings.HasPrefix(value, "//") && !strings.HasPrefix(value, `\\`) {
				if info, err := os.Lstat(physical); err == nil && info.Mode()&(os.ModeNamedPipe|os.ModeSocket|os.ModeDevice) == 0 {
					if canonical, err := filepath.EvalSymlinks(physical); err == nil {
						path = canonical
						physical = canonical
					}
				}
			}
			if _, err := os.ReadFile(physical); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return fail("Error: Settings file not found: " + path)
				}
				if info, statErr := os.Stat(physical); statErr == nil && info.IsDir() {
					return fail("Error processing settings: EISDIR: illegal operation on a directory, read")
				}
				return fail("Error processing settings: " + nodeFileError(err, "open", path))
			}
		}
		result.State.FlagSettingsPath = &path
		result.Events = append(result.Events, Event{Operation: "set-flag-path", Value: path}, Event{Operation: "reset-cache"})
	}
	if value, found := EagerValue(argv, "--setting-sources"); found {
		sources := []string{}
		if value != "" {
			for _, name := range strings.Split(value, ",") {
				name = jsTrim(name)
				switch name {
				case "user":
					sources = append(sources, "userSettings")
				case "project":
					sources = append(sources, "projectSettings")
				case "local":
					sources = append(sources, "localSettings")
				default:
					return fail(fmt.Sprintf("Error processing --setting-sources: Invalid setting source: %s. Valid options are: user, project, local", name))
				}
			}
		}
		result.State.AllowedSettingSources = sources
		result.Events = append(result.Events, Event{Operation: "set-sources", Value: sources}, Event{Operation: "reset-cache"})
	}
	return finish()
}

func enabledSources(allowed []string) []string {
	result := []string{}
	seen := map[string]bool{}
	for _, source := range append(append([]string{}, allowed...), "policySettings", "flagSettings") {
		if !seen[source] {
			result = append(result, source)
			seen[source] = true
		}
	}
	return result
}

func jsTrim(value string) string {
	return strings.TrimFunc(value, func(r rune) bool {
		return r >= '\t' && r <= '\r' || r == ' ' || r == '\u00a0' || r == '\u1680' ||
			r >= '\u2000' && r <= '\u200a' || r == '\u2028' || r == '\u2029' ||
			r == '\u202f' || r == '\u205f' || r == '\u3000' || r == '\ufeff'
	})
}

// Bun's filesystem Error.message is the startup CLI's public error string.
// Preserve reviewed errno messages rather than exposing Go's PathError text.
func nodeFileError(err error, operation, path string) string {
	for _, code := range []struct {
		errno             syscall.Errno
		name, description string
	}{
		{syscall.EACCES, "EACCES", "permission denied"}, {syscall.EPERM, "EPERM", "operation not permitted"},
		{syscall.EISDIR, "EISDIR", "illegal operation on a directory"}, {syscall.ENOTDIR, "ENOTDIR", "not a directory"},
		{syscall.ENOENT, "ENOENT", "no such file or directory"}, {syscall.ELOOP, "ELOOP", "too many levels of symbolic links"},
	} {
		if errors.Is(err, code.errno) {
			return fmt.Sprintf("%s: %s, %s '%s'", code.name, code.description, operation, path)
		}
	}
	return err.Error()
}
