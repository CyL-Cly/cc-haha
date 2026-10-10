// Disposable subprocess oracle. It runs the actual Go upgrader and settings
// services through the same injected filesystem and explicit HOME.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/migrations"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

type action struct {
	Operation string         `json:"operation"`
	Config    string         `json:"config"`
	Patch     storage.Object `json:"patch"`
}
type fault struct {
	Operation string `json:"operation"`
	Path      string `json:"path"`
	Message   string `json:"message"`
	Once      bool   `json:"once"`
	used      bool
}
type request struct {
	HomeDir string   `json:"homeDir"`
	Steps   []action `json:"steps"`
	Faults  []fault  `json:"faults"`
}
type event struct {
	Operation string   `json:"operation"`
	Paths     []string `json:"paths"`
}
type boundary struct {
	storage.FileSystem
	home   string
	events []event
	faults []fault
}

func (boundary *boundary) before(operation string, paths ...string) error {
	names := []string{}
	for _, path := range paths {
		name, err := filepath.Rel(boundary.home, path)
		if err != nil || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return errors.New("fixture path escaped disposable home")
		}
		names = append(names, filepath.ToSlash(name))
	}
	boundary.events = append(boundary.events, event{operation, names})
	for index := range boundary.faults {
		fault := &boundary.faults[index]
		if fault.Operation == operation && strings.HasPrefix(names[0], fault.Path) && (!fault.Once || !fault.used) {
			fault.used = true
			return errors.New(fault.Message)
		}
	}
	return nil
}
func (boundary *boundary) ReadFile(path string) ([]byte, error) {
	if err := boundary.before("read", path); err != nil {
		return nil, err
	}
	return boundary.FileSystem.ReadFile(path)
}
func (boundary *boundary) WriteFile(path string, data []byte, mode fs.FileMode) error {
	if err := boundary.before("write", path); err != nil {
		return err
	}
	return boundary.FileSystem.WriteFile(path, data, mode)
}
func (boundary *boundary) MkdirAll(path string, mode fs.FileMode) error {
	if err := boundary.before("mkdir", path); err != nil {
		return err
	}
	return boundary.FileSystem.MkdirAll(path, mode)
}
func (boundary *boundary) Rename(source, target string) error {
	if err := boundary.before("rename", source, target); err != nil {
		return err
	}
	return boundary.FileSystem.Rename(source, target)
}
func (boundary *boundary) CopyFile(source, target string) error {
	if err := boundary.before("copy", source, target); err != nil {
		return err
	}
	return boundary.FileSystem.CopyFile(source, target)
}
func (boundary *boundary) Remove(path string) error {
	if err := boundary.before("unlink", path); err != nil {
		return err
	}
	return boundary.FileSystem.Remove(path)
}
func (boundary *boundary) access(path string) error {
	if err := boundary.before("access", path); err != nil {
		return err
	}
	_, err := os.Stat(path)
	return err
}

func run() error {
	var input request
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		return err
	}
	if input.HomeDir == "" || input.HomeDir != os.Getenv("HOME") || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(input.HomeDir, ".claude") {
		return errors.New("migration fixture requires disposable HOME/config")
	}
	boundary := &boundary{FileSystem: storage.New(storage.Options{}).FileSystem(), home: input.HomeDir, events: []event{}, faults: input.Faults}
	store := storage.New(storage.Options{FS: boundary, Warn: func(string) {}})
	upgrader, err := migrations.New(migrations.Options{Store: store, Access: boundary.access, Now: func() time.Time { return time.UnixMilli(1700000000000) }, Random: bytes.NewReader(bytes.Repeat([]byte{1, 2, 3}, 10000))})
	if err != nil {
		return err
	}
	config := filepath.Join(input.HomeDir, ".claude")
	options := settings.Options{Store: store, HomeDir: input.HomeDir, Environment: func() map[string]string { return map[string]string{"CLAUDE_CONFIG_DIR": config} }, EnsureMigrated: upgrader.SettingsCallback, ShellEnvironment: func(context.Context) (map[string]string, error) { return map[string]string{}, nil }, GitIgnore: func(context.Context, string, string) error { return nil }}
	service, err := settings.New(options)
	if err != nil {
		return err
	}
	managed, err := settings.NewManaged(options)
	if err != nil {
		return err
	}
	results := []any{}
	ctx := context.Background()
	for _, step := range input.Steps {
		if step.Config != "" {
			if filepath.IsAbs(step.Config) || step.Config == ".." || strings.HasPrefix(step.Config, "../") {
				return errors.New("config must be a fixture-relative path")
			}
			config = filepath.Join(input.HomeDir, step.Config)
		}
		var value any
		var operationError error
		switch step.Operation {
		case "ensure":
			value, operationError = upgrader.Ensure(ctx, config)
		case "reset":
			upgrader.ResetForTests()
		case "get-user":
			value, operationError = service.GetUserSettings(ctx)
		case "managed-read":
			value, operationError = managed.ReadSettings(ctx)
		case "managed-update":
			value, operationError = settings.UpdateManaged(ctx, managed, func(current storage.Object) (settings.ManagedUpdate[string], error) {
				for key, item := range step.Patch {
					current[key] = item
				}
				return settings.ManagedUpdate[string]{Settings: current, Result: "migration fixture update"}, nil
			})
		default:
			return fmt.Errorf("unknown fixture operation: %s", step.Operation)
		}
		if operationError != nil {
			results = append(results, map[string]any{"error": operationError.Error()})
		} else {
			results = append(results, map[string]any{"value": value})
		}
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(map[string]any{"results": results, "trace": boundary.events})
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
