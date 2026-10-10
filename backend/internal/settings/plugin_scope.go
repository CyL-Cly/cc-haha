package settings

import (
	"context"
	"encoding/json"
	"path/filepath"
)

// UpdateEnabledPluginAtScope extends the same atomic settings transaction to
// project/local scopes, retaining the original local Git-ignore continuation.
func (s *Service) UpdateEnabledPluginAtScope(ctx context.Context, id string, enabled *bool, scope, root string) error {
	if scope == "user" {
		return s.UpdateEnabledPlugin(ctx, id, enabled)
	}
	if scope != "project" && scope != "local" {
		return badRequest("Invalid plugin settings scope")
	}
	resolved, err := s.projectRoot(root, scope)
	if err != nil {
		return err
	}
	name := "settings.json"
	if scope == "local" {
		name = "settings.local.json"
	}
	path := filepath.Join(resolved, ".claude", name)
	err = s.options.Store.WithWriteLock(path, func() error {
		current, err := s.readJSON(ctx, path, s.configDir())
		if err != nil {
			return err
		}
		plugins := shallowMerge(objectValue(current["enabledPlugins"]))
		if enabled == nil {
			delete(plugins, id)
		} else {
			data, _ := json.Marshal(*enabled)
			plugins[id] = data
		}
		current["enabledPlugins"] = objectJSON(plugins)
		return s.writeJSON(path, current)
	})
	if err != nil {
		return err
	}
	if scope == "local" {
		s.background.Add(1)
		go func() {
			defer s.background.Done()
			if err := s.options.GitIgnore(context.WithoutCancel(ctx), ".claude/settings.local.json", resolved); err != nil {
				s.options.LogError(err)
			}
		}()
	}
	return nil
}
