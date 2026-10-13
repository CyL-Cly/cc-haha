package application

import (
	"context"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/access"
	"github.com/NanmiCoder/cc-haha/backend/internal/server"
)

func newMigrationValidationServer(o ServerOptions) (*server.Server, error) {
	return server.New(server.Options{
		HomeDir: o.HomeDir, ConfigDir: o.ConfigDir, WorkDir: o.WorkDir,
		AppRoot: o.AppRoot, H5DistDir: o.H5DistDir, Version: o.Version,
		Environment: func() map[string]string {
			value := map[string]string{}
			for key, item := range o.Environment {
				value[key] = item
			}
			return value
		},
		Authorization:       access.Authorization{LocalToken: o.Environment[access.LocalAccessTokenEnv]},
		MigrationValidation: true,
		Factory: func(context.Context, server.SessionOptions, server.Emit) (server.LiveSession, error) {
			return nil, errors.New("Data migration is in progress")
		},
		MigrationActivate: func(context.Context) (*server.Server, error) {
			next := o
			next.Environment = map[string]string{}
			for key, value := range o.Environment {
				next.Environment[key] = value
			}
			delete(next.Environment, "CC_HAHA_MIGRATION_VALIDATION")
			return NewDesktopServer(next)
		},
	})
}
