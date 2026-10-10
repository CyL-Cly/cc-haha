package settings

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/migrations"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

type managedErrorFS struct {
	storage.FileSystem
	operation  string
	renameFrom string
}

func (boundary *managedErrorFS) WriteFile(path string, data []byte, mode fs.FileMode) error {
	if boundary.operation == "write" && strings.Contains(path, "settings.json.tmp.") {
		return errors.New("fixture write denied")
	}
	return boundary.FileSystem.WriteFile(path, data, mode)
}

func (boundary *managedErrorFS) Rename(from, to string) error {
	boundary.renameFrom = from
	if boundary.operation == "rename" {
		return errors.New("fixture rename denied")
	}
	return boundary.FileSystem.Rename(from, to)
}

func (boundary *managedErrorFS) MkdirAll(path string, mode fs.FileMode) error {
	if boundary.operation == "mkdir" {
		return errors.New("fixture mkdir denied")
	}
	return boundary.FileSystem.MkdirAll(path, mode)
}

func TestManagedMkdirAndRandomOutsideCatchRemainRawWithRealUpgrader(t *testing.T) {
	for _, operation := range []string{"mkdir", "random"} {
		t.Run(operation, func(t *testing.T) {
			options := fixtureOptions(t)
			boundary := &managedErrorFS{FileSystem: options.Store.FileSystem()}
			storageOptions := storage.Options{FS: boundary, Warn: func(string) {}}
			if operation == "random" {
				storageOptions.Random = bytes.NewReader(nil)
			}
			options.Store = storage.New(storageOptions)
			upgrader, err := migrations.New(migrations.Options{Store: options.Store})
			if err != nil {
				t.Fatal(err)
			}
			options.EnsureMigrated = upgrader.SettingsCallback
			managed := mustManaged(t, options)
			path := filepath.Join(managed.service.configDir(), "cc-haha", "settings.json")
			original := `{"env":{},"committed":true}`
			seed(t, path, original)
			if _, err := managed.ReadSettings(context.Background()); err != nil {
				t.Fatal(err)
			}
			boundary.operation = operation
			_, err = UpdateManaged(context.Background(), managed, func(current storage.Object) (ManagedUpdate[bool], error) {
				current["next"] = []byte(`true`)
				return ManagedUpdate[bool]{Settings: current, Result: true}, nil
			})
			var api *APIError
			if err == nil || errors.As(err, &api) {
				t.Fatal("failure outside source catch became typed API error", err)
			}
			if operation == "random" && !errors.Is(err, io.EOF) {
				t.Fatal("native RNG cause lost", err)
			}
			if actual, _ := os.ReadFile(path); string(actual) != original {
				t.Fatal("failure outside catch changed committed state", string(actual))
			}
			if remaining, _ := filepath.Glob(path + ".tmp.*"); len(remaining) != 0 {
				t.Fatal("failure outside catch produced temporary state", remaining)
			}
		})
	}
}

func TestManagedActualNativeRenamePrecheckUsesTemporaryFileProvenance(t *testing.T) {
	options := fixtureOptions(t)
	boundary := &managedErrorFS{FileSystem: options.Store.FileSystem()}
	options.Store = storage.New(storage.Options{FS: boundary, Warn: func(string) {}})
	upgrader, err := migrations.New(migrations.Options{Store: options.Store})
	if err != nil {
		t.Fatal(err)
	}
	options.EnsureMigrated = upgrader.SettingsCallback
	managed := mustManaged(t, options)
	path := filepath.Join(managed.service.configDir(), "cc-haha", "settings.json")
	seed(t, path, `{"env":{},"committed":true}`)
	if _, err := managed.ReadSettings(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, err = UpdateManaged(context.Background(), managed, func(current storage.Object) (ManagedUpdate[bool], error) {
		// An external replacement after the read makes rename hit the actual OS
		// directory target. Everything is under this test's disposable home.
		if err := os.Remove(path); err != nil {
			return ManagedUpdate[bool]{}, err
		}
		if err := os.Mkdir(path, 0700); err != nil {
			return ManagedUpdate[bool]{}, err
		}
		return ManagedUpdate[bool]{Settings: current, Result: true}, nil
	})
	var api *APIError
	if !errors.As(err, &api) || api.Code != "INTERNAL_ERROR" || api.StatusCode != 500 {
		t.Fatal("native rename failure lost typed business error", err)
	}
	if boundary.renameFrom == "" {
		t.Fatal("business updater did not reach actual rename")
	}
	// Freeze the errno wording only on the host with actual Bun/native proof.
	// Other native hosts still verify typed failure/cleanup here and need their
	// own actual-source fixture before claiming message equivalence.
	if runtime.GOOS == "darwin" {
		want := "Failed to write settings.json: Error: EISDIR: illegal operation on a directory, rename '" + boundary.renameFrom + "' -> '" + path + "'"
		if api.Message != want {
			t.Fatalf("native temporary-file provenance differs: %q; want %q", api.Message, want)
		}
	}
	if remaining, _ := filepath.Glob(path + ".tmp.*"); len(remaining) != 0 {
		t.Fatal("native failed rename left temporary state", remaining)
	}
}

func TestManagedWriteFailureRetainsJavaScriptErrorPrefixAndCommittedBytes(t *testing.T) {
	for _, operation := range []string{"write", "rename"} {
		t.Run(operation, func(t *testing.T) {
			options := fixtureOptions(t)
			boundary := &managedErrorFS{FileSystem: options.Store.FileSystem()}
			options.Store = storage.New(storage.Options{FS: boundary, Warn: func(string) {}})
			upgrader, err := migrations.New(migrations.Options{Store: options.Store})
			if err != nil {
				t.Fatal(err)
			}
			options.EnsureMigrated = upgrader.SettingsCallback
			managed := mustManaged(t, options)
			path := filepath.Join(managed.service.configDir(), "cc-haha", "settings.json")
			original := []byte(`{"env":{},"committed":true}`)
			seed(t, path, string(original))
			if _, err := managed.ReadSettings(context.Background()); err != nil {
				t.Fatal(err)
			}
			boundary.operation = operation
			_, err = UpdateManaged(context.Background(), managed, func(current storage.Object) (ManagedUpdate[bool], error) {
				current["next"] = []byte(`true`)
				return ManagedUpdate[bool]{Settings: current, Result: true}, nil
			})
			var api *APIError
			want := "Failed to write settings.json: Error: fixture " + operation + " denied"
			if !errors.As(err, &api) || api.StatusCode != 500 || api.Code != "INTERNAL_ERROR" || api.Message != want {
				t.Fatalf("source typed error differs: %v; want %q", err, want)
			}
			if actual, err := os.ReadFile(path); err != nil || !bytes.Equal(actual, original) {
				t.Fatalf("failed write changed committed bytes: %q, %v", actual, err)
			}
			if remaining, _ := filepath.Glob(path + ".tmp.*"); len(remaining) != 0 {
				t.Fatal("failed write left temporary state", remaining)
			}
		})
	}
}
