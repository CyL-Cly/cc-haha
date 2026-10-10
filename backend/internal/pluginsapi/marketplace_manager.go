package pluginsapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
)

// MarketplaceManager binds CLI management to the same physical catalog fetch,
// native settings writer and process plugin cache as installed-plugin consumers.
type MarketplaceManagerOptions struct {
	Resolver      *MarketplaceResolver
	Seeds         []string
	ReadSettings  func(context.Context, string, string) (protocol.JSONObject, error)
	WriteSettings func(context.Context, string, protocol.JSONObject, [][]string) error
	ClearCaches   func(context.Context) error
}
type MarketplaceManager struct{ options MarketplaceManagerOptions }
type AddedMarketplace struct {
	Name                string
	AlreadyMaterialized bool
	Source              protocol.JSONObject
}

func NewMarketplaceManager(o MarketplaceManagerOptions) (*MarketplaceManager, error) {
	if o.Resolver == nil || o.ReadSettings == nil || o.WriteSettings == nil || o.ClearCaches == nil {
		return nil, fmt.Errorf("Marketplace management requires native fetch/settings/cache producers")
	}
	return &MarketplaceManager{options: o}, nil
}
func (m *MarketplaceManager) Known(ctx context.Context) (protocol.JSONObject, error) {
	return ReadKnownMarketplaces(ctx, m.options.Resolver.options.PluginsDir)
}
func (m *MarketplaceManager) seed(location string) string {
	for _, dir := range m.options.Seeds {
		if location == dir || strings.HasPrefix(location, dir+string(filepath.Separator)) {
			return dir
		}
	}
	return ""
}
func (m *MarketplaceManager) strict(location string) bool {
	base := filepath.Clean(filepath.Join(m.options.Resolver.options.PluginsDir, "marketplaces"))
	return strings.HasPrefix(filepath.Clean(location), base+string(filepath.Separator))
}
func equalJSON(a, b protocol.JSONObject) bool {
	var x, y any
	ar, _ := a.MarshalJSON()
	br, _ := b.MarshalJSON()
	_ = json.Unmarshal(ar, &x)
	_ = json.Unmarshal(br, &y)
	return reflect.DeepEqual(x, y)
}
func (m *MarketplaceManager) save(ctx context.Context, value protocol.JSONObject) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return m.options.Resolver.write(filepath.Join(m.options.Resolver.options.PluginsDir, "known_marketplaces.json"), value)
}
func (m *MarketplaceManager) location(source, catalog protocol.JSONObject) string {
	switch source.String("source") {
	case "directory":
		return source.String("path")
	case "file":
		return filepath.Dir(filepath.Dir(source.String("path")))
	}
	return filepath.Join(m.options.Resolver.options.PluginsDir, "marketplaces", catalog.String("name"))
}
func (m *MarketplaceManager) Add(ctx context.Context, source protocol.JSONObject, scope string, progress func(string)) (AddedMarketplace, error) {
	policySettings, err := m.options.ReadSettings(ctx, "managed", "")
	if err != nil {
		return AddedMarketplace{}, err
	}
	policy := pluginregistry.PolicyFromSettings(policySettings)
	if !policy.Allowed(source) {
		message := "Marketplace source '" + pluginregistry.FormatSource(source) + "' is blocked by enterprise policy."
		if !policy.Blocked(source) {
			allowed := []string{}
			for _, v := range policy.Allowlist {
				allowed = append(allowed, pluginregistry.FormatSource(v))
			}
			if len(allowed) > 0 {
				message += " Allowed sources: " + strings.Join(allowed, ", ")
			} else {
				message += " No external marketplaces are allowed."
			}
		}
		return AddedMarketplace{}, fmt.Errorf("%s", message)
	}
	known, err := m.Known(ctx)
	if err != nil {
		return AddedMarketplace{}, err
	}
	for _, name := range properties(known) {
		if equalJSON(obj(obj(known.Get(name)).Get("source")), source) {
			return AddedMarketplace{name, true, source}, m.declare(ctx, name, source, scope)
		}
	}
	catalog, err := m.options.Resolver.FetchWithProgress(ctx, source, progress)
	if err != nil {
		return AddedMarketplace{}, err
	}
	name := catalog.String("name")
	if err = validateOfficialSource(name, source); err != nil {
		return AddedMarketplace{}, err
	}
	location := m.location(source, catalog)
	known, err = m.Known(ctx)
	if err != nil {
		return AddedMarketplace{}, err
	}
	old := obj(known.Get(name))
	if known.Get(name) != nil && old.IsObject() {
		if seed := m.seed(old.String("installLocation")); seed != "" {
			return AddedMarketplace{}, fmt.Errorf("Marketplace '%s' is seed-managed (%s). To use a different source, ask your admin to update the seed, or use a different marketplace name.", name, seed)
		}
		kind := obj(old.Get("source")).String("source")
		oldPath := old.String("installLocation")
		if kind != "file" && kind != "directory" && filepath.Clean(oldPath) != filepath.Clean(location) && m.strict(oldPath) {
			if err = os.RemoveAll(oldPath); err != nil {
				return AddedMarketplace{}, err
			}
		}
	}
	known.Set(name, raw(rec(field("source", source), field("installLocation", location), field("lastUpdated", m.options.Resolver.options.Now().UTC().Format("2006-01-02T15:04:05.000Z")))))
	if err = m.save(ctx, known); err != nil {
		return AddedMarketplace{}, err
	}
	return AddedMarketplace{name, false, source}, m.declare(ctx, name, source, scope)
}
func validateOfficialSource(name string, source protocol.JSONObject) error {
	reserved := map[string]bool{"claude-code-marketplace": true, "claude-code-plugins": true, "claude-plugins-official": true, "anthropic-marketplace": true, "anthropic-plugins": true, "agent-skills": true, "life-sciences": true, "knowledge-work-plugins": true}
	if !reserved[strings.ToLower(name)] {
		return nil
	}
	kind := source.String("source")
	if kind == "github" && strings.HasPrefix(strings.ToLower(source.String("repo")), "anthropics/") {
		return nil
	}
	if kind == "git" {
		u := strings.ToLower(source.String("url"))
		if strings.Contains(u, "github.com/anthropics/") || strings.Contains(u, "git@github.com:anthropics/") {
			return nil
		}
	}
	if kind == "github" || kind == "git" {
		return fmt.Errorf("The name '%s' is reserved for official Anthropic marketplaces. Only repositories from 'github.com/anthropics/' can use this name.", name)
	}
	return fmt.Errorf("The name '%s' is reserved for official Anthropic marketplaces and can only be used with GitHub sources from the 'anthropics' organization.", name)
}
func (m *MarketplaceManager) declare(ctx context.Context, name string, source protocol.JSONObject, scope string) error {
	settings, err := m.options.ReadSettings(ctx, scope, "")
	if err != nil {
		return err
	}
	entries := obj(settings.Get("extraKnownMarketplaces"))
	entries.Set(name, raw(rec(field("source", source))))
	// Original saveMarketplaceToSettings intentionally does not throw the writer result.
	_ = m.options.WriteSettings(ctx, scope, rec(field("extraKnownMarketplaces", entries)), nil)
	return m.options.ClearCaches(ctx)
}
func (m *MarketplaceManager) Remove(ctx context.Context, name string) error {
	known, err := m.Known(ctx)
	if err != nil {
		return err
	}
	entry := obj(known.Get(name))
	if known.Get(name) == nil || !entry.IsObject() {
		return fmt.Errorf("Marketplace '%s' not found", name)
	}
	if seed := m.seed(entry.String("installLocation")); seed != "" {
		return fmt.Errorf("Marketplace '%s' is registered from the read-only seed directory (%s) and will be re-registered on next startup. To stop using its plugins: claude plugin disable <plugin>@%s", name, seed, name)
	}
	known.Delete(name)
	if err = m.save(ctx, known); err != nil {
		return err
	}
	for _, path := range []string{filepath.Join(m.options.Resolver.options.PluginsDir, "marketplaces", name), filepath.Join(m.options.Resolver.options.PluginsDir, "marketplaces", name+".json")} {
		if err = os.RemoveAll(path); err != nil {
			return err
		}
	}
	for _, scope := range []string{"user", "project", "local"} {
		settings, err := m.options.ReadSettings(ctx, scope, "")
		if err != nil {
			return err
		}
		patch := rec()
		undefined := [][]string{}
		markets := obj(settings.Get("extraKnownMarketplaces"))
		if len(markets.Get(name)) > 0 && string(markets.Get(name)) != "null" && string(markets.Get(name)) != "false" {
			patch.Set("extraKnownMarketplaces", raw(markets))
			undefined = append(undefined, []string{"extraKnownMarketplaces", name})
		}
		enabled := obj(settings.Get("enabledPlugins"))
		for _, id := range properties(enabled) {
			if strings.HasSuffix(id, "@"+name) {
				patch.Set("enabledPlugins", raw(enabled))
				undefined = append(undefined, []string{"enabledPlugins", id})
			}
		}
		if len(undefined) > 0 {
			_ = m.options.WriteSettings(ctx, scope, patch, undefined)
		}
	}
	return m.options.ClearCaches(ctx)
}
func (m *MarketplaceManager) Update(ctx context.Context, name string, progress func(string)) error {
	known, err := m.Known(ctx)
	if err != nil {
		return err
	}
	entry := obj(known.Get(name))
	if known.Get(name) == nil || !entry.IsObject() {
		return fmt.Errorf("Marketplace '%s' not found. Available marketplaces: %s", name, strings.Join(properties(known), ", "))
	}
	source := obj(entry.Get("source"))
	if source.String("source") == "settings" {
		return nil
	}
	refresh := func() error {
		location := entry.String("installLocation")
		if seed := m.seed(location); seed != "" {
			return fmt.Errorf("Marketplace '%s' is seed-managed (%s) and its content is controlled by the seed image. To update: ask your admin to update the seed.", name, seed)
		}
		kind := source.String("source")
		if kind != "file" && kind != "directory" && !m.strict(location) {
			return fmt.Errorf("Marketplace '%s' has a corrupted installLocation (%s) — expected a path inside %s. This can happen after cross-platform path writes or manual edits to known_marketplaces.json. Run: claude plugin marketplace remove \"%s\" and re-add it.", name, location, filepath.Join(m.options.Resolver.options.PluginsDir, "marketplaces"), name)
		}
		if name == "claude-plugins-official" && m.options.Resolver.officialGCS(ctx, location) {
			entry.Set("lastUpdated", raw(m.options.Resolver.options.Now().UTC().Format("2006-01-02T15:04:05.000Z")))
			known.Set(name, raw(entry))
			return m.save(ctx, known)
		}
		if kind == "file" || kind == "directory" {
			if progress != nil {
				progress("Validating local marketplace")
			}
			_, err = readMarketplaceCLI(location)
		} else if kind == "url" {
			err = m.options.Resolver.downloadMarketplaceURL(ctx, source, location, progress)
		} else if kind == "git" || kind == "github" {
			err = m.options.Resolver.refreshGit(ctx, source, location, progress)
			if err == nil {
				if _, validation := readMarketplaceCLI(location); validation != nil {
					display := source.String("repo")
					if kind == "git" {
						display = redactGitURL(source.String("url"))
					}
					reason := "This marketplace may have been deprecated or moved to a new location."
					if name == "claude-code-plugins" {
						reason = "We've deprecated \"claude-code-plugins\" in favor of \"claude-plugins-official\"."
					}
					err = fmt.Errorf("The marketplace.json file is no longer present in this repository.\n\n%s\nSource: %s\n\nYou can remove this marketplace with: claude plugin marketplace remove \"%s\"", reason, display, name)
				}
			}
		} else {
			err = fmt.Errorf("Unsupported marketplace source type for refresh")
		}
		if err != nil {
			return err
		}
		entry.Set("lastUpdated", raw(m.options.Resolver.options.Now().UTC().Format("2006-01-02T15:04:05.000Z")))
		known.Set(name, raw(entry))
		return m.save(ctx, known)
	}
	if err = refresh(); err != nil {
		if errors.Is(err, syscall.EISDIR) {
			err = fmt.Errorf("EISDIR: illegal operation on a directory, read")
		}
		return fmt.Errorf("Failed to refresh marketplace '%s': %s", name, err)
	}
	return m.options.ClearCaches(ctx)
}
func (m *MarketplaceManager) UpdateAll(ctx context.Context) error {
	known, err := m.Known(ctx)
	if err != nil {
		return err
	}
	for _, name := range properties(known) {
		entry := obj(known.Get(name))
		source := obj(entry.Get("source"))
		if m.seed(entry.String("installLocation")) != "" || source.String("source") == "settings" {
			continue
		}
		if name == "claude-plugins-official" && m.options.Resolver.officialGCS(ctx, entry.String("installLocation")) {
			entry.Set("lastUpdated", raw(m.options.Resolver.options.Now().UTC().Format("2006-01-02T15:04:05.000Z")))
			known.Set(name, raw(entry))
			continue
		}
		catalog, err := m.options.Resolver.Fetch(ctx, source)
		if err != nil {
			continue
		}
		entry.Set("lastUpdated", raw(m.options.Resolver.options.Now().UTC().Format("2006-01-02T15:04:05.000Z")))
		entry.Set("installLocation", raw(m.location(source, catalog)))
		known.Set(name, raw(entry))
	}
	if err = m.save(ctx, known); err != nil {
		return err
	}
	return m.options.ClearCaches(ctx)
}
