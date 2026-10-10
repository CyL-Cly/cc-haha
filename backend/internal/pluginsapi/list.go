package pluginsapi

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

var capabilityKeys = []string{"commands", "agents", "skills", "hooks", "mcpServers", "lspServers"}

func emptyCapabilities() protocol.JSONObject {
	c := rec()
	for _, key := range capabilityKeys {
		c.Set(key, raw([]string{}))
	}
	return c
}
func capabilityCounts(c protocol.JSONObject) protocol.JSONObject {
	o := rec()
	for _, key := range capabilityKeys {
		o.Set(key, raw(len(arr(c.Get(key)))))
	}
	return o
}
func summary(d protocol.JSONObject) protocol.JSONObject {
	o := rec()
	for _, key := range []string{"id", "name", "marketplace", "scope", "enabled", "hasErrors", "isBuiltin", "version", "description", "authorName", "installPath", "projectPath", "componentCounts", "errors"} {
		o.Set(key, d.Get(key))
	}
	return o
}
func pickInstallation(entries []json.RawMessage, cwd string) *protocol.JSONObject {
	for _, scope := range []string{"local", "project"} {
		for _, v := range entries {
			e := obj(v)
			root := e.String("projectPath")
			if cwd != "" && root != "" && (cwd == root || strings.HasPrefix(cwd, root+string(filepath.Separator))) && e.String("scope") == scope {
				return &e
			}
		}
	}
	for _, v := range entries {
		e := obj(v)
		if e.String("scope") == "user" {
			return &e
		}
	}
	if len(entries) > 0 {
		e := obj(entries[0])
		return &e
	}
	return nil
}
func (s *Service) collect(ctx context.Context, cwd string) ([]protocol.JSONObject, map[string]protocol.JSONObject, []protocol.JSONObject, error) {
	state, err := s.load(ctx, false)
	if err != nil {
		return nil, nil, nil, err
	}
	installed, err := s.options.Installed.Session(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	known, err := s.options.KnownMarketplaces(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	ids := map[string]bool{}
	loaded := map[string]pluginregistry.Loaded{}
	entries := obj(installed.Get("plugins"))
	for _, key := range properties(entries) {
		ids[key] = true
	}
	for _, p := range append(state.Enabled, state.Disabled...) {
		if !strings.HasSuffix(p.Source, "@inline") {
			ids[p.Source] = true
			loaded[p.Source] = p
		}
	}
	keys := []string{}
	for key := range ids {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := utf16.Encode([]rune(keys[i])), utf16.Encode([]rune(keys[j]))
		for n := 0; n < len(a) && n < len(b); n++ {
			if a[n] != b[n] {
				return a[n] < b[n]
			}
		}
		return len(a) < len(b)
	})
	plugins := []protocol.JSONObject{}
	details := map[string]protocol.JSONObject{}
	for _, id := range keys {
		var p *pluginregistry.Loaded
		if value, ok := loaded[id]; ok {
			p = &value
		}
		d, err := s.serialize(ctx, id, pickInstallation(arr(entries.Get(id)), cwd), p, state.Errors)
		if err != nil {
			return nil, nil, nil, err
		}
		details[id] = d
		plugins = append(plugins, summary(d))
	}
	// This deliberately retains the original public service's .marketplaces
	// lookup. Its valid ordinary mapping has no such property.
	marketplaces := []protocol.JSONObject{}
	for _, key := range properties(obj(known.Get("marketplaces"))) {
		e := obj(obj(known.Get("marketplaces")).Get(key))
		count := 0
		for _, p := range plugins {
			if p.String("marketplace") == key {
				count++
			}
		}
		source := obj(e.Get("source"))
		display := "Unknown source"
		switch source.String("source") {
		case "github":
			display = source.String("repo")
		case "url", "git":
			display = source.String("url")
		case "file", "directory":
			display = source.String("path")
		case "settings":
			display = "settings:" + source.String("name")
		}
		row := rec(field("name", key), field("source", display), field("autoUpdate", string(e.Get("autoUpdate")) != "false"), field("installedCount", count))
		row.Set("lastUpdated", e.Get("lastUpdated"))
		marketplaces = append(marketplaces, row)
	}
	sortEntries(marketplaces)
	return plugins, details, marketplaces, nil
}
func (s *Service) List(ctx context.Context, cwd string) (protocol.JSONObject, error) {
	plugins, _, markets, err := s.collect(ctx, cwd)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	enabled, errorCount := 0, 0
	for _, p := range plugins {
		if string(p.Get("enabled")) == "true" {
			enabled++
		}
		errorCount += len(arr(p.Get("errors")))
	}
	return rec(field("plugins", plugins), field("marketplaces", markets), field("summary", rec(field("total", len(plugins)), field("enabled", enabled), field("errorCount", errorCount), field("marketplaceCount", len(markets))))), nil
}
func (s *Service) Detail(ctx context.Context, id, cwd string) (protocol.JSONObject, error) {
	_, details, _, err := s.collect(ctx, cwd)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if d, ok := details[id]; ok {
		return d, nil
	}
	return protocol.JSONObject{}, missing("Plugin not found: " + id)
}
func (s *Service) serialize(ctx context.Context, id string, installation *protocol.JSONObject, p *pluginregistry.Loaded, failures []protocol.JSONObject) (protocol.JSONObject, error) {
	name, market := splitID(id)
	if market == "" {
		market = "unknown"
	}
	scope := "user"
	if installation != nil {
		scope = installation.String("scope")
	}
	errors := []string{}
	for _, e := range failures {
		if e.String("source") == id || e.String("plugin") == name || strings.HasPrefix(e.String("source"), name+"@") {
			errors = append(errors, ErrorMessage(e))
		}
	}
	d := rec(field("id", id), field("name", name), field("marketplace", market), field("scope", scope), field("enabled", false), field("hasErrors", len(errors) > 0), field("isBuiltin", false), field("errors", errors))
	if installation != nil {
		d.Set("installPath", installation.Get("installPath"))
		d.Set("projectPath", installation.Get("projectPath"))
	}
	c := emptyCapabilities()
	commands, agents, hooks, skills, mcp := []protocol.JSONObject{}, []protocol.JSONObject{}, []protocol.JSONObject{}, []protocol.JSONObject{}, []protocol.JSONObject{}
	if p != nil {
		d.Set("name", raw(p.Name))
		d.Set("enabled", raw(p.Enabled))
		d.Set("isBuiltin", raw(p.IsBuiltin))
		d.Set("version", p.Manifest.Get("version"))
		d.Set("description", p.Manifest.Get("description"))
		d.Set("authorName", obj(p.Manifest.Get("author")).Get("name"))
		if p.IsBuiltin {
			definition, err := s.options.BuiltinDetail(p.Name)
			if err != nil {
				return protocol.JSONObject{}, err
			}
			for _, v := range arr(definition.Get("skills")) {
				skill := obj(v)
				skills = append(skills, rec(field("name", skill.String("name")), field("description", skill.String("description"))))
			}
			builtinHooks := obj(definition.Get("hooks"))
			hooks = hookEntries(&builtinHooks)
			builtinMCP := obj(definition.Get("mcpServers"))
			mcp = mcpEntries(p.Name, &builtinMCP, false)
			c.Set("hooks", raw(properties(builtinHooks)))
		} else {
			commands = markdownEntries(p.Name, append([]string{p.CommandsPath}, p.CommandsPaths...), true, false)
			agents = markdownEntries(p.Name, append([]string{p.AgentsPath}, p.AgentsPaths...), false, true)
			skills = skillEntries(p.Name, append([]string{p.SkillsPath}, p.SkillsPaths...))
			hooks = hookEntries(p.HooksConfig)
			mcp = mcpEntries(p.Name, p.MCPServers, true)
			events := []string{}
			seen := map[string]bool{}
			for _, h := range hooks {
				event := h.String("event")
				if !seen[event] {
					seen[event] = true
					events = append(events, event)
				}
			}
			c.Set("hooks", raw(events))
			if p.LSPServers != nil {
				c.Set("lspServers", raw(properties(*p.LSPServers)))
			}
		}
		for _, entry := range []struct {
			key    string
			values []protocol.JSONObject
		}{{"commands", commands}, {"agents", agents}, {"skills", skills}, {"mcpServers", mcp}} {
			names := []string{}
			for _, value := range entry.values {
				names = append(names, value.String("name"))
			}
			c.Set(entry.key, raw(names))
		}
	}
	d.Set("componentCounts", raw(capabilityCounts(c)))
	d.Set("capabilities", raw(c))
	d.Set("commandEntries", raw(commands))
	d.Set("agentEntries", raw(agents))
	d.Set("hookEntries", raw(hooks))
	d.Set("skillEntries", raw(skills))
	d.Set("mcpServerEntries", raw(mcp))
	return d, nil
}
func sortEntries(entries []protocol.JSONObject) {
	c := collate.New(language.English)
	sort.SliceStable(entries, func(i, j int) bool { return c.CompareString(entries[i].String("name"), entries[j].String("name")) < 0 })
}
func (s *Service) Reload(ctx context.Context, cwd string) (protocol.JSONObject, error) {
	if err := s.options.ClearCaches(ctx); err != nil {
		return protocol.JSONObject{}, err
	}
	state, err := s.load(ctx, false)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	skills, err := s.options.SkillsCount(ctx)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	agents, err := s.options.AgentsCount(ctx, cwd)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	_ = s.options.LoadHooks(ctx)
	hooks, mcp, lsp := 0, 0, 0
	for _, p := range state.Enabled {
		if p.HooksConfig != nil {
			for _, key := range properties(*p.HooksConfig) {
				for _, v := range arr(p.HooksConfig.Get(key)) {
					hooks += len(arr(obj(v).Get("hooks")))
				}
			}
		}
		if p.MCPServers != nil {
			mcp += len(p.MCPServers.OwnProperties())
		}
		if p.LSPServers != nil {
			lsp += len(p.LSPServers.OwnProperties())
		}
	}
	return rec(field("ok", true), field("summary", rec(field("enabled", len(state.Enabled)), field("disabled", len(state.Disabled)), field("skills", skills), field("agents", agents), field("hooks", hooks), field("mcpServers", mcp), field("lspServers", lsp), field("errors", len(state.Errors))))), nil
}
