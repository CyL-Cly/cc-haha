package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// PluginStore implements the original local managed marketplace and user
// installation persistence. Runtime discovery, policy and credential deletion
// remain mandatory bindings to the shared application services.
type PluginStoreOptions struct {
	ConfigDir          string
	Store              *storage.Store
	Now                func() time.Time
	UUID               func() string
	Policy             func(context.Context) (protocol.JSONObject, error)
	MarketplaceAllowed func(context.Context, string) error
	UpdateEnabled      func(context.Context, string, *bool) error
	Load               func(context.Context) (PluginLoadResult, error)
	Reload             func(context.Context, string, *protocol.JSONObject) error
	DeleteOptions      func(context.Context, string) error
}
type PluginStore struct{ options PluginStoreOptions }

func NewPluginStore(o PluginStoreOptions) (*PluginStore, error) {
	if !filepath.IsAbs(o.ConfigDir) || o.Store == nil || o.Now == nil || o.UUID == nil || o.Policy == nil || o.MarketplaceAllowed == nil || o.UpdateEnabled == nil || o.Load == nil || o.Reload == nil || o.DeleteOptions == nil {
		return nil, errors.New("connector plugin store requires shared settings/policy/loader/session/credential bindings")
	}
	return &PluginStore{o}, nil
}
func (p *PluginStore) BlockedByPolicy(ctx context.Context, id string) (bool, error) {
	policy, err := p.options.Policy(ctx)
	if err != nil {
		return false, err
	}
	return string(nested(policy.Get("enabledPlugins")).Get(id)) == "false", nil
}
func (p *PluginStore) UpdateEnabled(ctx context.Context, id string, v bool) error {
	return p.options.UpdateEnabled(ctx, id, &v)
}
func (p *PluginStore) LoadPlugins(ctx context.Context) (PluginLoadResult, error) {
	return p.options.Load(ctx)
}
func (p *PluginStore) ReloadSessions(ctx context.Context, id string, required *protocol.JSONObject) error {
	return p.options.Reload(ctx, id, required)
}

func (p *PluginStore) atomic(file string, value any) error {
	data, err := pretty(value)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		return err
	}
	tmp := file + "." + p.options.UUID() + ".tmp"
	defer os.Remove(tmp)
	if err = os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

func (p *PluginStore) RegisterMarketplace(ctx context.Context, root string) error {
	if err := p.options.MarketplaceAllowed(ctx, root); err != nil {
		return err
	}
	path := filepath.Join(p.options.ConfigDir, "plugins", "known_marketplaces.json")
	return p.options.Store.WithWriteLock(path, func() error {
		known, err := readObject(path, protocol.NewJSONObject())
		if err != nil {
			return err
		}
		for _, property := range known.OwnProperties() {
			source := nested(nested(property.Value).Get("source"))
			if source.String("source") == "directory" && source.String("path") == root {
				return nil
			}
		}
		market, err := readObject(filepath.Join(root, ".claude-plugin", "marketplace.json"), protocol.NewJSONObject())
		if err != nil {
			return err
		}
		if market.String("name") != managedMarketplace {
			return errors.New("Invalid managed connector marketplace")
		}
		known.Set(managedMarketplace, raw(record(field("source", record(field("source", "directory"), field("path", root))), field("installLocation", root), field("lastUpdated", iso(p.options.Now())))))
		return p.atomic(path, known)
	})
}

var cacheIdentity = regexp.MustCompile(`[^a-zA-Z0-9_-]`)
var cacheVersion = regexp.MustCompile(`[^a-zA-Z0-9._-]`)

func PluginCachePath(config, id, version string) string {
	name, market, ok := strings.Cut(id, "@")
	if !ok || market == "" {
		market = "unknown"
	}
	return filepath.Join(config, "plugins", "cache", cacheIdentity.ReplaceAllString(market, "-"), cacheIdentity.ReplaceAllString(name, "-"), cacheVersion.ReplaceAllString(version, "-"))
}

func (p *PluginStore) installed() (protocol.JSONObject, error) {
	path := filepath.Join(p.options.ConfigDir, "plugins", "installed_plugins.json")
	data, err := readObject(path, record(field("version", 2), field("plugins", protocol.NewJSONObject())))
	if err != nil {
		return data, err
	}
	plugins := nested(data.Get("plugins"))
	if !plugins.IsObject() {
		return data, errors.New("Invalid installed plugin records")
	}
	version := data.Get("version")
	if equalJSONNumber(version, 2) {
		return data, nil
	}
	if !equalJSONNumber(version, 1) {
		return data, errors.New("Unsupported installed plugin version")
	}
	migrated := protocol.NewJSONObject()
	for _, entry := range plugins.OwnProperties() {
		old := nested(entry.Value)
		if !old.IsObject() {
			return data, errors.New("Invalid installed plugin record")
		}
		var id string
		if err := json.Unmarshal(entry.NameJSON, &id); err != nil {
			return data, err
		}
		i := old.Clone()
		i.Set("scope", quote("user"))
		i.Set("installPath", quote(PluginCachePath(p.options.ConfigDir, id, old.String("version"))))
		if err := migrated.SetPropertyJSON(entry.NameJSON, raw([]protocol.JSONObject{i})); err != nil {
			return data, err
		}
	}
	data.Set("version", raw(2))
	data.Set("plugins", raw(migrated))
	return data, nil
}

func equalJSONNumber(v json.RawMessage, want float64) bool {
	var n float64
	return json.Unmarshal(v, &n) == nil && n == want && len(v) > 0 && v[0] != '"' && string(v) != "null"
}

func (p *PluginStore) CacheAndRegister(ctx context.Context, id, version, source string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	manifest, err := readObject(filepath.Join(source, ".claude-plugin", "plugin.json"), protocol.NewJSONObject())
	if err != nil {
		return err
	}
	if v := manifest.String("version"); v != "" {
		version = v
	}
	cache := PluginCachePath(p.options.ConfigDir, id, version)
	// This is the Source's user-scoped local-source cache materialization. No
	// executable or original account directory is moved into the plugin cache.
	if err = os.MkdirAll(filepath.Dir(cache), 0700); err != nil {
		return err
	}
	if err = os.RemoveAll(cache); err != nil {
		return err
	}
	if err = copyPluginTree(source, cache); err != nil {
		return err
	}
	path := filepath.Join(p.options.ConfigDir, "plugins", "installed_plugins.json")
	return p.options.Store.WithWriteLock(path, func() error {
		data, err := p.installed()
		if err != nil {
			return err
		}
		plugins := nested(data.Get("plugins"))
		entries := []protocol.JSONObject{}
		if v := plugins.Get(id); v != nil {
			if err = json.Unmarshal(v, &entries); err != nil {
				return err
			}
		}
		now := iso(p.options.Now())
		entry := record(field("scope", "user"), field("installPath", cache), field("version", version), field("installedAt", now), field("lastUpdated", now))
		updated := false
		for index, old := range entries {
			if old.String("scope") == "user" && old.Get("projectPath") == nil {
				entries[index] = entry
				updated = true
				break
			}
		}
		if !updated {
			entries = append(entries, entry)
		}
		plugins.Set(id, raw(entries))
		data.Set("plugins", raw(plugins))
		return p.atomic(path, data)
	})
}

func (p *PluginStore) UninstallUserPlugin(ctx context.Context, id string) error {
	path := filepath.Join(p.options.ConfigDir, "plugins", "installed_plugins.json")
	return p.options.Store.WithWriteLock(path, func() error {
		data, err := p.installed()
		if err != nil {
			return err
		}
		plugins := nested(data.Get("plugins"))
		entries := []protocol.JSONObject{}
		if v := plugins.Get(id); v != nil {
			if err = json.Unmarshal(v, &entries); err != nil {
				return err
			}
		}
		index := -1
		for i, entry := range entries {
			if entry.String("scope") == "user" && entry.Get("projectPath") == nil {
				index = i
				break
			}
		}
		if index < 0 {
			return errors.New("Plugin is not installed in user scope")
		}
		if err = p.options.UpdateEnabled(ctx, id, nil); err != nil {
			return err
		}
		removed := entries[index]
		entries = append(entries[:index:index], entries[index+1:]...)
		if len(entries) == 0 {
			plugins.Delete(id)
		} else {
			plugins.Set(id, raw(entries))
		}
		data.Set("plugins", raw(plugins))
		if err = p.atomic(path, data); err != nil {
			return err
		}
		if len(entries) == 0 {
			if installed := removed.String("installPath"); installed != "" {
				if err = p.markOrphan(installed); err != nil {
					return err
				}
			}
			if err = p.options.DeleteOptions(ctx, id); err != nil {
				return err
			}
		}
		return nil
	})
}

func (p *PluginStore) markOrphan(path string) error {
	// cacheUtils.ts stamps plain epoch milliseconds and consumes marker write
	// errors. Uninstallation keeps the version directory for concurrent readers.
	_ = os.WriteFile(filepath.Join(path, ".orphaned_at"), []byte(strconv.FormatInt(p.options.Now().UnixMilli(), 10)), 0600)
	return nil
}
