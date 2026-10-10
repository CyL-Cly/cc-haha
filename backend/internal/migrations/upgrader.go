// Package migrations ports the existing cc-haha persistence upgrades. Paths
// and filesystem provenance are injected; shared user settings are never a
// migration target and no new global schema or repair policy is introduced.
package migrations

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

type Report struct {
	MigratedEntries []string `json:"migratedEntries"`
	Failures        []string `json:"failures"`
}
type Options struct {
	Store *storage.Store
	// Access is fs.access(path)'s existence check, not a read or shape check.
	// Custom filesystem tests inject it beside that Store's same boundary.
	Access func(string) error
	Now    func() time.Time
	Random io.Reader
}
type completion struct {
	done   chan struct{}
	report Report
}
type Upgrader struct {
	fs        storage.FileSystem
	access    func(string) error
	now       func() time.Time
	random    io.Reader
	rngMu     sync.Mutex
	mu        sync.Mutex
	configDir string
	current   *completion
}

func New(options Options) (*Upgrader, error) {
	if options.Store == nil {
		return nil, errors.New("persistent storage upgrader requires a shared store")
	}
	if options.Access == nil {
		options.Access = func(path string) error { _, err := os.Stat(path); return err }
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	return &Upgrader{fs: options.Store.FileSystem(), access: options.Access, now: options.Now, random: options.Random}, nil
}

// Ensure caches exactly the last selected config directory, as the TS module's
// single promise does. A -> B -> A runs A twice. Waiting cancellation does not
// cancel a shared upgrade or discard its report for another caller.
func (upgrader *Upgrader) Ensure(ctx context.Context, configDir string) (Report, error) {
	if configDir == "" {
		return Report{}, errors.New("persistent storage upgrader requires an explicit config directory")
	}
	upgrader.mu.Lock()
	if upgrader.current == nil || upgrader.configDir != configDir {
		upgrader.configDir = configDir
		pending := &completion{done: make(chan struct{})}
		upgrader.current = pending
		go func() { pending.report = upgrader.run(configDir); close(pending.done) }()
	}
	pending := upgrader.current
	upgrader.mu.Unlock()
	select {
	case <-pending.done:
		return pending.report, nil
	case <-ctx.Done():
		return Report{}, ctx.Err()
	}
}

// SettingsCallback is the real SettingsService bootstrap dependency. Migration
// entry failures stay in the cached report; TS settings reads await the report
// without turning its failures list into an exception.
func (upgrader *Upgrader) SettingsCallback(ctx context.Context, configDir string) error {
	_, err := upgrader.Ensure(ctx, configDir)
	return err
}
func (upgrader *Upgrader) ResetForTests() {
	upgrader.mu.Lock()
	upgrader.configDir = ""
	upgrader.current = nil
	upgrader.mu.Unlock()
}

func (upgrader *Upgrader) nonce(path, suffix string) (string, error) {
	// Date.now is evaluated before randomBytes in the source filename template.
	timestamp := upgrader.now().UnixMilli()
	data := make([]byte, 3)
	upgrader.rngMu.Lock()
	_, err := io.ReadFull(upgrader.random, data)
	upgrader.rngMu.Unlock()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%s%d-%s", path, suffix, timestamp, hex.EncodeToString(data)), nil
}
func (upgrader *Upgrader) read(path string) (json.RawMessage, bool, error) {
	raw, err := upgrader.fs.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	decoded := storage.DecodeUTF8(raw)
	var value json.RawMessage
	if err := json.Unmarshal(decoded, &value); err != nil {
		return nil, false, err
	}
	return value, false, nil
}
func (upgrader *Upgrader) write(path string, raw json.RawMessage) error {
	// Preserve source ordering: mkdir is outside the try/cleanup transaction.
	if err := upgrader.fs.MkdirAll(filepath.Dir(path), 0777); err != nil {
		return err
	}
	temporary, err := upgrader.nonce(path, ".tmp.")
	if err != nil {
		return err
	}
	data, err := stableJSON(raw)
	if err == nil {
		err = upgrader.fs.WriteFile(temporary, data, 0666)
	}
	if err == nil {
		err = upgrader.fs.Rename(temporary, path)
	}
	if err != nil {
		_ = upgrader.fs.Remove(temporary)
	}
	return err
}
func (upgrader *Upgrader) backup(path string) error {
	target, err := upgrader.nonce(path, ".bak-before-migration-")
	if err != nil {
		return err
	}
	return upgrader.fs.CopyFile(path, target)
}
func (upgrader *Upgrader) quarantine(path string) error {
	target, err := upgrader.nonce(path, ".invalid-")
	if err != nil {
		return err
	}
	return upgrader.fs.Rename(path, target)
}
func appendFailure(report *Report, label string, err error) {
	report.Failures = append(report.Failures, label+": "+err.Error())
}
func syntaxError(err error) bool { var syntax *json.SyntaxError; return errors.As(err, &syntax) }

func (upgrader *Upgrader) migrateEntry(path, label string, report *Report, transform func(json.RawMessage) (json.RawMessage, error)) {
	current, missing, err := upgrader.read(path)
	if missing {
		return
	}
	if err == nil {
		var migrated json.RawMessage
		migrated, err = transform(current)
		if err == nil {
			var before, after []byte
			before, err = stableJSON(current)
			if err == nil {
				after, err = stableJSON(migrated)
			}
			if err == nil && bytes.Equal(before, after) {
				return
			}
			if err == nil {
				err = upgrader.backup(path)
			}
			if err == nil {
				err = upgrader.write(path, migrated)
			}
			if err == nil {
				report.MigratedEntries = append(report.MigratedEntries, label)
				return
			}
		}
	}
	if syntaxError(err) {
		if err = upgrader.quarantine(path); err == nil {
			err = upgrader.write(path, json.RawMessage("{}"))
		}
		if err == nil {
			report.MigratedEntries = append(report.MigratedEntries, label)
			return
		}
	}
	appendFailure(report, label, err)
}
func (upgrader *Upgrader) migrateLegacy(configDir, ccDir string, report *Report) {
	target := filepath.Join(ccDir, "providers.json")
	err := upgrader.access(target)
	if err == nil {
		return
	}
	if !errors.Is(err, fs.ErrNotExist) {
		appendFailure(report, "cc-haha/providers.json", err)
		return
	}
	legacy, missing, err := upgrader.read(filepath.Join(configDir, "providers.json"))
	if missing {
		return
	}
	if err == nil {
		migrated := migrateLegacy(legacy)
		if migrated == nil {
			return
		}
		err = upgrader.write(target, migrated)
		if err == nil {
			report.MigratedEntries = append(report.MigratedEntries, "providers.json -> cc-haha/providers.json")
			settingsPath := filepath.Join(ccDir, "settings.json")
			_, missing, readError := upgrader.read(settingsPath)
			if readError != nil || !missing {
				return
			}
			managed := managedForLegacy(migrated)
			if managed == nil {
				return
			}
			err = upgrader.write(settingsPath, managed)
			if err == nil {
				report.MigratedEntries = append(report.MigratedEntries, "providers.json -> cc-haha/settings.json")
				return
			}
		}
	}
	appendFailure(report, "providers.json", err)
}
func (upgrader *Upgrader) run(configDir string) Report {
	report := Report{MigratedEntries: []string{}, Failures: []string{}}
	ccDir := filepath.Join(configDir, "cc-haha")
	upgrader.migrateLegacy(configDir, ccDir, &report)
	upgrader.migrateEntry(filepath.Join(ccDir, "providers.json"), "cc-haha/providers.json", &report, migrateProviders)
	upgrader.migrateEntry(filepath.Join(ccDir, "settings.json"), "cc-haha/settings.json", &report, migrateManaged)
	return report
}
