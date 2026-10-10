package skillcommands

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/markdownfrontmatter"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Plugin command and Skill discovery have separate per-plugin realpath scopes.
// They consume actual Loaded artifacts, rather than a catalog projected from
// desktop Skill metadata or captured model output.
func (s *Service) PluginCommands(ctx context.Context, enabled []pluginregistry.Loaded) []Definition {
	out := []Definition{}
	for _, plugin := range enabled {
		seen := map[string]bool{}
		if plugin.CommandsPath != "" {
			out = append(out, s.pluginDirectoryCommands(ctx, plugin, plugin.CommandsPath, seen)...)
		}
		for _, path := range plugin.CommandsPaths {
			info, err := s.options.Files.Stat(path)
			if err != nil {
				s.fail(err)
				continue
			}
			if info.IsDir() {
				out = append(out, s.pluginDirectoryCommands(ctx, plugin, path, seen)...)
			} else if info.Mode().IsRegular() && strings.HasSuffix(path, ".md") && !s.pluginDuplicate(path, seen) {
				file, err := s.readFile(path, filepath.Dir(path), "plugin")
				if err != nil {
					s.fail(err)
					continue
				}
				name := plugin.Name + ":" + strings.TrimSuffix(filepath.Base(path), ".md")
				nameJSON := pluginCommandNameJSON(plugin, listingQuoted(strings.TrimSuffix(filepath.Base(path), ".md")))
				overrides := protocol.NewJSONObject()
				if plugin.CommandsMetadata != nil {
					for _, property := range plugin.CommandsMetadata.OwnProperties() {
						var metadata protocol.JSONObject
						_ = metadata.UnmarshalJSON(property.Value)
						if source := metadata.String("source"); source != "" && filepath.Join(plugin.Path, source) == path {
							var key string
							_ = json.Unmarshal(property.NameJSON, &key)
							name, overrides = plugin.Name+":"+key, pluginOverrides(metadata)
							nameJSON = pluginCommandNameJSON(plugin, property.NameJSON)
							break
						}
					}
				}
				if command, err := s.pluginDefinition(ctx, plugin, file, name, nameJSON, false, false, overrides); err == nil {
					out = append(out, command)
				} else {
					s.fail(err)
				}
			}
		}
		if plugin.CommandsMetadata != nil {
			for _, property := range plugin.CommandsMetadata.OwnProperties() {
				var metadata protocol.JSONObject
				_ = metadata.UnmarshalJSON(property.Value)
				if content := metadata.String("content"); content != "" && !pluginTruthy(metadata.Get("source")) {
					var key string
					_ = json.Unmarshal(property.NameJSON, &key)
					name := plugin.Name + ":" + key
					parsed, err := markdownfrontmatter.Parse(content)
					if err != nil {
						s.fail(err)
						continue
					}
					file := markdownFile{path: "<inline:" + name + ">", base: plugin.Path, source: "plugin", p: parsed}
					if command, err := s.pluginDefinition(ctx, plugin, file, name, pluginCommandNameJSON(plugin, property.NameJSON), false, false, pluginOverrides(metadata)); err == nil {
						out = append(out, command)
					} else {
						s.fail(err)
					}
				}
			}
		}
	}
	return out
}

func (s *Service) pluginDuplicate(path string, seen map[string]bool) bool {
	identity, err := s.options.Files.Realpath(path)
	if err != nil {
		identity = path
	}
	if seen[identity] {
		return true
	}
	seen[identity] = true
	return false
}

func (s *Service) pluginDirectoryCommands(ctx context.Context, plugin pluginregistry.Loaded, root string, seen map[string]bool) []Definition {
	files := []markdownFile{}
	for _, path := range s.markdownPaths(root, true) {
		if s.pluginDuplicate(path, seen) {
			continue
		}
		file, err := s.readFile(path, root, "plugin")
		if err != nil {
			s.fail(err)
			continue
		}
		files = append(files, file)
	}
	out := []Definition{}
	for _, file := range transform(files) {
		isSkill := strings.EqualFold(filepath.Base(file.path), "SKILL.md")
		name := strings.TrimSuffix(filepath.Base(file.path), ".md")
		dir := filepath.Dir(file.path)
		if isSkill {
			name, dir = filepath.Base(dir), filepath.Dir(dir)
		}
		namespace := ""
		if strings.HasPrefix(dir, file.base) {
			namespace = strings.TrimPrefix(strings.TrimPrefix(dir, file.base), string(filepath.Separator))
		}
		if namespace != "" {
			name = strings.ReplaceAll(namespace, string(filepath.Separator), ":") + ":" + name
		}
		if command, err := s.pluginDefinition(ctx, plugin, file, plugin.Name+":"+name, pluginCommandNameJSON(plugin, listingQuoted(name)), isSkill, false, protocol.NewJSONObject()); err == nil {
			out = append(out, command)
		} else {
			s.fail(err)
		}
	}
	return out
}

func (s *Service) PluginSkills(ctx context.Context, enabled []pluginregistry.Loaded) []Definition {
	out := []Definition{}
	for _, plugin := range enabled {
		seen := map[string]bool{}
		paths := []string{}
		if plugin.SkillsPath != "" {
			paths = append(paths, plugin.SkillsPath)
		}
		paths = append(paths, plugin.SkillsPaths...)
		for _, root := range paths {
			path := filepath.Join(root, "SKILL.md")
			data, err := s.options.Files.ReadFile(path)
			if err == nil {
				out = append(out, s.pluginSkillFile(ctx, plugin, path, data, seen)...)
				continue
			}
			if !errors.Is(err, fs.ErrNotExist) {
				s.fail(err)
				continue
			}
			entries, err := s.options.Files.ReadDir(root)
			if err != nil {
				s.fail(err)
				continue
			}
			for _, child := range entries {
				if !child.IsDir() && child.Type()&fs.ModeSymlink == 0 {
					continue
				}
				path := filepath.Join(root, child.Name(), "SKILL.md")
				if data, err := s.options.Files.ReadFile(path); err == nil {
					out = append(out, s.pluginSkillFile(ctx, plugin, path, data, seen)...)
				} else {
					s.fail(err)
				}
			}
		}
	}
	return out
}

func (s *Service) pluginSkillFile(ctx context.Context, plugin pluginregistry.Loaded, path string, data []byte, seen map[string]bool) []Definition {
	if s.pluginDuplicate(path, seen) {
		return nil
	}
	p, err := markdownfrontmatter.Parse(string(protocol.DecodeJavaScriptUTF8(data)))
	if err != nil {
		s.fail(err)
		return nil
	}
	file := markdownFile{path: path, base: filepath.Dir(path), source: "plugin", p: p}
	name := plugin.Name + ":" + filepath.Base(filepath.Dir(path))
	command, err := s.pluginDefinition(ctx, plugin, file, name, pluginCommandNameJSON(plugin, listingQuoted(filepath.Base(filepath.Dir(path)))), true, true, protocol.NewJSONObject())
	if err != nil {
		s.fail(err)
		return nil
	}
	return []Definition{command}
}

func pluginTruthy(raw json.RawMessage) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	switch value := value.(type) {
	case nil:
		return false
	case bool:
		return value
	case float64:
		return value != 0
	case string:
		return value != ""
	default:
		return true
	}
}

func pluginOverrides(metadata protocol.JSONObject) protocol.JSONObject {
	out := protocol.NewJSONObject()
	for from, to := range map[string]string{"description": "description", "argumentHint": "argument-hint", "model": "model"} {
		if value := metadata.Get(from); pluginTruthy(value) {
			out.Set(to, value)
		}
	}
	if tools := metadata.Get("allowedTools"); pluginTruthy(tools) {
		set(out, "allowed-tools", strings.Join(rawStrings(tools), ","))
	}
	return out
}

func (s *Service) pluginDefinition(ctx context.Context, plugin pluginregistry.Loaded, file markdownFile, name string, nameJSON json.RawMessage, isSkill, skillMode bool, overrides protocol.JSONObject) (Definition, error) {
	p := file.p
	get := func(key string) json.RawMessage {
		if value := overrides.Get(key); value != nil {
			return value
		}
		return p.Frontmatter.Get(key)
	}
	stringValue := func(raw json.RawMessage) string {
		converted, _ := protocol.JavaScriptStringJSON(raw)
		var text string
		_ = json.Unmarshal(converted, &text)
		return text
	}
	m := protocol.NewJSONObject()
	set(m, "type", "prompt")
	m.Set("name", nameJSON)
	label := "Plugin command"
	if isSkill {
		label = "Plugin skill"
	}
	descriptionJSON, specified := pluginDescriptionJSON(p, label)
	if raw := overrides.Get("description"); raw != nil {
		descriptionJSON, specified = pluginTrimStringJSON(raw), true
		units, _ := protocol.DecodeJavaScriptJSONString(descriptionJSON)
		if len(units) == 0 {
			descriptionJSON, specified = pluginDescriptionJSON(markdownfrontmatter.Parsed{Content: p.Content}, label)
		}
	}
	m.Set("description", descriptionJSON)
	set(m, "hasUserSpecifiedDescription", specified)
	toolValues := rawStrings(get("allowed-tools"))
	normalize := func(path string) string {
		if runtime.GOOS == "windows" {
			return strings.ReplaceAll(path, "\\", "/")
		}
		return path
	}
	for i, text := range toolValues {
		text = strings.ReplaceAll(text, "${CLAUDE_PLUGIN_ROOT}", normalize(plugin.Path))
		if strings.Contains(text, "${CLAUDE_PLUGIN_DATA}") && plugin.Source != "" {
			if s.options.PluginDataDirectory == nil {
				return Definition{}, errors.New("plugin allowed-tools require actual data-directory producer")
			}
			path, err := s.options.PluginDataDirectory(ctx, plugin.Source)
			if err != nil {
				return Definition{}, err
			}
			text = strings.ReplaceAll(text, "${CLAUDE_PLUGIN_DATA}", normalize(path))
		}
		toolValues[i] = text
	}
	toolsJSON, _ := json.Marshal(toolValues)
	tools := parseTools(toolsJSON)
	set(m, "allowedTools", tools)
	for _, key := range []string{"argument-hint", "when_to_use", "version"} {
		dest := map[string]string{"argument-hint": "argumentHint", "when_to_use": "whenToUse", "version": "version"}[key]
		m.Set(dest, get(key))
	}
	arguments := parseArgumentNames(get("arguments"))
	if len(arguments) > 0 {
		set(m, "argNames", arguments)
	}
	if raw := get("model"); pluginTruthy(raw) && stringValue(raw) != "inherit" {
		var input string
		if json.Unmarshal(raw, &input) != nil {
			return Definition{}, errors.New("modelInput.trim is not a function")
		}
		if s.options.ResolveModel == nil {
			return Definition{}, errors.New("plugin model requires actual model resolver")
		}
		model, err := s.options.ResolveModel(ctx, input)
		if err != nil {
			return Definition{}, err
		}
		set(m, "model", model)
	}
	if effort := parseEffort(p.Get("effort")); effort != nil {
		m.Set("effort", effort)
	}
	disabled, user := boolean(p.Get("disable-model-invocation")), p.Get("user-invocable") == nil || boolean(p.Get("user-invocable"))
	set(m, "disableModelInvocation", disabled)
	set(m, "userInvocable", user)
	set(m, "isHidden", !user)
	set(m, "contentLength", len(utf16.Encode([]rune(p.Content))))
	set(m, "source", "plugin")
	loadedFrom, progress := "", "running"
	if isSkill || skillMode {
		loadedFrom, progress = "plugin", "loading"
		set(m, "loadedFrom", loadedFrom)
	}
	set(m, "progressMessage", progress)
	manifest, err := plugin.Manifest.MarshalJSON()
	if err != nil {
		return Definition{}, err
	}
	info := protocol.NewJSONObject(protocol.JSONField{Name: "pluginManifest", Value: manifest})
	set(info, "repository", plugin.Source)
	rawInfo, _ := info.MarshalJSON()
	m.Set("pluginInfo", rawInfo)
	displayJSON := nameJSON
	if raw := get("name"); pluginTruthy(raw) {
		if isSkill {
			display, err := protocol.DecodeJavaScriptJSONString(raw)
			if err != nil {
				return Definition{}, errors.New("displayName.startsWith is not a function")
			}
			prefix := pluginCommandNameJSON(plugin, listingQuoted(""))
			prefixUnits, _ := protocol.DecodeJavaScriptJSONString(prefix)
			displayJSON = raw
			if len(display) < len(prefixUnits) || !equalPluginUnits(display[:len(prefixUnits)], prefixUnits) {
				aliases, _ := json.Marshal([]json.RawMessage{raw})
				m.Set("aliases", aliases)
				displayJSON, _ = listingJoin(prefix, raw)
			}
		} else {
			// Source keeps non-string display values for regular commands.
			displayJSON = raw
		}
	}
	m.Set("userFacingName", displayJSON)
	shell := strings.ToLower(trimJS(optionalString(p.Get("shell"))))
	if shell != "bash" && shell != "powershell" {
		shell = ""
	}
	userConfig := protocol.JSONObject{}
	if raw := plugin.Manifest.Get("userConfig"); raw != nil {
		_ = userConfig.UnmarshalJSON(raw)
	}
	baseDir := ""
	if skillMode {
		baseDir = filepath.Dir(file.path)
	}
	return Definition{metadata: m, body: p.Content, name: name, baseDir: baseDir, loadedFrom: loadedFrom, pluginPath: plugin.Path, pluginSource: plugin.Source, shell: shell, arguments: arguments, allowedTools: tools, pluginUserConfig: userConfig, pluginSkillMode: skillMode}, nil
}

// Names are constructed at the actual plugin producers, before the UTF-8
// display/path projection loses unpaired JavaScript code units.
func pluginCommandNameJSON(plugin pluginregistry.Loaded, suffix json.RawMessage) json.RawMessage {
	prefix := plugin.Manifest.Get("name")
	if prefix == nil {
		prefix = listingQuoted(plugin.Name)
	}
	joined, _ := listingJoin(prefix, listingQuoted(":"), suffix)
	return joined
}
func equalPluginUnits(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func pluginTrimStringJSON(raw json.RawMessage) json.RawMessage {
	quoted, _ := protocol.JavaScriptStringJSON(raw)
	units, _ := protocol.DecodeJavaScriptJSONString(quoted)
	for len(units) > 0 && protocol.IsJavaScriptWhitespace(rune(units[0])) {
		units = units[1:]
	}
	for len(units) > 0 && protocol.IsJavaScriptWhitespace(rune(units[len(units)-1])) {
		units = units[:len(units)-1]
	}
	return protocol.QuoteJavaScriptUTF16(units)
}
func pluginDescriptionJSON(p markdownfrontmatter.Parsed, label string) (json.RawMessage, bool) {
	if v := p.Get("description"); v != nil {
		switch v.Kind {
		case "string":
			raw := pluginTrimStringJSON(p.Frontmatter.Get("description"))
			units, _ := protocol.DecodeJavaScriptJSONString(raw)
			if len(units) > 0 {
				return raw, true
			}
		case "number", "boolean":
			return listingQuoted(v.String()), true
		}
	}
	for _, line := range strings.Split(p.Content, "\n") {
		text := trimJS(line)
		if text == "" {
			continue
		}
		if m := regexp.MustCompile(`^#+\s+(.+)$`).FindStringSubmatch(text); m != nil {
			text = m[1]
		}
		units := utf16.Encode([]rune(text))
		if len(units) > 100 {
			units = append(units[:97:97], '.', '.', '.')
		}
		return protocol.QuoteJavaScriptUTF16(units), false
	}
	return listingQuoted(label), false
}
