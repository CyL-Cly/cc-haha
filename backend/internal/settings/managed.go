package settings

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// ManagedService preserves the separate cc-haha settings file. Like the TS
// service, its updater owns env replacement/deletion; no allowlist is invented.
type ManagedService struct{ service *Service }

func NewManaged(options Options) (*ManagedService, error) {
	if err := validateBootstrap(options); err != nil {
		return nil, err
	}
	return &ManagedService{service: &Service{options: options}}, nil
}

func (managed *ManagedService) read(ctx context.Context, path, config string) (storage.Object, error) {
	if err := managed.service.options.EnsureMigrated(ctx, config); err != nil {
		return nil, err
	}
	return storage.ReadRecoverableJSON(managed.service.options.Store, storage.ReadOptions[storage.Object]{Path: path, Label: "cc-haha managed settings", Default: storage.Object{}, Normalize: storage.NormalizeObject})
}

func (managed *ManagedService) ReadSettings(ctx context.Context) (storage.Object, error) {
	config := managed.service.configDir()
	return managed.read(ctx, filepath.Join(config, "cc-haha", "settings.json"), config)
}

type ManagedUpdate[T any] struct {
	Settings storage.Object
	Result   T
}

// UpdateManaged keeps the generic updater result out of persisted JSON and
// holds the shared FIFO lock through read, updater and atomic replacement.
func UpdateManaged[T any](ctx context.Context, managed *ManagedService, updater func(storage.Object) (ManagedUpdate[T], error)) (T, error) {
	var result T
	if managed == nil || updater == nil {
		return result, errors.New("managed settings update requires service and updater")
	}
	config := managed.service.configDir()
	path := filepath.Join(config, "cc-haha", "settings.json")
	err := managed.service.options.Store.WithWriteLock(path, func() error {
		current, err := managed.read(ctx, path, config)
		if err != nil {
			return err
		}
		next, err := updater(current)
		if err != nil {
			return err
		}
		if err := validatePatch(next.Settings); err != nil {
			return err
		}
		serialized, err := marshalJavaScriptSettings(shallowMerge(next.Settings))
		if err != nil {
			return err
		}
		if err := managed.service.options.Store.AtomicWriteJSON(path, "settings.json", serialized, storage.WriteOptions{MkdirFailureOutsideCatch: true}); err != nil {
			var failure *storage.FileError
			if errors.As(err, &failure) {
				return &APIError{StatusCode: 500, Code: "INTERNAL_ERROR", Message: fmt.Sprintf("Failed to write settings.json: %s", storage.JavaScriptErrorText(failure.Err, storage.JavaScriptErrorContext{RenameSourceIsFile: true}))}
			}
			return err
		}
		result = next.Result
		return nil
	})
	return result, err
}
