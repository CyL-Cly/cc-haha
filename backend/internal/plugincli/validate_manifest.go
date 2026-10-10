package plugincli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/pluginschema"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

func validationJSON(content []byte) (json.RawMessage, error) {
	if message, invalid := provider.PinnedJavaScriptJSONSyntaxMessage(string(content)); invalid {
		var text string
		_ = json.Unmarshal(message, &text)
		return nil, errors.New(text)
	}
	return json.RawMessage(content), nil
}
func appendSchemaIssues(r *ValidationResult, issues []pluginschema.Issue) {
	for _, issue := range issues {
		path := []string{}
		for _, part := range issue.Path {
			path = append(path, fmt.Sprint(part))
		}
		label := strings.Join(path, ".")
		if label == "" {
			label = "root"
		}
		code := issue.Details.String("code")
		if code == "" {
			code = "custom"
		}
		r.addError(label, issue.Message, code)
	}
}
func traversal(r *ValidationResult, p, field, hint string) {
	if !strings.Contains(p, "..") {
		return
	}
	message := "Path contains \"..\" which could be a path traversal attempt: " + p
	if hint != "" {
		message = "Path contains \"..\": " + p + ". " + hint
	}
	r.addError(field, message, "")
}
func marketplaceHint(p string) string {
	stripped := p
	for strings.HasPrefix(stripped, "../") {
		stripped = stripped[3:]
	}
	corrected := "./plugins/my-plugin"
	if stripped != p {
		corrected = "./" + stripped
	}
	return "Plugin source paths are resolved relative to the marketplace root (the directory containing .claude-plugin/), not relative to marketplace.json. Use \"" + corrected + "\" instead of \"" + p + "\"."
}

func validateManifestFile(ctx context.Context, p, kind string) (ValidationResult, error) {
	r := validationResult(p, kind)
	if err := ctx.Err(); err != nil {
		return r, err
	}
	content, err := os.ReadFile(p)
	if err != nil {
		code := validationErrno(err)
		message := "Failed to read file: " + validationIOError(err).Error()
		if code == "ENOENT" {
			message = "File not found: " + p
		} else if code == "EISDIR" {
			message = "Path is not a file: " + p
		}
		r.addError("file", message, code)
		return r, nil
	}
	parsed, err := validationJSON(content)
	if err != nil {
		r.addError("json", "Invalid JSON syntax: "+err.Error(), "")
		return r, nil
	}
	obj := object(parsed)
	schemas, err := pluginschema.Declarations()
	if err != nil {
		return r, err
	}
	schema := schemas.Manifest
	if kind == "plugin" {
		for _, key := range []string{"commands", "agents", "skills"} {
			value := obj.Get(key)
			if !truthy(value) {
				continue
			}
			rows := array(value)
			if !strings.HasPrefix(strings.TrimSpace(string(value)), "[") {
				rows = []json.RawMessage{value}
			}
			for i, item := range rows {
				var p string
				if json.Unmarshal(item, &p) == nil {
					traversal(&r, p, fmt.Sprintf("%s[%d]", key, i), "")
				}
			}
		}
		if obj.IsObject() {
			for _, entry := range obj.OwnProperties() {
				var key string
				_ = json.Unmarshal(entry.NameJSON, &key)
				if key == "category" || key == "source" || key == "tags" || key == "strict" || key == "id" {
					obj.Delete(key)
					r.warn(key, "Field '"+key+"' belongs in the marketplace entry (marketplace.json), not plugin.json. It's harmless here but unused — Claude Code ignores it at load time.")
				}
			}
			parsed, _ = obj.MarshalJSON()
		}
	} else {
		schema = schemas.Marketplace
		plugins := obj.Get("plugins")
		if strings.HasPrefix(strings.TrimSpace(string(plugins)), "[") {
			for i, item := range array(plugins) {
				source := object(item).Get("source")
				var p string
				if json.Unmarshal(source, &p) == nil {
					traversal(&r, p, fmt.Sprintf("plugins[%d].source", i), marketplaceHint(p))
				}
				path := object(source).Get("path")
				if json.Unmarshal(path, &p) == nil {
					traversal(&r, p, fmt.Sprintf("plugins[%d].source.path", i), "")
				}
			}
		}
		var pluginsNode pluginschema.Node
		_ = json.Unmarshal(schema.Shape.Get("plugins"), &pluginsNode)
		pluginsNode.Element.Catchall = &pluginschema.Node{Type: "never"}
		schema.Shape.Set("plugins", raw(pluginsNode))
	}
	schema.Catchall = &pluginschema.Node{Type: "never"}
	data, issues := pluginschema.Validate(schema, parsed)
	appendSchemaIssues(&r, issues)
	if len(issues) != 0 {
		return r, nil
	}
	manifest := object(data)
	if kind == "plugin" {
		name := manifest.String("name")
		if !regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`).MatchString(name) {
			r.warn("name", "Plugin name \""+name+"\" is not kebab-case. Claude Code accepts it, but the Claude.ai marketplace sync requires kebab-case (lowercase letters, digits, and hyphens only, e.g., \"my-plugin\").")
		}
		if !truthy(manifest.Get("version")) {
			r.warn("version", "No version specified. Consider adding a version following semver (e.g., \"1.0.0\")")
		}
		if !truthy(manifest.Get("description")) {
			r.warn("description", "No description provided. Adding a description helps users understand what your plugin does")
		}
		if !truthy(manifest.Get("author")) {
			r.warn("author", "No author information provided. Consider adding author details for plugin attribution")
		}
	} else {
		plugins := array(manifest.Get("plugins"))
		if len(plugins) == 0 {
			r.warn("plugins", "Marketplace has no plugins defined")
		}
		for i, item := range plugins {
			name := object(item).String("name")
			count := 0
			for _, other := range plugins {
				if object(other).String("name") == name {
					count++
				}
			}
			if count > 1 {
				r.addError(fmt.Sprintf("plugins[%d].name", i), "Duplicate plugin name \""+name+"\" found in marketplace", "")
			}
		}
		root := filepath.Dir(p)
		if filepath.Base(root) == ".claude-plugin" {
			root = filepath.Dir(root)
		}
		for i, item := range plugins {
			entry := object(item)
			var source string
			if !truthy(entry.Get("version")) || json.Unmarshal(entry.Get("source"), &source) != nil || !strings.HasPrefix(source, "./") {
				continue
			}
			content, e := os.ReadFile(filepath.Join(root, source, ".claude-plugin", "plugin.json"))
			if e != nil {
				continue
			}
			parsed, e := validationJSON(content)
			if e != nil {
				continue
			}
			var version string
			if json.Unmarshal(object(parsed).Get("version"), &version) != nil || version == "" || version == entry.String("version") {
				continue
			}
			r.warn(fmt.Sprintf("plugins[%d].version", i), "Entry declares version \""+entry.String("version")+"\" but "+source+"/.claude-plugin/plugin.json says \""+version+"\". At install time, plugin.json wins (calculatePluginVersion precedence) — the entry version is silently ignored. Update this entry to \""+version+"\" to match.")
		}
		if !truthy(object(manifest.Get("metadata")).Get("description")) {
			r.warn("metadata.description", "No marketplace description provided. Adding a description helps users understand what this marketplace offers")
		}
	}
	return r, nil
}
