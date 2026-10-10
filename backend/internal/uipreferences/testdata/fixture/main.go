package main

import (
	"bytes"
	"context"
	"encoding/hex"
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
	"github.com/NanmiCoder/cc-haha/backend/internal/uipreferences"
)

type step struct {
	Operation   string          `json:"operation"`
	Patch       json.RawMessage `json:"patch"`
	Bytes       []byte          `json:"bytes"`
	ContentType string          `json:"contentType"`
}
type fault struct {
	Operation string `json:"operation"`
	Path      string `json:"path"`
	Message   string `json:"message"`
	Once      bool   `json:"once"`
	used      bool
}
type request struct {
	Home   string  `json:"home"`
	Steps  []step  `json:"steps"`
	Faults []fault `json:"faults"`
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
			return errors.New("UI fixture escaped disposable home")
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
	if input.Home == "" || input.Home != os.Getenv("HOME") || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(input.Home, ".claude") {
		return errors.New("UI fixture requires disposable HOME/config")
	}
	boundary := &boundary{FileSystem: storage.New(storage.Options{}).FileSystem(), home: input.Home, events: []event{}, faults: input.Faults}
	warnings := []string{}
	clock := func() time.Time { return time.UnixMilli(1700000000000) }
	random := bytes.NewReader(bytes.Repeat([]byte{1, 2, 3}, 100000))
	store := storage.New(storage.Options{FS: boundary, Now: clock, Random: random, Warn: func(message string) { warnings = append(warnings, message) }})
	upgrader, err := migrations.New(migrations.Options{Store: store, Access: boundary.access, Now: clock, Random: random})
	if err != nil {
		return err
	}
	service, err := uipreferences.New(uipreferences.Options{Store: store, Upgrader: upgrader, ConfigDir: func() string { return filepath.Join(input.Home, ".claude") }, Access: boundary.access, Now: clock, Random: random, PID: 42})
	if err != nil {
		return err
	}
	results := []any{}
	ctx := context.Background()
	for _, step := range input.Steps {
		var value any
		var err error
		switch step.Operation {
		case "read":
			value, err = service.ReadPreferences(ctx)
		case "sidebar":
			value, err = service.UpdateSidebarPreferences(ctx, step.Patch)
		case "profile":
			value, err = service.UpdateProfilePreferences(ctx, step.Patch)
		case "project-name":
			value, err = service.UpdateProjectDisplayName(ctx, step.Patch)
		case "pet":
			value, err = service.UpdatePetPreferences(ctx, step.Patch)
		case "voice":
			value, err = service.UpdateVoiceInputPreferences(ctx, step.Patch)
		case "upload-avatar":
			value, err = service.UpdateProfileAvatar(ctx, step.Bytes, step.ContentType)
		case "clear-avatar":
			value, err = service.ClearProfileAvatar(ctx)
		case "read-avatar":
			var avatar *uipreferences.Avatar
			avatar, err = service.ReadProfileAvatar(ctx)
			if avatar != nil {
				value = map[string]any{"bytes": hex.EncodeToString(avatar.Bytes), "contentType": avatar.ContentType}
			}
		default:
			return fmt.Errorf("unknown UI fixture operation %s", step.Operation)
		}
		if err != nil {
			status, code := 500, "INTERNAL_ERROR"
			var api *settings.APIError
			if errors.As(err, &api) {
				status, code = api.StatusCode, api.Code
			}
			results = append(results, map[string]any{"error": map[string]any{"status": status, "code": code, "message": strings.ReplaceAll(err.Error(), input.Home, "<HOME>")}})
		} else {
			results = append(results, map[string]any{"value": value})
		}
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(map[string]any{"results": results, "trace": boundary.events, "warningCount": len(warnings)})
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
