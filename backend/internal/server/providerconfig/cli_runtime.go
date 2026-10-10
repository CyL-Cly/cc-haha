package providerconfig

import (
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// NewCLIRuntime follows managedEnv's readActiveProviderManagedEnv: malformed or
// absent startup input is an empty selection, never desktop recovery/writeback.
// Selection normalization and managed-env production remain the shared ones.
func NewCLIRuntime(options Options) (*Service, error) {
	service, err := New(options)
	if err != nil {
		return nil, err
	}
	service.indexRead = func() (storage.Object, error) {
		raw, err := options.Store.FileSystem().ReadFile(service.indexPath())
		if err != nil {
			return nil, err
		}
		index, valid, err := normalize(raw)
		if err != nil {
			return nil, err
		}
		if !valid {
			return nil, errors.New("Invalid providers index")
		}
		return index, nil
	}
	return service, nil
}
