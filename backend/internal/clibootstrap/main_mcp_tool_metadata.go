package clibootstrap

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

func nativeMCPToolMetadata(def toolschema.Definition) protocol.JSONObject {
	value := protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: quoteMain(def.Name)})
	if def.SearchHint != nil && def.Name != "ToolSearch" {
		value.Set("searchHint", def.SearchHint)
	}
	if len(def.Aliases) > 0 {
		value.Set("aliases", rawMain(def.Aliases))
	}
	maximum := json.RawMessage(`100000`)
	switch def.Name {
	case "Bash", "PowerShell":
		maximum = json.RawMessage(`30000`)
	case "Grep":
		maximum = json.RawMessage(`20000`)
	case "Read":
		maximum = json.RawMessage(`null`)
	}
	value.Set("maxResultSizeChars", maximum)
	if def.Name == "Bash" || def.Name == "PowerShell" || def.Name == "Grep" || def.Name == "Read" || def.Name == "Edit" || def.Name == "Write" || def.Name == "TodoWrite" {
		value.Set("strict", json.RawMessage(`true`))
	}
	if def.ShouldDefer {
		value.Set("shouldDefer", json.RawMessage(`true`))
	}

	return value
}
func mcpSchemaField(kind, description string) protocol.JSONObject {
	schema := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain(kind)})
	if description != "" {
		schema.Set("description", quoteMain(description))
	}
	return schema
}
func mcpObjectSchema(properties protocol.JSONObject, required []string, root bool) protocol.JSONObject {
	schema := protocol.NewJSONObject()
	if root {
		schema.Set("$schema", quoteMain("https://json-schema.org/draft/2020-12/schema"))
	}
	schema.Set("type", quoteMain("object"))
	schema.Set("properties", rawMain(properties))
	schema.Set("required", rawMain(required))
	schema.Set("additionalProperties", json.RawMessage(`false`))
	return schema
}

// Native output declarations follow the original Zod output recipes. Input
// schema/prompt remain produced by the shared actual builtin definitions.
func nativeMCPOutputSchema(name string) json.RawMessage {
	properties := protocol.NewJSONObject()
	add := func(key, kind, description string) { properties.Set(key, rawMain(mcpSchemaField(kind, description))) }
	switch name {
	case "PowerShell":
		add("stdout", "string", "The standard output of the command")
		add("stderr", "string", "The standard error output of the command")
		add("interrupted", "boolean", "Whether the command was interrupted")
		add("returnCodeInterpretation", "string", "Semantic interpretation for non-error exit codes with special meaning")
		add("isImage", "boolean", "Flag to indicate if stdout contains image data")
		add("persistedOutputPath", "string", "Path to persisted full output when too large for inline")
		add("persistedOutputSize", "number", "Total output size in bytes when persisted")
		add("backgroundTaskId", "string", "ID of the background task if command is running in background")
		add("backgroundedByUser", "boolean", "True if the user manually backgrounded the command with Ctrl+B")
		add("assistantAutoBackgrounded", "boolean", "True if the command was auto-backgrounded by the assistant-mode blocking budget")
		return rawMain(mcpObjectSchema(properties, []string{"stdout", "stderr", "interrupted"}, true))
	case "Bash":
		add("stdout", "string", "The standard output of the command")
		add("stderr", "string", "The standard error output of the command")
		add("rawOutputPath", "string", "Path to raw output file for large MCP tool outputs")
		add("interrupted", "boolean", "Whether the command was interrupted")
		add("isImage", "boolean", "Flag to indicate if stdout contains image data")
		add("backgroundTaskId", "string", "ID of the background task if command is running in background")
		add("backgroundedByUser", "boolean", "True if the user manually backgrounded the command with Ctrl+B")
		add("assistantAutoBackgrounded", "boolean", "True if assistant-mode auto-backgrounded a long-running blocking command")
		add("dangerouslyDisableSandbox", "boolean", "Flag to indicate if sandbox mode was overridden")
		add("returnCodeInterpretation", "string", "Semantic interpretation for non-error exit codes with special meaning")
		add("noOutputExpected", "boolean", "Whether the command is expected to produce no output on success")
		blocks := mcpSchemaField("array", "Structured content blocks")
		blocks.Set("items", json.RawMessage(`{}`))
		properties.Set("structuredContent", rawMain(blocks))
		add("persistedOutputPath", "string", "Path to the persisted full output in tool-results dir (set when output is too large for inline)")
		add("persistedOutputSize", "number", "Total size of the output in bytes (set when output is too large for inline)")
		return rawMain(mcpObjectSchema(properties, []string{"stdout", "stderr", "interrupted"}, true))
	case "Edit":
		add("filePath", "string", "The file path that was edited")
		add("oldString", "string", "The original string that was replaced")
		add("newString", "string", "The new string that replaced it")
		add("originalFile", "string", "The original file contents before editing")
		hunk := protocol.NewJSONObject()
		for _, key := range []string{"oldStart", "oldLines", "newStart", "newLines"} {
			hunk.Set(key, rawMain(mcpSchemaField("number", "")))
		}
		lines := mcpSchemaField("array", "")
		lines.Set("items", rawMain(mcpSchemaField("string", "")))
		hunk.Set("lines", rawMain(lines))
		patch := mcpSchemaField("array", "Diff patch showing the changes")
		patch.Set("items", rawMain(mcpObjectSchema(hunk, []string{"oldStart", "oldLines", "newStart", "newLines", "lines"}, false)))
		properties.Set("structuredPatch", rawMain(patch))
		add("userModified", "boolean", "Whether the user modified the proposed changes")
		add("replaceAll", "boolean", "Whether all occurrences were replaced")
		git := protocol.NewJSONObject(protocol.JSONField{Name: "filename", Value: rawMain(mcpSchemaField("string", ""))})
		status := mcpSchemaField("string", "")
		status.Set("enum", rawMain([]string{"modified", "added"}))
		git.Set("status", rawMain(status))
		for _, key := range []string{"additions", "deletions", "changes"} {
			git.Set(key, rawMain(mcpSchemaField("number", "")))
		}
		git.Set("patch", rawMain(mcpSchemaField("string", "")))
		repository := protocol.NewJSONObject(protocol.JSONField{Name: "description", Value: quoteMain("GitHub owner/repo when available")}, protocol.JSONField{Name: "anyOf", Value: rawMain([]protocol.JSONObject{mcpSchemaField("string", ""), mcpSchemaField("null", "")})})
		git.Set("repository", rawMain(repository))
		properties.Set("gitDiff", rawMain(mcpObjectSchema(git, []string{"filename", "status", "additions", "deletions", "changes", "patch"}, false)))
		return rawMain(mcpObjectSchema(properties, []string{"filePath", "oldString", "newString", "originalFile", "structuredPatch", "userModified", "replaceAll"}, true))
	default:
		return nativeMCPOrdinaryOutputSchema(name)
	}
}
