package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func mustManaged(t *testing.T, options Options) *ManagedService {
	t.Helper()
	managed, err := NewManaged(options)
	if err != nil {
		t.Fatal(err)
	}
	return managed
}

func TestManagedBootstrapRequiresMigrationButNoUnrelatedShellOrGit(t *testing.T) {
	options := fixtureOptions(t)
	options.EnsureMigrated = nil
	if _, err := NewManaged(options); err == nil {
		t.Fatal("missing migration callback accepted")
	}
	options = fixtureOptions(t)
	options.ShellEnvironment, options.GitIgnore = nil, nil
	if _, err := NewManaged(options); err != nil {
		t.Fatalf("managed service requires unrelated dependencies: %v", err)
	}
}

func TestManagedUpdaterResultEnvReplacementDeletionAndUserIsolation(t *testing.T) {
	options := fixtureOptions(t)
	invalidations := 0
	options.InvalidateCache = func() { invalidations++ }
	managed := mustManaged(t, options)
	config := managed.service.configDir()
	user := filepath.Join(config, "settings.json")
	path := filepath.Join(config, "cc-haha", "settings.json")
	userOriginal := `{"effortLevel":"high","env":{"ANTHROPIC_BASE_URL":"original-claude-env","ANTHROPIC_API_KEY":"fake-original-key"},"userFuture":{"keep":true}}`
	seed(t, user, userOriginal)
	seed(t, path, `{"customField":"keep","modelContext":200000,"env":{"EXISTING_VAR":"keep","ANTHROPIC_API_KEY":"fake-managed-key","futureEnv":{"keep":true}}}`)
	result, err := UpdateManaged(testContext, managed, func(current storage.Object) (ManagedUpdate[string], error) {
		env := objectValue(current["env"])
		delete(env, "ANTHROPIC_API_KEY")
		env["ANTHROPIC_MODEL"] = stringJSON("fixture-model")
		current["env"] = objectJSON(env)
		current["modelContext"] = nil
		return ManagedUpdate[string]{Settings: current, Result: "opaque caller result"}, nil
	})
	if err != nil || result != "opaque caller result" {
		t.Fatalf("updater result changed: %q, %v", result, err)
	}
	value, err := managed.ReadSettings(testContext)
	if err != nil {
		t.Fatal(err)
	}
	assertObject(t, value, `{"customField":"keep","env":{"EXISTING_VAR":"keep","ANTHROPIC_MODEL":"fixture-model","futureEnv":{"keep":true}}}`)
	raw, _ := os.ReadFile(user)
	if string(raw) != userOriginal || invalidations != 0 {
		t.Fatal("managed write changed user settings or invalidated user cache")
	}
	if strings.Contains(string(mustRead(t, path)), "opaque caller result") {
		t.Fatal("updater result leaked to persisted shape")
	}
	_, err = UpdateManaged(testContext, managed, func(current storage.Object) (ManagedUpdate[bool], error) {
		current["env"] = json.RawMessage(`{"REPLACED_ENV":"only"}`)
		return ManagedUpdate[bool]{Settings: current, Result: true}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	assertObject(t, fileObject(t, path), `{"customField":"keep","env":{"REPLACED_ENV":"only"}}`)
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestManagedUpdatesSerializeAcrossInstancesAndContinueAfterFailedUpdater(t *testing.T) {
	options := fixtureOptions(t)
	var migrations atomic.Int64
	options.EnsureMigrated = func(context.Context, string) error { migrations.Add(1); return nil }
	first, second := mustManaged(t, options), mustManaged(t, options)
	failure := errors.New("updater fixture rejected")
	_, err := UpdateManaged(testContext, first, func(storage.Object) (ManagedUpdate[int], error) { return ManagedUpdate[int]{}, failure })
	if !errors.Is(err, failure) {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	failures := make(chan error, 20)
	for index := 0; index < 20; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			managed := first
			if index%2 == 0 {
				managed = second
			}
			_, err := UpdateManaged(testContext, managed, func(current storage.Object) (ManagedUpdate[int], error) {
				current[fmt.Sprintf("update-%d", index)] = json.RawMessage(`true`)
				return ManagedUpdate[int]{Settings: current, Result: index}, nil
			})
			failures <- err
		}(index)
	}
	group.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	value, err := first.ReadSettings(testContext)
	if err != nil || len(value) != 20 || migrations.Load() != 22 {
		t.Fatalf("managed updates lost or migration bypassed: %d, %v, %d", len(value), err, migrations.Load())
	}
	if _, err := os.Stat(filepath.Join(first.service.configDir(), "settings.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("managed updates created user settings")
	}
}

func TestManagedMissingCorruptReadAndMigrationFailures(t *testing.T) {
	options := fixtureOptions(t)
	managed := mustManaged(t, options)
	value, err := managed.ReadSettings(testContext)
	if err != nil {
		t.Fatal(err)
	}
	assertObject(t, value, `{}`)
	path := filepath.Join(managed.service.configDir(), "cc-haha", "settings.json")
	seed(t, path, `{"env":`)
	value, err = managed.ReadSettings(testContext)
	if err != nil {
		t.Fatal(err)
	}
	assertObject(t, value, `{}`)
	files, _ := os.ReadDir(filepath.Dir(path))
	if len(files) != 1 || !strings.HasPrefix(files[0].Name(), "settings.json.invalid-") {
		t.Fatalf("managed corrupt source lost: %v", files)
	}
	failure := errors.New("migration fixture failed")
	options.EnsureMigrated = func(context.Context, string) error { return failure }
	managed = mustManaged(t, options)
	if _, err := managed.ReadSettings(testContext); !errors.Is(err, failure) {
		t.Fatal("migration error hidden", err)
	}
	_, err = UpdateManaged(testContext, managed, func(storage.Object) (ManagedUpdate[bool], error) {
		t.Fatal("updater ran after migration failure")
		return ManagedUpdate[bool]{}, nil
	})
	if !errors.Is(err, failure) {
		t.Fatal("migration write bypass", err)
	}
}

func TestManagedIOAndInvalidUpdateErrorsKeepCommittedBytes(t *testing.T) {
	options := fixtureOptions(t)
	filesystem := &failingFS{}
	options.Store = storage.New(storage.Options{FS: filesystem})
	managed := mustManaged(t, options)
	path := filepath.Join(managed.service.configDir(), "cc-haha", "settings.json")
	seed(t, path, `{"committed":true}`)
	filesystem.readFailure = path
	if _, err := managed.ReadSettings(testContext); !errors.Is(err, fs.ErrPermission) {
		t.Fatal(err)
	}
	_, err := UpdateManaged(testContext, managed, func(current storage.Object) (ManagedUpdate[bool], error) {
		return ManagedUpdate[bool]{Settings: current}, nil
	})
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatal("read failure bypass", err)
	}
	filesystem.readFailure = ""
	filesystem.writeFailure = true
	_, err = UpdateManaged(testContext, managed, func(storage.Object) (ManagedUpdate[bool], error) {
		return ManagedUpdate[bool]{Settings: object(`{"next":true}`)}, nil
	})
	var apiError *APIError
	if !errors.As(err, &apiError) || apiError.StatusCode != 500 || apiError.Code != "INTERNAL_ERROR" || !strings.HasPrefix(apiError.Message, "Failed to write settings.json:") {
		t.Fatalf("managed write error contract: %v", err)
	}
	assertObject(t, fileObject(t, path), `{"committed":true}`)
	filesystem.writeFailure = false
	_, err = UpdateManaged(testContext, managed, func(storage.Object) (ManagedUpdate[bool], error) {
		return ManagedUpdate[bool]{Settings: storage.Object{"bad": json.RawMessage("{")}}, nil
	})
	if err == nil {
		t.Fatal("invalid raw object accepted")
	}
	if _, err := UpdateManaged[bool](testContext, nil, nil); err == nil {
		t.Fatal("invalid updater accepted")
	}
}
