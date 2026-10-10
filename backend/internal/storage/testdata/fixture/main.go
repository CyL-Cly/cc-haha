// This executable exists only for offline TS/Go storage interoperability tests.
// testdata is excluded from the production go list ./... package set.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

type request struct {
	Operation string         `json:"operation"`
	Path      string         `json:"path"`
	Patch     storage.Object `json:"patch"`
}

func run() error {
	var input request
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		return err
	}
	warnings := []string{}
	store := storage.New(storage.Options{Warn: func(message string) { warnings = append(warnings, message) }})
	read := func() (storage.Object, error) {
		return storage.ReadRecoverableJSON(store, storage.ReadOptions[storage.Object]{Path: input.Path, Label: "settings", Default: storage.Object{}, Normalize: storage.NormalizeObject})
	}
	var value storage.Object
	var err error
	switch input.Operation {
	case "read":
		value, err = read()
	case "update":
		err = store.WithWriteLock(input.Path, func() error {
			value, err = read()
			if err != nil {
				return err
			}
			for key, patch := range input.Patch {
				value[key] = patch
			}
			return store.AtomicWriteJSON(input.Path, "settings", value, storage.WriteOptions{RetryMissingParent: true})
		})
	default:
		return fmt.Errorf("unsupported storage fixture operation: %s", input.Operation)
	}
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(map[string]any{"value": value, "warnings": warnings})
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
