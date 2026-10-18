package agentcatalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// PluginDefinitionJSON keeps a plugin's substituted prompt immutable and raw
// through its actual model consumer. The public metadata is a separate copy;
// changing a returned rawSystemPrompt field cannot change the captured prompt.
func PluginDefinitionJSON(path, pluginName, pluginSource string, namespace []string, data []byte, memoryEnabled bool, substitute func(json.RawMessage) (json.RawMessage, error)) (*Definition, error) {
	var prompt json.RawMessage
	definition, err := PluginDefinition(path, pluginName, pluginSource, namespace, data, memoryEnabled, func(body string) (string, error) {
		var err error
		prompt = quoteAgentPrompt(body)
		if substitute != nil {
			prompt, err = substitute(prompt)
		}
		if err != nil {
			return "", err
		}
		var display string
		err = json.Unmarshal(prompt, &display)
		return display, err
	})
	if err == nil {
		definition.promptJSON = append(json.RawMessage(nil), prompt...)
		definition.Fields.Set("rawSystemPrompt", prompt)
	}
	return definition, err
}

// PluginDefinition applies the shipped plugin trust boundary: per-agent
// permissionMode, hooks and mcpServers are ignored by this Source producer.
func PluginDefinition(path, pluginName, pluginSource string, namespace []string, data []byte, memoryEnabled bool, substitute func(string) (string, error)) (*Definition, error) {
	f, body, err := Frontmatter(string(protocol.DecodeJavaScriptUTF8(data)))
	if err != nil {
		return nil, err
	}
	baseName := strings.TrimSuffix(filepath.Base(path), ".md")
	if truthy(f.Get("name")) {
		value, err := protocol.JavaScriptStringJSON(f.Get("name"))
		if err != nil {
			return nil, err
		}
		baseName, err = exactString(value)
		if err != nil {
			return nil, err
		}
	}
	name := strings.Join(append(append([]string{pluginName}, namespace...), baseName), ":")
	description := ""
	for _, key := range []string{"description", "when-to-use"} {
		raw := f.Get(key)
		if raw == nil {
			continue
		}
		if raw[0] == '"' || raw[0] == 't' || raw[0] == 'f' || raw[0] >= '0' && raw[0] <= '9' || raw[0] == '-' {
			value, err := protocol.JavaScriptStringJSON(raw)
			if err != nil {
				return nil, err
			}
			description, err = exactString(value)
			if err != nil {
				return nil, err
			}
			description = trimJS(description)
			if description != "" {
				break
			}
		}
	}
	if description == "" {
		description = "Agent from " + pluginName + " plugin"
	}
	model := trimJS(f.String("model"))
	if strings.EqualFold(model, "inherit") {
		model = "inherit"
	}
	memory := f.String("memory")
	if memory != "user" && memory != "project" && memory != "local" {
		memory = ""
	}
	body = trimJS(body)
	if substitute != nil {
		body, err = substitute(body)
		if err != nil {
			return nil, err
		}
	}
	hash := sha256.Sum256(protocol.DecodeJavaScriptUTF8(data))
	parsedFields := f.Clone()
	for _, property := range []string{"permissionMode", "hooks", "mcpServers", "initialPrompt"} {
		parsedFields.Delete(property)
	}
	d := &Definition{Name: name, Description: description, Model: model, Memory: memory, Source: "plugin", FilePath: path, ContentHash: hex.EncodeToString(hash[:]), Body: body, Effort: effortValue(f.Get("effort")), Frontmatter: parsedFields}
	if err := enrichMarkdownDefinition(d, memoryEnabled); err != nil {
		return nil, err
	}
	fields := protocol.NewJSONObject()
	for _, key := range []string{"agentType", "whenToUse", "tools", "disallowedTools", "skills", "rawSystemPrompt", "source", "sourceFilePath", "sourceContentHash", "color", "model"} {
		value := d.Fields.Get(key)
		if key == "color" {
			value = f.Get(key)
		}
		if value != nil {
			fields.Set(key, value)
		}
	}
	filename := encodeField(baseName)
	if truthy(f.Get("name")) {
		filename = f.Get("name")
	}
	fields.Set("filename", filename)
	fields.Set("plugin", encodeField(pluginSource))
	for _, key := range []string{"background", "memory", "isolation", "effort", "maxTurns"} {
		if value := d.Fields.Get(key); value != nil {
			fields.Set(key, value)
		}
	}
	d.Fields = fields
	d.Frontmatter = f
	return d, nil
}
