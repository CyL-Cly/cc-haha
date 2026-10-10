// Package storage implements injected-path JSON persistence. It does not find
// user configuration, select a schema, or decide which data may be repaired.
package storage

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Object retains unknown values, including numeric lexemes and escaped Unicode,
// until the owning service explicitly changes them.
type Object map[string]json.RawMessage

// FileSystem is the persistence seam used by deterministic failure tests.
type FileSystem interface {
	ReadFile(string) ([]byte, error)
	WriteFile(string, []byte, fs.FileMode) error
	MkdirAll(string, fs.FileMode) error
	Rename(string, string) error
	Remove(string) error
	CopyFile(string, string) error
}

type Options struct {
	FS     FileSystem
	Now    func() time.Time
	Random io.Reader
	Warn   func(string)
}

type pathLock struct {
	tail chan struct{}
	refs int
}

// Store should be shared by services that update the same paths. Its locks only
// coordinate this process, matching the existing service write queues.
type Store struct {
	fs     FileSystem
	now    func() time.Time
	random io.Reader
	warn   func(string)
	mu     sync.Mutex
	locks  map[string]*pathLock
	rngMu  sync.Mutex
}

func New(options Options) *Store {
	if options.FS == nil {
		options.FS = osFileSystem{}
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	if options.Warn == nil {
		options.Warn = func(message string) { fmt.Fprintln(os.Stderr, message) }
	}
	return &Store{fs: options.FS, now: options.Now, random: options.Random, warn: options.Warn, locks: make(map[string]*pathLock)}
}

// FileSystem lets a service preserve the same injected persistence boundary
// when its existing transaction has a different backup or write sequence.
func (store *Store) FileSystem() FileSystem { return store.fs }

type ReadOptions[T any] struct {
	Path      string
	Label     string
	Default   T
	Normalize func(json.RawMessage) (value T, valid bool, err error)
}

// NormalizeObject accepts exactly the non-null, non-array JSON object shape
// accepted by normalizeJsonObject.ts. It imposes no schema or field filtering.
func NormalizeObject(raw json.RawMessage) (Object, bool, error) {
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, false, nil
	}
	var object Object
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, false, err
	}
	return object, true, nil
}

// ReadRecoverableJSON matches recoverableJsonFile.ts: absent and blank files
// return a fresh default without writing; bad JSON or rejected shape is moved
// aside on a best-effort basis. Normalizer failures and other I/O errors fail.
// A normalized result is never implicitly persisted.
func ReadRecoverableJSON[T any](store *Store, options ReadOptions[T]) (T, error) {
	var zero T
	if store == nil || options.Path == "" || options.Normalize == nil {
		return zero, errors.New("recoverable JSON requires a store, path, and normalizer")
	}
	raw, err := store.fs.ReadFile(options.Path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cloneDefault(options.Default)
		}
		return zero, &FileError{Operation: "read", Path: options.Path, Label: options.Label, Err: err}
	}
	decoded := decodeUTF8(raw)
	if strings.TrimFunc(string(decoded), jsWhitespace) == "" {
		return cloneDefault(options.Default)
	}
	var parsed json.RawMessage
	if err := json.Unmarshal(decoded, &parsed); err != nil {
		if err := store.quarantine(options.Path, options.Label, err.Error()); err != nil {
			return zero, err
		}
		return cloneDefault(options.Default)
	}
	value, valid, err := options.Normalize(parsed)
	if err != nil {
		return zero, err
	}
	if !valid {
		if err := store.quarantine(options.Path, options.Label, "unexpected JSON shape"); err != nil {
			return zero, err
		}
		return cloneDefault(options.Default)
	}
	return value, nil
}

func cloneDefault[T any](value T) (T, error) {
	var clone T
	raw, err := json.Marshal(value)
	if err != nil {
		return clone, err
	}
	if err := json.Unmarshal(raw, &clone); err != nil {
		return clone, err
	}
	return clone, nil
}

// FileError provides the original cause for an HTTP layer to map to its
// existing internal-error response. Storage itself has no HTTP dependency.
type FileError struct {
	Operation string
	Path      string
	Label     string
	Err       error
}

func (err *FileError) Error() string {
	preposition := "to"
	if err.Operation == "read" {
		preposition = "from"
	}
	return fmt.Sprintf("Failed to %s %s %s %s: %v", err.Operation, err.Label, preposition, err.Path, err.Err)
}

func (err *FileError) Unwrap() error { return err.Err }

func (store *Store) randomHex(size int) (string, error) {
	data := make([]byte, size)
	store.rngMu.Lock()
	defer store.rngMu.Unlock()
	if _, err := io.ReadFull(store.random, data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

func (store *Store) backupPath(path, suffix string) (string, error) {
	random, err := store.randomHex(3)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s.%s-%d-%s", path, suffix, store.now().UnixMilli(), random), nil
}

func (store *Store) quarantine(path, label, reason string) error {
	backup, err := store.backupPath(path, "invalid")
	if err != nil {
		return err
	}
	if err := store.fs.Rename(path, backup); err != nil {
		store.warn(fmt.Sprintf("[desktop] Recovered invalid %s from %s, but failed to quarantine it: %v", label, path, err))
		return nil
	}
	store.warn(fmt.Sprintf("[desktop] Recovered invalid %s; moved %s to %s: %s", label, path, backup, reason))
	return nil
}

// BackupFile copies the original bytes, leaving the source intact. The caller
// supplies an existing migration suffix such as "bak-before-migration".
func (store *Store) BackupFile(path, suffix string) (string, error) {
	if path == "" || suffix == "" || strings.ContainsAny(suffix, `/\`) {
		return "", errors.New("backup requires a path and filename suffix")
	}
	backup, err := store.backupPath(path, suffix)
	if err != nil {
		return "", err
	}
	if err := store.fs.CopyFile(path, backup); err != nil {
		return "", err
	}
	return backup, nil
}

type WriteOptions struct {
	// Only the existing user/project settings writer retries an ENOENT race.
	// Managed settings, UI preferences, and migrations use the default false.
	RetryMissingParent bool
	// ManagedSettingsService creates its parent outside the write/rename catch.
	// Keep that raw cause and skip temporary cleanup at precisely that boundary.
	MkdirFailureOutsideCatch bool
}

// AtomicWriteJSON writes an indented JSON document and trailing newline to a
// unique sibling, then renames it. Failed attempts clean up only that sibling.
// The caller owns cache invalidation and any read/merge/write transaction.
func (store *Store) AtomicWriteJSON(path, label string, value any, options WriteOptions) error {
	if path == "" {
		return errors.New("atomic JSON write requires a path")
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return err
	}
	return store.atomicWriteBytes(path, label, buffer.Bytes(), options)
}

// AtomicWriteRawJSON preserves the owning service's JSON.stringify bytes,
// including trailing-newline semantics, while retaining the shared atomic
// transaction and failure cleanup. It never supplies a schema or normalizer.
func (store *Store) AtomicWriteRawJSON(path, label string, value json.RawMessage, options WriteOptions) error {
	if path == "" {
		return errors.New("atomic JSON write requires a path")
	}
	if !json.Valid(value) {
		return errors.New("atomic JSON write requires valid JSON")
	}
	return store.atomicWriteBytes(path, label, value, options)
}

func (store *Store) atomicWriteBytes(path, label string, value []byte, options WriteOptions) error {
	attempts := 1
	if options.RetryMissingParent {
		attempts = 2
	}
	var lastError error
	for attempt := 0; attempt < attempts; attempt++ {
		timestamp := store.now().UnixMilli()
		random, err := store.randomHex(6)
		if err != nil {
			return err
		}
		temporary := fmt.Sprintf("%s.tmp.%d.%d.%s", path, os.Getpid(), timestamp, random)
		err = store.fs.MkdirAll(filepath.Dir(path), 0777)
		if err != nil && options.MkdirFailureOutsideCatch {
			return err
		}
		if err == nil {
			err = store.fs.WriteFile(temporary, value, 0666)
		}
		if err == nil {
			err = store.fs.Rename(temporary, path)
		}
		if err == nil {
			return nil
		}
		lastError = err
		_ = store.fs.Remove(temporary)
		if !errors.Is(err, fs.ErrNotExist) {
			break
		}
	}
	return &FileError{Operation: "write", Path: path, Label: label, Err: lastError}
}

// WithWriteLock serializes a whole read-modify-write operation for one path.
// Tasks run in queue admission order, matching the TS promise chain. Failure or
// panic releases the next task; unrelated paths remain independent.
func (store *Store) WithWriteLock(path string, task func() error) error {
	key := filepath.Clean(path)
	store.mu.Lock()
	lock := store.locks[key]
	if lock == nil {
		lock = &pathLock{}
		store.locks[key] = lock
	}
	previous := lock.tail
	completed := make(chan struct{})
	lock.tail = completed
	lock.refs++
	store.mu.Unlock()
	if previous != nil {
		<-previous
	}
	defer func() {
		store.mu.Lock()
		close(completed)
		lock.refs--
		if lock.refs == 0 {
			delete(store.locks, key)
		}
		store.mu.Unlock()
	}()
	return task()
}

type osFileSystem struct{}

func (osFileSystem) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }
func (osFileSystem) WriteFile(path string, data []byte, mode fs.FileMode) error {
	return os.WriteFile(path, data, mode)
}
func (osFileSystem) MkdirAll(path string, mode fs.FileMode) error { return os.MkdirAll(path, mode) }
func (osFileSystem) Rename(old, next string) error                { return os.Rename(old, next) }
func (osFileSystem) Remove(path string) error                     { return os.Remove(path) }
func (osFileSystem) CopyFile(source, destination string) (err error) {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return err
	}
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer func() {
		if closeError := output.Close(); err == nil {
			err = closeError
		}
		if err != nil {
			_ = os.Remove(destination)
		}
	}()
	_, err = io.Copy(output, input)
	return err
}
