package pluginsapi

import (
	"context"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// EnforceDelisting consumes marketplace manifests and the existing installed
// registry. Managed-only records and previously flagged IDs are left intact.
// A failed uninstall is still flagged, as the original startup caller does.
func (m *MarketplaceManager) EnforceDelisting(ctx context.Context, operations *Service, now time.Time) ([]string, error) {
	newly := []string{}
	if operations == nil {
		return newly, nil
	}
	if err := m.options.Flagged.Load(ctx, now); err != nil {
		return newly, err
	}
	flags := m.options.Flagged.Snapshot()
	installed, err := operations.options.Installed.Session(ctx)
	if err != nil {
		return newly, err
	}
	known, err := m.Known(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return newly, ctx.Err()
		}
		known = rec()
	}
	for _, market := range properties(known) {
		if err := ctx.Err(); err != nil {
			return newly, err
		}
		entry := obj(known.Get(market))
		catalog, err := ReadMarketplace(entry.String("installLocation"))
		if err != nil {
			catalog, err = m.options.Resolver.Fetch(ctx, obj(entry.Get("source")))
		}
		if err != nil || string(catalog.Get("forceRemoveDeletedPlugins")) != "true" {
			continue
		}
		names := map[string]bool{}
		for _, plugin := range arr(catalog.Get("plugins")) {
			names[obj(plugin).String("name")] = true
		}
		for _, id := range properties(obj(installed.Get("plugins"))) {
			name, suffix := splitID(id)
			if suffix != market || names[name] || flags.Get(id) != nil {
				continue
			}
			records := arr(obj(installed.Get("plugins")).Get(id))
			hasEditable := false
			for _, record := range records {
				switch obj(record).String("scope") {
				case "user", "project", "local":
					hasEditable = true
				}
			}
			if !hasEditable {
				continue
			}
			for _, record := range records {
				scope := obj(record).String("scope")
				switch scope {
				case "user", "project", "local":
					_, _ = operations.Uninstall(ctx, id, scope, "", false)
				}
			}
			if err := ctx.Err(); err != nil {
				return newly, err
			}
			_ = m.options.Flagged.Add(ctx, id, now)
			flags = m.options.Flagged.Snapshot()
			newly = append(newly, id)
		}
	}
	return newly, nil
}

func (m *MarketplaceManager) FlaggedPlugins() protocol.JSONObject {
	return m.options.Flagged.Snapshot()
}
