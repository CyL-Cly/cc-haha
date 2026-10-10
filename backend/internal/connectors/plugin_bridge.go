package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type LoadedPlugin struct {
	Source string
	Path   string
}
type PluginLoadResult struct {
	Enabled []LoadedPlugin
	Errors  []string
}

// Runtime is the actual application plugin/settings/credential boundary. The
// managed bridge owns artifact publication; it cannot manufacture loader or
// active-session readiness from those files. Callers must bind these services.
type PluginBackend interface {
	BlockedByPolicy(context.Context, string) (bool, error)
	RegisterMarketplace(context.Context, string) error
	CacheAndRegister(context.Context, string, string, string) error
	UpdateEnabled(context.Context, string, bool) error
	UninstallUserPlugin(context.Context, string) error
	LoadPlugins(context.Context) (PluginLoadResult, error)
	ReloadSessions(context.Context, string, *protocol.JSONObject) error
}

type ManagedBridge struct {
	mu           sync.Mutex
	config, root string
	catalog      Catalog
	backend      PluginBackend
	uuid         func() string
}

func NewManagedBridge(config string, c Catalog, backend PluginBackend, uuid func() string) (*ManagedBridge, error) {
	if !filepath.IsAbs(config) || backend == nil || uuid == nil {
		return nil, errors.New("managed connector bridge requires private config, plugin/settings/credential/runtime services and UUID")
	}
	return &ManagedBridge{config: config, root: filepath.Join(config, "connectors", "marketplace"), catalog: c, backend: backend, uuid: uuid}, nil
}

func readObject(file string, absent protocol.JSONObject) (protocol.JSONObject, error) {
	source, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return absent.Clone(), nil
	}
	if err != nil {
		return protocol.JSONObject{}, err
	}
	var object protocol.JSONObject
	if err := object.UnmarshalJSON(source); err != nil {
		return object, err
	}
	if !object.IsObject() {
		return object, errors.New("Expected a JSON object")
	}
	return object, nil
}

func pretty(value any) ([]byte, error) {
	source, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	source, err = protocol.ParseAndStringifyJavaScriptJSON(source)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	err = json.Indent(&out, source, "", "  ")
	return out.Bytes(), err
}

func (b *ManagedBridge) write(file string, data []byte) error {
	tmp := file + "." + b.uuid() + ".tmp"
	defer os.Remove(tmp)
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

func (b *ManagedBridge) ownership() error {
	known, err := readObject(filepath.Join(b.config, "plugins", "known_marketplaces.json"), protocol.NewJSONObject())
	if err != nil {
		return err
	}
	if v := known.Get(managedMarketplace); v != nil {
		source := nested(nested(v).Get("source"))
		path, err := filepath.Abs(source.String("path"))
		if err != nil || source.String("source") != "directory" || path != b.root {
			return errors.New("The haha-connectors marketplace name is already used by another source")
		}
	}
	return nil
}

func (b *ManagedBridge) Install(ctx context.Context, def protocol.JSONObject, i Installation) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	name, err := managedPluginName(def)
	if err != nil {
		return err
	}
	bundle := b.catalog.skill(def.String("id"), def.String("version"))
	var files []BundleFile
	if bundle != nil {
		files, err = ReadSkillBundle(*bundle, i.Directory)
		if err != nil {
			return err
		}
	}
	if err = b.ownership(); err != nil {
		return err
	}
	blocked, err := b.backend.BlockedByPolicy(ctx, def.String("pluginId"))
	if err != nil {
		return err
	}
	if blocked {
		return errors.New("This connector plugin is disabled by organization policy")
	}
	pluginRoot := filepath.Join(b.root, "plugins", name)
	for _, dir := range []string{filepath.Join(b.root, ".claude-plugin"), filepath.Join(pluginRoot, ".claude-plugin")} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			return err
		}
	}
	if bundle != nil {
		if err = os.RemoveAll(filepath.Join(pluginRoot, "skills")); err != nil {
			return err
		}
	}
	if err = os.MkdirAll(filepath.Join(pluginRoot, "skills", name), 0700); err != nil {
		return err
	}
	for _, file := range files {
		path := filepath.Join(pluginRoot, filepath.FromSlash(file.Target))
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		if err = b.write(path, file.Bytes); err != nil {
			return err
		}
	}
	version := def.String("version") + "-connector.1"
	manifest := protocol.NewJSONObject()
	remoteArtifact := false
	if recipe := b.catalog.remote(def.String("id")); recipe != nil {
		remote, err := BuildRemotePlugin(*recipe)
		if err != nil {
			return err
		}
		manifest = remote.Manifest.Clone()
		data, err := pretty(remote.MCPConfig)
		if err != nil {
			return err
		}
		if err = b.write(filepath.Join(pluginRoot, ".mcp.json"), data); err != nil {
			return err
		}
		remoteArtifact = true
	}
	display := def.String("displayName")
	if nullish(def.Get("displayName")) {
		display = nativeSkillRecipes[def.String("id")].Name
		if display == "" {
			display = name
		}
	}
	manifest.Assign(record(field("name", name), field("version", version), field("description", display+" desktop connector"), field("skills", "./skills")))
	if remoteArtifact {
		manifest.Set("mcpServers", quote("./.mcp.json"))
	}
	data, err := pretty(manifest)
	if err != nil {
		return err
	}
	if err = b.write(filepath.Join(pluginRoot, ".claude-plugin", "plugin.json"), data); err != nil {
		return err
	}
	text, err := RenderConnectorSkill(b.catalog, def, i)
	if err != nil {
		return err
	}
	if err = b.write(filepath.Join(pluginRoot, "skills", name, "SKILL.md"), []byte(text)); err != nil {
		return err
	}
	marketFile := filepath.Join(b.root, ".claude-plugin", "marketplace.json")
	market, err := readObject(marketFile, record(field("name", managedMarketplace), field("plugins", []protocol.JSONObject{})))
	if err != nil {
		return err
	}
	var entries []protocol.JSONObject
	if market.String("name") != managedMarketplace || json.Unmarshal(market.Get("plugins"), &entries) != nil || entries == nil {
		return errors.New("Invalid managed connector marketplace")
	}
	kept := []protocol.JSONObject{}
	for _, entry := range entries {
		if entry.String("name") != name {
			kept = append(kept, entry)
		}
	}
	kept = append(kept, record(field("name", name), field("source", "./plugins/"+name), field("version", version), field("strict", true)))
	data, err = pretty(record(field("name", managedMarketplace), field("owner", record(field("name", "Claude Code Haha"))), field("plugins", kept)))
	if err != nil {
		return err
	}
	if err = b.write(marketFile, data); err != nil {
		return err
	}
	if err = b.backend.RegisterMarketplace(ctx, b.root); err != nil {
		return err
	}
	if err = b.backend.UpdateEnabled(ctx, def.String("pluginId"), false); err != nil {
		return err
	}
	return b.backend.CacheAndRegister(ctx, def.String("pluginId"), version, pluginRoot)
}

func (b *ManagedBridge) SetEnabled(ctx context.Context, def protocol.JSONObject, enabled bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, err := managedPluginName(def); err != nil {
		return err
	}
	if err := b.ownership(); err != nil {
		return err
	}
	if enabled {
		blocked, err := b.backend.BlockedByPolicy(ctx, def.String("pluginId"))
		if err != nil {
			return err
		}
		if blocked {
			return errors.New("This connector plugin is disabled by organization policy")
		}
	}
	return b.backend.UpdateEnabled(ctx, def.String("pluginId"), enabled)
}

func (b *ManagedBridge) Ready(ctx context.Context, def protocol.JSONObject) (bool, error) {
	name, err := managedPluginName(def)
	if err != nil {
		return false, err
	}
	loaded, err := b.backend.LoadPlugins(ctx)
	if err != nil {
		return false, err
	}
	for _, id := range loaded.Errors {
		if id == def.String("pluginId") {
			return false, nil
		}
	}
	for _, plugin := range loaded.Enabled {
		if plugin.Source != def.String("pluginId") {
			continue
		}
		if bundle := b.catalog.skill(def.String("id"), def.String("version")); bundle != nil {
			if _, err := ReadSkillBundle(*bundle, plugin.Path); err != nil {
				return false, nil
			}
		}
		data, err := os.ReadFile(filepath.Join(plugin.Path, "skills", name, "SKILL.md"))
		return err == nil && strings.HasPrefix(string(data), "---\nname:"), nil
	}
	return false, nil
}

func (b *ManagedBridge) Remove(ctx context.Context, def protocol.JSONObject) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	name, err := managedPluginName(def)
	if err != nil {
		return err
	}
	if err = b.ownership(); err != nil {
		return err
	}
	if err = b.backend.UpdateEnabled(ctx, def.String("pluginId"), false); err != nil {
		return err
	}
	if err = b.backend.UninstallUserPlugin(ctx, def.String("pluginId")); err != nil && !strings.Contains(err.Error(), "not installed") {
		return err
	}
	marketFile := filepath.Join(b.root, ".claude-plugin", "marketplace.json")
	market, err := readObject(marketFile, protocol.NewJSONObject())
	if err != nil {
		return err
	}
	if len(market.OwnProperties()) > 0 {
		var entries []protocol.JSONObject
		if market.String("name") != managedMarketplace || json.Unmarshal(market.Get("plugins"), &entries) != nil || entries == nil {
			return errors.New("Invalid managed connector marketplace")
		}
		kept := []protocol.JSONObject{}
		for _, entry := range entries {
			if entry.String("name") != name {
				kept = append(kept, entry)
			}
		}
		market.Set("plugins", raw(kept))
		data, err := pretty(market)
		if err != nil {
			return err
		}
		if err = b.write(marketFile, data); err != nil {
			return err
		}
	}
	return os.RemoveAll(filepath.Join(b.root, "plugins", name))
}

func (b *ManagedBridge) Reload(ctx context.Context, id string, required *protocol.JSONObject) error {
	if required != nil {
		if _, err := managedPluginName(*required); err != nil {
			return err
		}
	}
	return b.backend.ReloadSessions(ctx, id, required)
}

// copyPluginTree is the local-source materialization primitive used by the
// registry backend. It preserves directories, modes, bytes and symlink targets
// just like the original local-source cache copy; a loaded plugin's owning
// parser is responsible for validating its declared components.
func copyPluginTree(source, target string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		dest := filepath.Join(target, relative)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(dest, info.Mode().Perm())
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, dest)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, data, info.Mode().Perm())
	})
}

func iso(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }
