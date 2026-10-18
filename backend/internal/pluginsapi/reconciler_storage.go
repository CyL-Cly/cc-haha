package pluginsapi

import (
	"context"
	"os"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/pluginzip"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// RegisterSeeds uses validated seed metadata and probes the current mount,
// rather than trusting paths from the machine which built the seed image.
func (m *MarketplaceManager) RegisterSeeds(ctx context.Context) (bool, error) {
	if len(m.options.Seeds) == 0 {
		return false, nil
	}
	known, err := m.Known(ctx)
	if err != nil {
		return false, err
	}
	claimed, changed := map[string]bool{}, false
	for _, seed := range m.options.Seeds {
		if err := ctx.Err(); err != nil {
			return changed, err
		}
		config, err := ReadKnownMarketplaces(ctx, seed)
		if err != nil {
			continue
		}
		for _, name := range properties(config) {
			if claimed[name] {
				continue
			}
			location := ""
			for _, candidate := range []string{filepath.Join(seed, "marketplaces", name), filepath.Join(seed, "marketplaces", name+".json")} {
				if _, err := ReadMarketplace(candidate); err == nil {
					location = candidate
					break
				}
			}
			if location == "" {
				continue
			}
			claimed[name] = true
			entry := obj(config.Get(name))
			desired := rec(field("source", obj(entry.Get("source"))), field("installLocation", location), field("lastUpdated", stringValue(entry.Get("lastUpdated"))), field("autoUpdate", false))
			if equalJSON(obj(known.Get(name)), desired) {
				continue
			}
			known.Set(name, raw(desired))
			changed = true
		}
	}
	if changed {
		err = m.save(ctx, known)
	}
	return changed, err
}

// SyncZipMarketplaces also runs in steady state. Existing offline metadata is
// merged before current mappings so an ephemeral home does not erase peers.
func (m *MarketplaceManager) SyncZipMarketplaces(ctx context.Context, archive *pluginzip.Service) error {
	if archive == nil || !archive.CacheEnabled() {
		return nil
	}
	path := archive.CachePath()
	known, err := m.Known(ctx)
	if err != nil {
		known = rec()
	}
	for _, name := range properties(known) {
		if err := ctx.Err(); err != nil {
			return err
		}
		location := obj(known.Get(name)).String("installLocation")
		if location == "" {
			continue
		}
		for _, candidate := range []string{filepath.Join(location, ".claude-plugin", "marketplace.json"), filepath.Join(location, "marketplace.json"), location} {
			content, err := os.ReadFile(candidate)
			if err != nil {
				continue
			}
			_ = archive.AtomicWrite(ctx, filepath.Join(path, pluginzip.MarketplaceRelativePath(name)), content)
			break
		}
	}
	previous, err := ReadKnownMarketplaces(ctx, path)
	if err != nil {
		previous = protocol.NewJSONObject()
	}
	for _, property := range known.OwnProperties() {
		_ = previous.SetPropertyJSON(property.NameJSON, property.Value)
	}
	content, err := previous.MarshalJSON()
	if err != nil {
		return err
	}
	return archive.AtomicWrite(ctx, filepath.Join(path, "known_marketplaces.json"), content)
}
