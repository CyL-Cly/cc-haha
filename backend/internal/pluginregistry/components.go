// Package pluginregistry loads actual plugin artifacts and source precedence
// natively. Component discovery is shared by cached/inline/install consumers.
package pluginregistry

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/pluginschema"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

type FileSystem interface {
	Stat(string) (fs.FileInfo, error)
	ReadFile(string) ([]byte, error)
	Realpath(string) (string, error)
}
type OSFileSystem struct{}

func (OSFileSystem) Stat(path string) (fs.FileInfo, error) { return os.Stat(path) }
func (OSFileSystem) ReadFile(path string) ([]byte, error)  { return os.ReadFile(path) }
func (OSFileSystem) Realpath(path string) (string, error)  { return filepath.EvalSymlinks(path) }

type workingDirectoryFS struct {
	FileSystem
	cwd string
}

func (f workingDirectoryFS) Stat(path string) (fs.FileInfo, error) {
	return f.FileSystem.Stat(resolveRelative(f.cwd, path))
}
func (f workingDirectoryFS) ReadFile(path string) ([]byte, error) {
	return f.FileSystem.ReadFile(resolveRelative(f.cwd, path))
}
func (f workingDirectoryFS) Realpath(path string) (string, error) {
	return f.FileSystem.Realpath(resolveRelative(f.cwd, path))
}

type Loaded struct {
	Name              string               `json:"name"`
	Manifest          protocol.JSONObject  `json:"manifest"`
	Path              string               `json:"path"`
	Source            string               `json:"source"`
	Repository        string               `json:"repository"`
	Enabled           bool                 `json:"enabled"`
	IsBuiltin         bool                 `json:"isBuiltin,omitempty"`
	SHA               string               `json:"sha,omitempty"`
	CommandsPath      string               `json:"commandsPath,omitempty"`
	CommandsPaths     []string             `json:"commandsPaths,omitempty"`
	CommandsMetadata  *protocol.JSONObject `json:"commandsMetadata,omitempty"`
	AgentsPath        string               `json:"agentsPath,omitempty"`
	AgentsPaths       []string             `json:"agentsPaths,omitempty"`
	SkillsPath        string               `json:"skillsPath,omitempty"`
	SkillsPaths       []string             `json:"skillsPaths,omitempty"`
	OutputStylesPath  string               `json:"outputStylesPath,omitempty"`
	OutputStylesPaths []string             `json:"outputStylesPaths,omitempty"`
	WorkflowsPath     string               `json:"workflowsPath,omitempty"`
	WorkflowsPaths    []string             `json:"workflowsPaths,omitempty"`
	HooksConfig       *protocol.JSONObject `json:"hooksConfig,omitempty"`
	MCPServers        *protocol.JSONObject `json:"mcpServers,omitempty"`
	LSPServers        *protocol.JSONObject `json:"lspServers,omitempty"`
	Settings          *protocol.JSONObject `json:"settings,omitempty"`
}
type Result struct {
	cacheID  uint64
	Enabled  []Loaded              `json:"enabled"`
	Disabled []Loaded              `json:"disabled"`
	Errors   []protocol.JSONObject `json:"errors"`
}
type Creator struct {
	Files   FileSystem
	Schemas pluginschema.Schemas
}

func NewCreator(files FileSystem) (*Creator, error) {
	schemas, err := pluginschema.Declarations()
	if err != nil {
		return nil, err
	}
	if files == nil {
		files = OSFileSystem{}
	}
	return &Creator{files, schemas}, nil
}
func object(raw json.RawMessage) protocol.JSONObject {
	var o protocol.JSONObject
	_ = o.UnmarshalJSON(raw)
	return o
}
func raw(value any) json.RawMessage { bytes, _ := json.Marshal(value); return bytes }
func array(value json.RawMessage) []json.RawMessage {
	if len(value) > 0 && value[0] == '[' {
		var a []json.RawMessage
		_ = json.Unmarshal(value, &a)
		return a
	}
	return []json.RawMessage{value}
}
func truthy(value json.RawMessage) bool {
	if len(value) == 0 {
		return false
	}
	switch string(value) {
	case "null", "false", "0", "-0", `""`:
		return false
	}
	return true
}
func errorRecord(kind, source string, fields ...protocol.JSONField) protocol.JSONObject {
	return protocol.NewJSONObject(append([]protocol.JSONField{{Name: "type", Value: raw(kind)}, {Name: "source", Value: raw(source)}}, fields...)...)
}
func field(name string, value any) protocol.JSONField {
	return protocol.JSONField{Name: name, Value: raw(value)}
}
func (c *Creator) exists(path string) bool { _, err := c.Files.Stat(path); return err == nil }
func (c *Creator) readJSON(path string) (json.RawMessage, error) {
	bytes, err := c.Files.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := string(protocol.DecodeJavaScriptUTF8(bytes))
	if !json.Valid([]byte(text)) {
		message, _ := provider.PinnedJavaScriptJSONSyntaxMessage(text)
		var value string
		_ = json.Unmarshal(message, &value)
		return nil, fmt.Errorf("%s", value)
	}
	return json.RawMessage(text), nil
}
func issuesText(issues []pluginschema.Issue) string {
	parts := []string{}
	for _, issue := range issues {
		keys := []string{}
		for _, key := range issue.Path {
			keys = append(keys, fmt.Sprint(key))
		}
		prefix := ""
		if len(keys) > 0 {
			prefix = strings.Join(keys, ".") + ": "
		}
		parts = append(parts, prefix+issue.Message)
	}
	return strings.Join(parts, ", ")
}
func (c *Creator) manifest(path, name, source string) (protocol.JSONObject, error) {
	if !c.exists(path) {
		return protocol.NewJSONObject(field("name", name), field("description", "Plugin from "+source)), nil
	}
	content, err := c.readJSON(path)
	if err != nil {
		return protocol.JSONObject{}, fmt.Errorf("Plugin %s has a corrupt manifest file at %s.\n\nJSON parse error: %s", name, path, err)
	}
	value, issues := pluginschema.Validate(c.Schemas.Manifest, content)
	if len(issues) > 0 {
		return protocol.JSONObject{}, fmt.Errorf("Plugin %s has an invalid manifest file at %s.\n\nValidation errors: %s", name, path, issuesText(issues))
	}
	return object(value), nil
}
func (c *Creator) paths(root, name, source, component string, paths json.RawMessage, errors *[]protocol.JSONObject) []string {
	result := []string{}
	for _, item := range array(paths) {
		var relative string
		if json.Unmarshal(item, &relative) != nil {
			continue
		}
		path := filepath.Join(root, relative)
		if c.exists(path) {
			result = append(result, path)
		} else {
			*errors = append(*errors, errorRecord("path-not-found", source, field("plugin", name), field("path", path), field("component", component)))
		}
	}
	return result
}
func (c *Creator) commandPaths(root, name, source string, value json.RawMessage, errors *[]protocol.JSONObject) ([]string, *protocol.JSONObject) {
	commands := object(value)
	if commands.IsObject() {
		fields := commands.OwnProperties()
		if len(fields) == 0 {
			return nil, nil
		}
		first := object(fields[0].Value)
		if first.IsObject() && (first.Get("source") != nil || first.Get("content") != nil) {
			metadata := protocol.NewJSONObject()
			paths := []string{}
			for _, property := range fields {
				entry := object(property.Value)
				if !entry.IsObject() {
					continue
				}
				if relative := entry.String("source"); relative != "" {
					path := filepath.Join(root, relative)
					if c.exists(path) {
						paths = append(paths, path)
						_ = metadata.SetPropertyJSON(property.NameJSON, property.Value)
					} else {
						*errors = append(*errors, errorRecord("path-not-found", source, field("plugin", name), field("path", path), field("component", "commands")))
					}
				} else if entry.String("content") != "" {
					_ = metadata.SetPropertyJSON(property.NameJSON, property.Value)
				}
			}
			if len(metadata.OwnProperties()) > 0 {
				return paths, &metadata
			}
			return paths, nil
		}
	}
	return c.paths(root, name, source, "commands", value, errors), nil
}
func mergeHooks(base, additional *protocol.JSONObject) *protocol.JSONObject {
	if base == nil {
		return additional
	}
	merged := base.Clone()
	for _, property := range additional.OwnProperties() {
		var name string
		_ = json.Unmarshal(property.NameJSON, &name)
		old := merged.Get(name)
		if !truthy(old) {
			_ = merged.SetPropertyJSON(property.NameJSON, property.Value)
		} else {
			values := append(array(old), array(property.Value)...)
			_ = merged.SetPropertyJSON(property.NameJSON, raw(values))
		}
	}
	return &merged
}
func (c *Creator) hookFile(path, name string) (*protocol.JSONObject, error) {
	if !c.exists(path) {
		return nil, fmt.Errorf("Hooks file not found at %s for plugin %s. If the manifest declares hooks, the file must exist.", path, name)
	}
	input, err := c.readJSON(path)
	if err != nil {
		return nil, err
	}
	wrapper := object(input)
	content := wrapper.Get("hooks")
	parsed, issues := pluginschema.Validate(c.Schemas.Hooks, content)
	if description := wrapper.Get("description"); len(description) > 0 && description[0] != '"' {
		issues = append([]pluginschema.Issue{{Path: []any{"description"}, Message: "Invalid input: expected string, received " + jsonKind(description)}}, issues...)
	}
	if len(issues) > 0 {
		return nil, fmt.Errorf("%s", issuesJSON(issues, "hooks"))
	}
	result := object(parsed)
	return &result, nil
}
func jsonKind(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "undefined"
	}
	switch raw[0] {
	case '"':
		return "string"
	case '{':
		return "object"
	case '[':
		return "array"
	case 'n':
		return "null"
	case 't', 'f':
		return "boolean"
	}
	return "number"
}

// Hook errors carry original schema detail; their exact serialization is
// verified separately from manifest's compact errors.
func issuesJSON(issues []pluginschema.Issue, prefix string) string {
	for i := range issues {
		if len(issues[i].Path) == 0 || issues[i].Path[0] != "description" {
			issues[i].Path = append([]any{prefix}, issues[i].Path...)
		}
	}
	return pluginschema.ErrorJSON(issues, nil)
}

func filteredSettings(input json.RawMessage) *protocol.JSONObject {
	o := object(input)
	if !o.IsObject() {
		return nil
	}
	agent := o.Get("agent")
	if len(agent) == 0 || agent[0] != '"' {
		return nil
	}
	result := protocol.NewJSONObject(protocol.JSONField{Name: "agent", Value: agent})
	return &result
}
func (c *Creator) Create(ctx context.Context, path, source string, enabled bool, fallback string, strict bool) (Loaded, []protocol.JSONObject, error) {
	if err := ctx.Err(); err != nil {
		return Loaded{}, nil, err
	}
	manifest, err := c.manifest(filepath.Join(path, ".claude-plugin", "plugin.json"), fallback, source)
	if err != nil {
		return Loaded{}, nil, err
	}
	p := Loaded{Name: manifest.String("name"), Manifest: manifest, Path: path, Source: source, Repository: source, Enabled: enabled}
	errors := []protocol.JSONObject{}
	for _, entry := range []struct {
		field, dir string
		dest       *string
	}{{"commands", "commands", &p.CommandsPath}, {"agents", "agents", &p.AgentsPath}, {"skills", "skills", &p.SkillsPath}, {"outputStyles", "output-styles", &p.OutputStylesPath}} {
		if !truthy(manifest.Get(entry.field)) {
			dir := filepath.Join(path, entry.dir)
			if c.exists(dir) {
				*entry.dest = dir
			}
		}
	}
	if value := manifest.Get("commands"); truthy(value) {
		p.CommandsPaths, p.CommandsMetadata = c.commandPaths(path, p.Name, source, value, &errors)
	}
	for _, entry := range []struct {
		field, component string
		dest             *[]string
	}{{"agents", "agents", &p.AgentsPaths}, {"skills", "skills", &p.SkillsPaths}, {"workflows", "workflows", &p.WorkflowsPaths}, {"outputStyles", "output-styles", &p.OutputStylesPaths}} {
		if value := manifest.Get(entry.field); truthy(value) {
			*entry.dest = c.paths(path, p.Name, source, entry.component, value, &errors)
		}
	}
	if dir := filepath.Join(path, "workflows"); c.exists(dir) {
		p.WorkflowsPath = dir
	}
	loadedHooks := map[string]bool{}
	var hooks *protocol.JSONObject
	load := func(hookPath, spec string) {
		if !c.exists(hookPath) {
			errors = append(errors, errorRecord("path-not-found", source, field("plugin", p.Name), field("path", hookPath), field("component", "hooks")))
			return
		}
		normalized, err := c.Files.Realpath(hookPath)
		if err != nil {
			normalized = hookPath
		}
		if loadedHooks[normalized] {
			if strict {
				reason := "Duplicate hooks file detected: " + spec + " resolves to already-loaded file " + normalized + ". The standard hooks/hooks.json is loaded automatically, so manifest.hooks should only reference additional hook files."
				errors = append(errors, errorRecord("hook-load-failed", source, field("plugin", p.Name), field("hookPath", hookPath), field("reason", reason)))
			}
			return
		}
		result, err := c.hookFile(hookPath, p.Name)
		if err != nil {
			errors = append(errors, errorRecord("hook-load-failed", source, field("plugin", p.Name), field("hookPath", hookPath), field("reason", err.Error())))
			return
		}
		hooks = mergeHooks(hooks, result)
		loadedHooks[normalized] = true
	}
	standard := filepath.Join(path, "hooks", "hooks.json")
	if c.exists(standard) {
		load(standard, "hooks/hooks.json")
	}
	if value := manifest.Get("hooks"); truthy(value) {
		for _, spec := range array(value) {
			if jsonKind(spec) == "string" {
				var relative string
				_ = json.Unmarshal(spec, &relative)
				load(filepath.Join(path, relative), relative)
			} else if jsonKind(spec) == "object" {
				additional := object(spec)
				hooks = mergeHooks(hooks, &additional)
			}
		}
	}
	p.HooksConfig = hooks
	if bytes, err := c.readJSON(filepath.Join(path, "settings.json")); err == nil {
		p.Settings = filteredSettings(bytes)
	}
	if p.Settings == nil {
		p.Settings = filteredSettings(manifest.Get("settings"))
	}
	return p, errors, nil
}
