// Offline business-service parity fixture. Settings and the real upgrader use
// one injected Store, and are never installed in the production executable.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/migrations"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

type step struct {
	Operation string                `json:"operation"`
	Patch     storage.Object        `json:"patch"`
	Unset     []string              `json:"unset"`
	Root      string                `json:"root"`
	Agent     string                `json:"agent"`
	Mode      string                `json:"mode"`
	Models    settings.ModelMapping `json:"models"`
}

type request struct {
	HomeDir        string            `json:"homeDir"`
	ProjectRoot    string            `json:"projectRoot"`
	ShellEnv       map[string]string `json:"shellEnv"`
	GitExecutable  string            `json:"gitExecutable"`
	MigrationScope string            `json:"migrationScope"`
	Steps          []step            `json:"steps"`
}

func run() error {
	var input request
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		return err
	}
	if input.MigrationScope != "current-settings-fixture" || input.HomeDir == "" || input.HomeDir != os.Getenv("HOME") {
		return errors.New("settings fixture requires explicit disposable home and current-settings migration corpus")
	}
	warnings, migrationPaths := []string{}, []string{}
	invalidations := 0
	environment := map[string]string{}
	for _, variable := range os.Environ() {
		for index, character := range variable {
			if character == '=' {
				environment[variable[:index]] = variable[index+1:]
				break
			}
		}
	}
	store := storage.New(storage.Options{Warn: func(message string) { warnings = append(warnings, message) }})
	upgrader, err := migrations.New(migrations.Options{Store: store})
	if err != nil {
		return err
	}
	options := settings.Options{
		Store:   store,
		HomeDir: input.HomeDir, ProjectRoot: input.ProjectRoot,
		Environment: func() map[string]string { return environment },
		EnsureMigrated: func(ctx context.Context, config string) error {
			migrationPaths = append(migrationPaths, config)
			return upgrader.SettingsCallback(ctx, config)
		},
		ShellEnvironment: func(context.Context) (map[string]string, error) { return input.ShellEnv, nil },
		InvalidateCache:  func() { invalidations++ }, GitExecutable: input.GitExecutable,
		LogError: func(err error) { warnings = append(warnings, err.Error()) },
	}
	service, err := settings.New(options)
	if err != nil {
		return err
	}
	managed, err := settings.NewManaged(options)
	if err != nil {
		return err
	}
	ctx := context.Background()
	results := []any{}
	for _, action := range input.Steps {
		patch := action.Patch
		if len(action.Unset) > 0 && patch == nil {
			patch = storage.Object{}
		}
		for _, field := range action.Unset {
			patch[field] = nil
		}
		var value any
		var err error
		switch action.Operation {
		case "get-user":
			value, err = service.GetUserSettings(ctx)
		case "get-project":
			value, err = service.GetProjectSettings(ctx, action.Root)
		case "get-local":
			value, err = service.GetLocalSettings(ctx, action.Root)
		case "get-merged":
			value, err = service.GetSettings(ctx, action.Root)
		case "get-teams":
			value, err = service.GetAgentTeamsEnabled(ctx)
		case "get-permission":
			value, err = service.GetPermissionMode(ctx)
		case "update-user":
			err = service.UpdateUserSettings(ctx, patch)
		case "update-project":
			err = service.UpdateProjectSettings(ctx, patch, action.Root)
		case "update-local":
			err = service.UpdateLocalSettings(ctx, patch, action.Root)
			service.DrainBackground()
		case "official-mapping":
			err = service.UpdateOfficialModelMapping(ctx, action.Models)
		case "override":
			err = service.UpdateBuiltInAgentOverride(ctx, action.Agent, patch)
		case "reset-override":
			err = service.UpdateBuiltInAgentOverride(ctx, action.Agent, nil)
		case "set-permission":
			err = service.SetPermissionMode(ctx, action.Mode)
		case "managed-read":
			value, err = managed.ReadSettings(ctx)
		case "managed-update":
			value, err = settings.UpdateManaged(ctx, managed, func(current storage.Object) (settings.ManagedUpdate[string], error) {
				for key, item := range patch {
					current[key] = item
				}
				return settings.ManagedUpdate[string]{Settings: current, Result: "fixture updater result"}, nil
			})
		default:
			return fmt.Errorf("unsupported settings fixture operation: %s", action.Operation)
		}
		if err != nil {
			status, code := 500, "INTERNAL_ERROR"
			var apiError *settings.APIError
			if errors.As(err, &apiError) {
				status, code = apiError.StatusCode, apiError.Code
			}
			results = append(results, map[string]any{"error": map[string]any{"status": status, "code": code, "message": err.Error()}})
		} else {
			results = append(results, map[string]any{"value": value})
		}
	}
	service.DrainBackground()
	for index, path := range migrationPaths {
		migrationPaths[index], err = filepath.Rel(input.HomeDir, path)
		if err != nil {
			return err
		}
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(map[string]any{"results": results, "warningCount": len(warnings), "migrationPaths": migrationPaths, "invalidations": invalidations})
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
