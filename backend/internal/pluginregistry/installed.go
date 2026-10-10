package pluginregistry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/pluginschema"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

type InstalledOptions struct {
	PluginsDir string
	Store      *storage.Store
	Exists     func(string) bool
	Now        func() time.Time
	OnError    func(error)
}
type InstalledRegistry struct {
	options InstalledOptions
	schemas pluginschema.Schemas
	mu      sync.Mutex
	session *protocol.JSONObject
}

func NewInstalledRegistry(options InstalledOptions) (*InstalledRegistry, error) {
	if options.PluginsDir == "" || options.Store == nil {
		return nil, errors.New("installed plugins require an explicit directory and shared store")
	}
	if options.Exists == nil {
		options.Exists = func(path string) bool { _, err := os.Stat(path); return err == nil }
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.OnError == nil {
		options.OnError = func(error) {}
	}
	schemas, err := pluginschema.Declarations()
	if err != nil {
		return nil, err
	}
	return &InstalledRegistry{options: options, schemas: schemas}, nil
}
func (r *InstalledRegistry) path() string {
	return filepath.Join(r.options.PluginsDir, "installed_plugins.json")
}
func emptyInstalled() protocol.JSONObject {
	return protocol.NewJSONObject(field("version", 2), field("plugins", protocol.NewJSONObject()))
}
func (r *InstalledRegistry) Clear() { r.mu.Lock(); r.session = nil; r.mu.Unlock() }

// Session is deliberately separate from disk-only background updates. A
// running session keeps its existing cached plugin until explicit reload.
func (r *InstalledRegistry) Session(ctx context.Context) (protocol.JSONObject, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.session != nil {
		return r.session.Clone(), nil
	}
	var data protocol.JSONObject
	err := r.options.Store.WithWriteLock(r.path(), func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		projected, preserved, rebase, err := r.read()
		if err != nil {
			r.options.OnError(err)
			data = emptyInstalled()
			return nil
		}
		data = projected
		if rebase {
			if err := r.write(preserved); err != nil {
				r.options.OnError(err)
				data = emptyInstalled()
			}
		}
		return nil
	})
	if err != nil {
		return protocol.JSONObject{}, err
	}
	r.session = &data
	return data.Clone(), nil
}
func (r *InstalledRegistry) Disk() (protocol.JSONObject, error) {
	data, _, _, err := r.read()
	if err != nil {
		r.options.OnError(err)
		return emptyInstalled(), nil
	}
	return data, nil
}
func (r *InstalledRegistry) read() (protocol.JSONObject, protocol.JSONObject, bool, error) {
	bytes, err := r.options.Store.FileSystem().ReadFile(r.path())
	if errors.Is(err, os.ErrNotExist) {
		return emptyInstalled(), emptyInstalled(), false, nil
	}
	if err != nil {
		return protocol.JSONObject{}, protocol.JSONObject{}, false, err
	}
	bytes = protocol.DecodeJavaScriptUTF8(bytes)
	if !json.Valid(bytes) {
		return protocol.JSONObject{}, protocol.JSONObject{}, false, errors.New("invalid installed plugins JSON")
	}
	validated, issues := pluginschema.Validate(r.schemas.Installed, bytes)
	if len(issues) > 0 {
		return protocol.JSONObject{}, protocol.JSONObject{}, false, fmt.Errorf("invalid installed plugins: %s", issuesText(issues))
	}
	data, preserved := object(validated), object(bytes)
	plugins, rawPlugins := object(data.Get("plugins")), object(preserved.Get("plugins"))
	wasV1 := numericVersion(data.Get("version"), 1)
	if wasV1 {
		migrated, rawMigrated := protocol.NewJSONObject(), protocol.NewJSONObject()
		for _, property := range plugins.OwnProperties() {
			var id string
			_ = json.Unmarshal(property.NameJSON, &id)
			old := object(property.Value)
			entry := protocol.NewJSONObject(field("scope", "user"), field("installPath", VersionedPathIn(r.options.PluginsDir, id, old.String("version"))))
			preservedEntry := object(rawPlugins.Get(id)).Clone()
			preservedEntry.Set("scope", raw("user"))
			preservedEntry.Set("installPath", entry.Get("installPath"))
			for _, key := range []string{"version", "installedAt", "lastUpdated", "gitCommitSha"} {
				entry.Set(key, old.Get(key))
			}
			_ = migrated.SetPropertyJSON(property.NameJSON, raw([]protocol.JSONObject{entry}))
			_ = rawMigrated.SetPropertyJSON(property.NameJSON, raw([]protocol.JSONObject{preservedEntry}))
		}
		plugins, rawPlugins = migrated, rawMigrated
		data.Set("version", raw(2))
		preserved.Set("version", raw(2))
	}
	rebased := false
	for _, property := range plugins.OwnProperties() {
		var id string
		_ = json.Unmarshal(property.NameJSON, &id)
		entries, preservedEntries := array(property.Value), array(rawPlugins.Get(id))
		for index, value := range entries {
			entry := object(value)
			if entry.Get("version") == nil {
				return protocol.JSONObject{}, protocol.JSONObject{}, false, errors.New("installed plugin version is missing")
			}
			expected := VersionedPathIn(r.options.PluginsDir, id, entry.String("version"))
			absoluteExpected, _ := filepath.Abs(expected)
			absoluteRecorded, _ := filepath.Abs(entry.String("installPath"))
			if absoluteExpected != absoluteRecorded && r.options.Exists(expected) {
				entry.Set("installPath", raw(expected))
				entries[index] = raw(entry)
				if index < len(preservedEntries) {
					p := object(preservedEntries[index])
					p.Set("installPath", raw(expected))
					preservedEntries[index] = raw(p)
				}
				rebased = true
			}
		}
		_ = plugins.SetPropertyJSON(property.NameJSON, raw(entries))
		_ = rawPlugins.SetPropertyJSON(property.NameJSON, raw(preservedEntries))
	}
	data.Set("plugins", raw(plugins))
	preserved.Set("plugins", raw(rawPlugins))
	// V1 conversion is memory-only on read, matching Source. Writes through
	// installation management persist V2 and preserve extension fields.
	if wasV1 {
		rebased = false
	}
	return data, preserved, rebased, nil
}
func numericVersion(value json.RawMessage, want float64) bool {
	number, err := strconv.ParseFloat(string(value), 64)
	return err == nil && number == want
}
func (r *InstalledRegistry) write(value protocol.JSONObject) error {
	encoded, err := value.MarshalJSON()
	if err != nil {
		return err
	}
	var pretty bytes.Buffer
	if err = json.Indent(&pretty, encoded, "", "  "); err != nil {
		return err
	}
	return r.options.Store.AtomicWriteRawJSON(r.path(), "installed plugins", pretty.Bytes(), storage.WriteOptions{RetryMissingParent: true})
}
func sameInstallation(entry protocol.JSONObject, scope string, project *string) bool {
	if entry.String("scope") != scope {
		return false
	}
	if project == nil {
		return entry.Get("projectPath") == nil
	}
	return entry.Get("projectPath") != nil && entry.String("projectPath") == *project
}
func (r *InstalledRegistry) Add(ctx context.Context, id, scope, path string, metadata protocol.JSONObject, project *string) error {
	if !pluginID.MatchString(id) {
		return errors.New("Plugin ID must be in format: plugin@marketplace")
	}
	if scope != "user" && scope != "project" && scope != "local" && scope != "managed" {
		return errors.New("invalid plugin scope")
	}
	return r.options.Store.WithWriteLock(r.path(), func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, preserved, _, err := r.read()
		if err != nil {
			r.options.OnError(err)
			preserved = emptyInstalled()
		}
		plugins := object(preserved.Get("plugins"))
		entries := []json.RawMessage{}
		if value := plugins.Get(id); value != nil {
			entries = array(value)
		}
		index := -1
		entry := protocol.NewJSONObject()
		for i, value := range entries {
			if sameInstallation(object(value), scope, project) {
				index = i
				entry = object(value).Clone()
				break
			}
		}
		entry.Set("scope", raw(scope))
		entry.Set("installPath", raw(path))
		for _, key := range []string{"version", "installedAt", "lastUpdated", "gitCommitSha"} {
			entry.Set(key, metadata.Get(key))
		}
		entry.Delete("projectPath")
		if project != nil && *project != "" {
			entry.Set("projectPath", raw(*project))
		}
		if index < 0 {
			entries = append(entries, raw(entry))
		} else {
			entries[index] = raw(entry)
		}
		plugins.Set(id, raw(entries))
		preserved.Set("version", raw(2))
		preserved.Set("plugins", raw(plugins))
		return r.write(preserved)
	})
}
func (r *InstalledRegistry) UpdateDisk(ctx context.Context, id, scope string, project *string, path, version string, sha *string) error {
	return r.options.Store.WithWriteLock(r.path(), func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, preserved, _, err := r.read()
		if err != nil {
			r.options.OnError(err)
			return nil
		}
		plugins := object(preserved.Get("plugins"))
		if plugins.Get(id) == nil {
			return nil
		}
		entries := array(plugins.Get(id))
		changed := false
		for i, value := range entries {
			entry := object(value)
			if sameInstallation(entry, scope, project) {
				entry.Set("installPath", raw(path))
				entry.Set("version", raw(version))
				entry.Set("lastUpdated", raw(r.options.Now().UTC().Format("2006-01-02T15:04:05.000Z")))
				if sha != nil {
					entry.Set("gitCommitSha", raw(*sha))
				}
				entries[i] = raw(entry)
				changed = true
				break
			}
		}
		if !changed {
			return nil
		}
		plugins.Set(id, raw(entries))
		preserved.Set("plugins", raw(plugins))
		return r.write(preserved)
	})
}
func (r *InstalledRegistry) Remove(ctx context.Context, id, scope string, project *string) error {
	return r.options.Store.WithWriteLock(r.path(), func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, preserved, _, err := r.read()
		if err != nil {
			r.options.OnError(err)
			preserved = emptyInstalled()
		}
		plugins := object(preserved.Get("plugins"))
		if plugins.Get(id) == nil {
			return nil
		}
		entries := []json.RawMessage{}
		for _, value := range array(plugins.Get(id)) {
			if !sameInstallation(object(value), scope, project) {
				entries = append(entries, value)
			}
		}
		if len(entries) == 0 {
			plugins.Delete(id)
		} else {
			plugins.Set(id, raw(entries))
		}
		preserved.Set("plugins", raw(plugins))
		return r.write(preserved)
	})
}

func (r *InstalledRegistry) Pending(ctx context.Context) (protocol.JSONObject, error) {
	memory, err := r.Session(ctx)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	disk, err := r.Disk()
	if err != nil {
		return protocol.JSONObject{}, err
	}
	known := object(memory.Get("plugins"))
	details := []protocol.JSONObject{}
	for _, property := range object(disk.Get("plugins")).OwnProperties() {
		var id string
		_ = json.Unmarshal(property.NameJSON, &id)
		if known.Get(id) == nil {
			continue
		}
		for _, value := range array(property.Value) {
			entry := object(value)
			var project *string
			if entry.Get("projectPath") != nil {
				p := entry.String("projectPath")
				project = &p
			}
			for _, prior := range array(known.Get(id)) {
				old := object(prior)
				if sameInstallation(old, entry.String("scope"), project) && old.String("installPath") != entry.String("installPath") {
					oldVersion, newVersion := old.String("version"), entry.String("version")
					if oldVersion == "" {
						oldVersion = "unknown"
					}
					if newVersion == "" {
						newVersion = "unknown"
					}
					details = append(details, protocol.NewJSONObject(field("pluginId", id), field("scope", entry.String("scope")), field("oldVersion", oldVersion), field("newVersion", newVersion)))
					break
				}
			}
		}
	}
	return protocol.NewJSONObject(field("count", len(details)), field("details", details), field("pending", len(details) > 0)), nil
}
