package pluginsapi

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func stringValue(v json.RawMessage) string {
	var value string
	_ = json.Unmarshal(v, &value)
	return value
}
func reverseDependents(id string, plugins []pluginregistry.Loaded) []string {
	target, _ := splitID(id)
	out := []string{}
	for _, p := range plugins {
		if !p.Enabled || p.Source == id {
			continue
		}
		for _, dep := range arr(p.Manifest.Get("dependencies")) {
			qualified := pluginregistry.QualifyDependency(stringValue(dep), p.Source)
			_, market := splitID(qualified)
			if market != "" && qualified == id || market == "" && qualified == target {
				out = append(out, p.Name)
				break
			}
		}
	}
	return out
}
func dependencySuffix(deps []string) string {
	if len(deps) == 0 {
		return ""
	}
	return " — warning: required by " + strings.Join(deps, ", ")
}
func (s *Service) findSettings(ctx context.Context, plugin, cwd string) (string, string, error) {
	for _, scope := range []string{"local", "project", "user"} {
		settings, err := s.source(ctx, scope, cwd)
		if err != nil {
			return "", "", err
		}
		for _, key := range properties(obj(settings.Get("enabledPlugins"))) {
			if strings.Contains(plugin, "@") && key == plugin || !strings.Contains(plugin, "@") && strings.HasPrefix(key, plugin+"@") {
				return key, scope, nil
			}
		}
	}
	return "", "", nil
}
func (s *Service) SetEnabled(ctx context.Context, plugin string, enabled bool, scope, cwd string) (protocol.JSONObject, error) {
	operation := "disable"
	if enabled {
		operation = "enable"
	}
	if s.options.IsBuiltin(plugin) {
		err := s.options.WriteEnabled(ctx, "user", "", plugin, &enabled)
		if err != nil {
			return protocol.JSONObject{}, bad("Failed to " + operation + " built-in plugin: " + err.Error())
		}
		if err = s.options.ClearCaches(ctx); err != nil {
			return protocol.JSONObject{}, err
		}
		name, _ := splitID(plugin)
		return success("Successfully " + operation + "d built-in plugin: " + name), nil
	}
	id, foundScope, err := s.findSettings(ctx, plugin, cwd)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	resolvedScope := scope
	if scope != "" {
		if id == "" {
			if strings.Contains(plugin, "@") {
				id = plugin
			} else {
				return protocol.JSONObject{}, bad("Plugin \"" + plugin + "\" not found in settings. Use plugin@marketplace format.")
			}
		}
	} else if id != "" {
		resolvedScope = foundScope
	} else if strings.Contains(plugin, "@") {
		id = plugin
		resolvedScope = "user"
	} else {
		return protocol.JSONObject{}, bad("Plugin \"" + plugin + "\" not found in any editable settings scope. Use plugin@marketplace format.")
	}
	if enabled {
		policy, err := s.source(ctx, "managed", "")
		if err != nil {
			return protocol.JSONObject{}, err
		}
		if string(obj(policy.Get("enabledPlugins")).Get(id)) == "false" {
			return protocol.JSONObject{}, bad("Plugin \"" + id + "\" is blocked by your organization's policy and cannot be enabled")
		}
	}
	settings, err := s.source(ctx, resolvedScope, cwd)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	value := obj(settings.Get("enabledPlugins")).Get(id)
	precedence := map[string]int{"user": 0, "project": 1, "local": 2}
	override := scope != "" && foundScope != "" && precedence[scope] > precedence[foundScope]
	if scope != "" && value == nil && foundScope != "" && foundScope != scope && !override {
		return protocol.JSONObject{}, bad("Plugin \"" + plugin + "\" is installed at " + foundScope + " scope, not " + scope + ". Use --scope " + foundScope + " or omit --scope to auto-detect.")
	}
	current := false
	if scope != "" && !override {
		current = pluginregistry.IsEnabledSetting(value)
	} else {
		editable, err := s.options.EditableEnabled(ctx)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		current = editable[id]
	}
	if current == enabled {
		suffix := ""
		if scope != "" {
			suffix = " at " + scope + " scope"
		}
		state := "disabled"
		if enabled {
			state = "enabled"
		}
		return protocol.JSONObject{}, bad("Plugin \"" + plugin + "\" is already " + state + suffix)
	}
	deps := []string{}
	if !enabled {
		loaded, err := s.load(ctx, true)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		deps = reverseDependents(id, append(loaded.Enabled, loaded.Disabled...))
	}
	if err = s.options.WriteEnabled(ctx, resolvedScope, cwd, id, &enabled); err != nil {
		return protocol.JSONObject{}, bad("Failed to " + operation + " plugin: " + err.Error())
	}
	if err = s.options.ClearCaches(ctx); err != nil {
		return protocol.JSONObject{}, err
	}
	name, _ := splitID(id)
	return success("Successfully " + operation + "d plugin: " + name + " (scope: " + resolvedScope + ")" + dependencySuffix(deps)), nil
}
func project(scope, cwd, original string) *string {
	if scope != "project" && scope != "local" {
		return nil
	}
	if cwd == "" {
		cwd = original
	}
	return &cwd
}
func installationMatches(e protocol.JSONObject, scope string, root *string) bool {
	if e.String("scope") != scope {
		return false
	}
	if root == nil {
		return e.Get("projectPath") == nil
	}
	return e.Get("projectPath") != nil && e.String("projectPath") == *root
}
func (s *Service) markOrphan(path string) {
	_ = os.WriteFile(filepath.Join(path, ".orphaned_at"), []byte(strconv.FormatInt(s.options.Now().UnixMilli(), 10)), 0666)
}
func dataName(id string) string {
	var b strings.Builder
	for _, r := range utf16.Encode([]rune(id)) {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			b.WriteByte(byte(r))
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}
func (s *Service) Uninstall(ctx context.Context, plugin, scope, cwd string, keepData bool) (protocol.JSONObject, error) {
	state, err := s.load(ctx, true)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	all := append(state.Enabled, state.Disabled...)
	name, _ := splitID(plugin)
	var found *pluginregistry.Loaded
	for _, p := range all {
		if p.Name == plugin || p.Name == name {
			copy := p
			found = &copy
			break
		}
	}
	settings, err := s.source(ctx, scope, cwd)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	installed, err := s.options.Installed.Session(ctx)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	entries := obj(installed.Get("plugins"))
	id, pluginName := "", ""
	if found != nil {
		id = found.Name
		if strings.Contains(plugin, "@") {
			id = plugin
		}
		for _, key := range properties(obj(settings.Get("enabledPlugins"))) {
			if key == plugin || key == found.Name || strings.HasPrefix(key, found.Name+"@") {
				id = key
				break
			}
		}
		pluginName = found.Name
	} else {
		if len(arr(entries.Get(plugin))) > 0 {
			id = plugin
		} else {
			for _, key := range properties(entries) {
				keyName, _ := splitID(key)
				if keyName == name && len(arr(entries.Get(key))) > 0 {
					id = key
					break
				}
			}
		}
		if id == "" {
			return protocol.JSONObject{}, bad("Plugin \"" + plugin + "\" not found in installed plugins")
		}
		pluginName, _ = splitID(id)
	}
	root := project(scope, cwd, s.options.WorkDir)
	var installation *protocol.JSONObject
	records := arr(entries.Get(id))
	for _, v := range records {
		e := obj(v)
		if installationMatches(e, scope, root) {
			installation = &e
			break
		}
	}
	if installation == nil {
		actual := "user"
		currentProject := cwd
		if currentProject == "" {
			currentProject = s.options.WorkDir
		}
		for _, kind := range []string{"local", "project", "user", "managed"} {
			for _, v := range records {
				e := obj(v)
				if e.String("scope") == kind && ((kind == "local" || kind == "project") && e.String("projectPath") == currentProject || kind == "user" || kind == "managed") {
					actual = kind
					goto picked
				}
			}
		}
	picked:
		if actual != scope && len(records) > 0 {
			if actual == "project" {
				return protocol.JSONObject{}, bad("Plugin \"" + plugin + "\" is enabled at project scope (.claude/settings.json, shared with your team). To disable just for you: claude plugin disable " + plugin + " --scope local")
			}
			return protocol.JSONObject{}, bad("Plugin \"" + plugin + "\" is installed in " + actual + " scope, not " + scope + ". Use --scope " + actual + " to uninstall.")
		}
		return protocol.JSONObject{}, bad("Plugin \"" + plugin + "\" is not installed in " + scope + " scope. Use --scope to specify the correct scope.")
	}
	if err = s.options.WriteEnabled(ctx, scope, cwd, id, nil); err != nil {
		return protocol.JSONObject{}, bad("Failed to update settings: " + err.Error())
	}
	if err = s.options.ClearCaches(ctx); err != nil {
		return protocol.JSONObject{}, err
	}
	if err = s.options.Installed.Remove(ctx, id, scope, root); err != nil {
		return protocol.JSONObject{}, err
	}
	s.options.Installed.Clear()
	updated, err := s.options.Installed.Session(ctx)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if len(arr(obj(updated.Get("plugins")).Get(id))) == 0 {
		if path := installation.String("installPath"); path != "" {
			s.markOrphan(path)
		}
		_ = s.options.DeleteOptions(ctx, id)
		if !keepData {
			_ = os.RemoveAll(filepath.Join(s.options.PluginsDir, "data", dataName(id)))
		}
	}
	return success("Successfully uninstalled plugin: " + pluginName + " (scope: " + scope + ")" + dependencySuffix(reverseDependents(id, all))), nil
}
func (s *Service) Update(ctx context.Context, plugin, scope, cwd string) (protocol.JSONObject, error) {
	name, market := splitID(plugin)
	id := plugin
	if market != "" {
		id = name + "@" + market
	}
	info, err := s.options.LookupPlugin(ctx, plugin)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if info == nil {
		return protocol.JSONObject{}, bad("Plugin \"" + name + "\" not found")
	}
	disk, err := s.options.Installed.Disk()
	if err != nil {
		return protocol.JSONObject{}, err
	}
	entries := arr(obj(disk.Get("plugins")).Get(id))
	if len(entries) == 0 {
		return protocol.JSONObject{}, bad("Plugin \"" + name + "\" is not installed")
	}
	root := project(scope, cwd, s.options.WorkDir)
	var installation *protocol.JSONObject
	for _, v := range entries {
		e := obj(v)
		if installationMatches(e, scope, root) {
			installation = &e
			break
		}
	}
	scopeDesc := scope
	if root != nil {
		scopeDesc += " (" + *root + ")"
	}
	if installation == nil {
		return protocol.JSONObject{}, bad("Plugin \"" + name + "\" is not installed at scope " + scopeDesc)
	}
	entry := info.Entry
	source := entry.Get("source")
	sourcePath, newVersion, sha := "", "", ""
	remote := len(source) > 0 && source[0] != '"'
	if remote {
		cached, err := s.options.Materializer.Cache(ctx, source, rec(field("name", entry.String("name"))))
		if err != nil {
			return protocol.JSONObject{}, err
		}
		sourcePath = cached.Path
		sha = cached.GitCommitSHA
		newVersion = pluginregistry.CalculateVersion(source, cached.Manifest, sourcePath, entry.String("version"), sha)
	} else {
		location := info.InstallLocation
		stat, err := os.Stat(location)
		if err != nil {
			if os.IsNotExist(err) {
				return protocol.JSONObject{}, bad("Marketplace directory not found at " + location)
			}
			return protocol.JSONObject{}, err
		}
		if !stat.IsDir() {
			location = filepath.Dir(location)
		}
		sourcePath = filepath.Join(location, stringValue(source))
		if _, err = os.Stat(sourcePath); err != nil {
			if os.IsNotExist(err) {
				return protocol.JSONObject{}, bad("Plugin source not found at " + sourcePath)
			}
			return protocol.JSONObject{}, err
		}
		p, _, loadErr := s.creator.Create(ctx, sourcePath, id, true, entry.String("name"), false)
		manifest := p.Manifest
		if loadErr != nil {
			manifest = rec()
		}
		newVersion = pluginregistry.CalculateVersion(source, manifest, sourcePath, entry.String("version"), "")
	}
	versioned := pluginregistry.VersionedPathIn(s.options.PluginsDir, id, newVersion)
	if remote {
		defer func() {
			if sourcePath != versioned {
				_ = os.RemoveAll(sourcePath)
			}
		}()
	}
	oldVersion := installation.String("version")
	if oldVersion == newVersion || installation.String("installPath") == versioned || installation.String("installPath") == versioned+".zip" {
		return success(name + " is already at the latest version (" + newVersion + ")."), nil
	}
	versioned, err = s.options.Materializer.CopyToVersioned(ctx, sourcePath, id, newVersion, entry, "")
	if err != nil {
		return protocol.JSONObject{}, err
	}
	var commit *string
	if sha != "" {
		commit = &sha
	}
	if err = s.options.Installed.UpdateDisk(ctx, id, scope, root, versioned, newVersion, commit); err != nil {
		return protocol.JSONObject{}, err
	}
	oldPath := installation.String("installPath")
	if oldPath != "" && oldPath != versioned {
		disk, err = s.options.Installed.Disk()
		if err != nil {
			return protocol.JSONObject{}, err
		}
		referenced := false
		for _, key := range properties(obj(disk.Get("plugins"))) {
			for _, v := range arr(obj(disk.Get("plugins")).Get(key)) {
				if obj(v).String("installPath") == oldPath {
					referenced = true
				}
			}
		}
		if !referenced {
			s.markOrphan(oldPath)
		}
	}
	if oldVersion == "" {
		oldVersion = "unknown"
	}
	return success(fmt.Sprintf("Plugin \"%s\" updated from %s to %s for scope %s. Restart to apply changes.", name, oldVersion, newVersion, scopeDesc)), nil
}
