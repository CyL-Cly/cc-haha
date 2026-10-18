package pluginsapi

import (
	"context"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strings"
)

// SetAutoUpdate updates both the physical catalog and its original declaring
// settings scope; seed content remains controlled by the seed image.
func (m *MarketplaceManager) SetAutoUpdate(ctx context.Context, name string, enabled bool) error {
	known, err := m.Known(ctx)
	if err != nil {
		return err
	}
	entry := obj(known.Get(name))
	if known.Get(name) == nil {
		return fmt.Errorf("Marketplace '%s' not found. Available marketplaces: %s", name, strings.Join(properties(known), ", "))
	}
	if seed := m.seed(entry.String("installLocation")); seed != "" {
		return fmt.Errorf("Marketplace '%s' is seed-managed (%s) and auto-update is always disabled for seed content. To update: ask your admin to update the seed.", name, seed)
	}
	if string(entry.Get("autoUpdate")) == fmt.Sprint(enabled) {
		return nil
	}
	entry.Set("autoUpdate", raw(enabled))
	known.Set(name, raw(entry))
	if err = m.save(ctx, known); err != nil {
		return err
	}
	for _, scope := range []string{"local", "project", "user"} {
		settings, err := m.options.ReadSettings(ctx, scope, "")
		if err != nil {
			return err
		}
		declared := obj(obj(settings.Get("extraKnownMarketplaces")).Get(name))
		if len(declared.OwnProperties()) == 0 {
			continue
		}
		declared = rec(protocol.JSONField{Name: "source", Value: declared.Get("source")}, field("autoUpdate", enabled))
		_ = m.options.WriteSettings(ctx, scope, rec(field("extraKnownMarketplaces", rec(field(name, declared)))), nil)
		break
	}
	return nil
}
