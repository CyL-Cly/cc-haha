package pluginsapi

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strings"
)

// Install is the ordinary pure operation used by the CLI handler. Intent is
// committed once for the dependency closure before materialization starts.
func (s *Service) Install(ctx context.Context, plugin, scope string) (protocol.JSONObject, error) {
	if s.core == nil {
		return protocol.JSONObject{}, fmt.Errorf("plugin install requires actual CLI operation bindings")
	}
	name, market := splitID(plugin)
	var root *MarketplacePlugin
	var err error
	if market != "" {
		root, err = s.options.LookupPlugin(ctx, plugin)
	} else {
		known, e := s.options.KnownMarketplaces(ctx)
		if e != nil {
			return protocol.JSONObject{}, e
		}
		for _, candidate := range properties(known) {
			info, e := s.options.LookupPlugin(ctx, name+"@"+candidate)
			if e == nil && info != nil {
				root = info
				market = candidate
				break
			}
		}
	}
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if root == nil {
		where := "any configured marketplace"
		if market != "" {
			where = "marketplace \"" + market + "\""
		}
		return protocol.JSONObject{}, bad("Plugin \"" + name + "\" not found in " + where)
	}
	id := root.Entry.String("name") + "@" + market
	policy, err := s.options.ReadSettings(ctx, "managed", "")
	if err != nil {
		return protocol.JSONObject{}, err
	}
	blocked := obj(policy.Get("enabledPlugins"))
	if string(blocked.Get(id)) == "false" {
		return protocol.JSONObject{}, bad("Plugin \"" + root.Entry.String("name") + "\" is blocked by your organization's policy and cannot be installed")
	}
	if value := root.Entry.Get("source"); len(value) > 0 && value[0] == '"' && root.InstallLocation == "" {
		return protocol.JSONObject{}, bad("Cannot install local plugin \"" + root.Entry.String("name") + "\" without marketplace install location")
	}
	allowed := map[string]bool{}
	known, err := s.options.KnownMarketplaces(ctx)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if catalog, e := ReadMarketplace(obj(known.Get(market)).String("installLocation")); e == nil {
		var values []string
		_ = json.Unmarshal(catalog.Get("allowCrossMarketplaceDependenciesOn"), &values)
		for _, v := range values {
			allowed[v] = true
		}
	}
	settings, err := s.options.ReadSettings(ctx, scope, "")
	if err != nil {
		return protocol.JSONObject{}, err
	}
	enabled := obj(settings.Get("enabledPlugins"))
	infos := map[string]*MarketplacePlugin{id: root}
	visited := map[string]bool{}
	stack := []string{}
	closure := []string{}
	var walk func(string, string) error
	walk = func(current, requiredBy string) error {
		if current != id && pluginregistry.IsEnabledSetting(enabled.Get(current)) {
			return nil
		}
		_, currentMarket := splitID(current)
		if currentMarket != market && !allowed[currentMarket] {
			where := "a different marketplace"
			hint := ""
			if currentMarket != "" {
				where = "marketplace \"" + currentMarket + "\""
				hint = " Add \"" + currentMarket + "\" to allowCrossMarketplaceDependenciesOn in the ROOT marketplace's marketplace.json (the marketplace of the plugin you're installing — only its allowlist applies; no transitive trust)."
			}
			return bad("Dependency \"" + current + "\" (required by " + requiredBy + ") is in " + where + ", which is not in the allowlist — cross-marketplace dependencies are blocked by default. Install it manually first." + hint)
		}
		for _, v := range stack {
			if v == current {
				return bad("Dependency cycle: " + strings.Join(append(append([]string{}, stack...), current), " → "))
			}
		}
		if visited[current] {
			return nil
		}
		visited[current] = true
		info := infos[current]
		if info == nil {
			var e error
			info, e = s.options.LookupPlugin(ctx, current)
			if e != nil {
				return e
			}
			if info != nil {
				infos[current] = info
			}
		}
		if info == nil {
			_, m := splitID(current)
			where := "in any configured marketplace"
			if m != "" {
				where = ". Is the \"" + m + "\" marketplace added?"
				return bad("Dependency \"" + current + "\" (required by " + requiredBy + ") not found" + where)
			}
			return bad("Dependency \"" + current + "\" (required by " + requiredBy + ") not found " + where)
		}
		stack = append(stack, current)
		var deps []string
		_ = json.Unmarshal(info.Entry.Get("dependencies"), &deps)
		for _, dep := range deps {
			if e := walk(pluginregistry.QualifyDependency(dep, current), current); e != nil {
				return e
			}
		}
		stack = stack[:len(stack)-1]
		closure = append(closure, current)
		return nil
	}
	if err = walk(id, id); err != nil {
		return protocol.JSONObject{}, err
	}
	for _, member := range closure {
		if member != id && string(blocked.Get(member)) == "false" {
			return protocol.JSONObject{}, bad("Plugin \"" + root.Entry.String("name") + "\" depends on \"" + member + "\", which is blocked by your organization's policy")
		}
	}
	if err = s.core.WriteEnabledMany(ctx, scope, closure); err != nil {
		return protocol.JSONObject{}, bad("Failed to update settings: " + err.Error())
	}
	projectPath := project(scope, "", s.options.WorkDir)
	for _, member := range closure {
		info := infos[member]
		if info == nil {
			continue
		}
		local := ""
		source := info.Entry.Get("source")
		if len(source) > 0 && source[0] == '"' {
			local, err = pluginregistry.PathWithinBase(info.InstallLocation, stringValue(source))
			if err != nil {
				return protocol.JSONObject{}, err
			}
		}
		if _, err = s.options.Materializer.InstallAndRegister(ctx, member, info.Entry, scope, projectPath, local); err != nil {
			return protocol.JSONObject{}, err
		}
	}
	if err = s.options.ClearCaches(ctx); err != nil {
		return protocol.JSONObject{}, err
	}
	suffix := ""
	if n := len(closure) - 1; n > 0 {
		noun := "dependencies"
		if n == 1 {
			noun = "dependency"
		}
		suffix = fmt.Sprintf(" (+ %d %s)", n, noun)
	}
	return success("Successfully installed plugin: " + id + " (scope: " + scope + ")" + suffix), nil
}
func (s *Service) DisableAll(ctx context.Context) (protocol.JSONObject, error) {
	enabled, err := s.options.EditableEnabled(ctx)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if len(enabled) == 0 {
		return success("No enabled plugins to disable"), nil
	}
	disabled := 0
	failures := []string{}
	// The ordered writer provides the original settings key insertion order.
	ids, err := s.core.EnabledOrder(ctx)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	for _, id := range ids {
		if !enabled[id] {
			continue
		}
		if _, err = s.SetEnabled(ctx, id, false, "", ""); err != nil {
			failures = append(failures, id+": "+err.Error())
		} else {
			disabled++
		}
	}
	noun := "plugins"
	if disabled == 1 {
		noun = "plugin"
	}
	message := fmt.Sprintf("Disabled %d %s", disabled, noun)
	if len(failures) > 0 {
		return protocol.JSONObject{}, bad(fmt.Sprintf("%s, %d failed:\n%s", message, len(failures), strings.Join(failures, "\n")))
	}
	return success(message), nil
}
