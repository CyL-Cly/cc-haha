package clibootstrap

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

// These are the original output Zod recipes exposed by entrypoints/mcp.ts.
// Union outputs (Read, Agent, TaskOutput, Skill) have no object root and are
// deliberately omitted by that entrypoint. Inputs remain shared tool recipes.
func nativeMCPOrdinaryOutputSchema(name string) json.RawMessage {
	properties := protocol.NewJSONObject()
	add := func(key, kind, description string) { properties.Set(key, rawMain(mcpSchemaField(kind, description))) }
	enum := func(key, description string, values ...string) {
		v := mcpSchemaField("string", description)
		v.Set("enum", rawMain(values))
		properties.Set(key, rawMain(v))
	}
	array := func(key, description string, item json.RawMessage) {
		v := mcpSchemaField("array", description)
		v.Set("items", item)
		properties.Set(key, rawMain(v))
	}
	nullable := func(key, description string) {
		v := protocol.NewJSONObject(protocol.JSONField{Name: "anyOf", Value: rawMain([]protocol.JSONObject{mcpSchemaField("string", ""), mcpSchemaField("null", "")})})
		if description != "" {
			v.Set("description", quoteMain(description))
		}
		properties.Set(key, rawMain(v))
	}
	required := []string{}
	switch name {
	case "Glob":
		add("durationMs", "number", "Time taken to execute the search in milliseconds")
		add("numFiles", "number", "Total number of files found")
		array("filenames", "Array of file paths that match the pattern", rawMain(mcpSchemaField("string", "")))
		add("truncated", "boolean", "Whether results were truncated (limited to 100 files)")
		required = []string{"durationMs", "numFiles", "filenames", "truncated"}
	case "Grep":
		enum("mode", "", "content", "files_with_matches", "count")
		add("numFiles", "number", "")
		array("filenames", "", rawMain(mcpSchemaField("string", "")))
		add("content", "string", "")
		for _, key := range []string{"numLines", "numMatches", "appliedLimit", "appliedOffset"} {
			add(key, "number", "")
		}
		required = []string{"numFiles", "filenames"}
	case "ExitPlanMode":
		nullable("plan", "The plan that was presented to the user")
		add("isAgent", "boolean", "")
		add("filePath", "string", "The file path where the plan was saved")
		add("hasTaskTool", "boolean", "Whether the Agent tool is available in the current context")
		add("planWasEdited", "boolean", "True when the user edited the plan (CCR web UI or Ctrl+G); determines whether the plan is echoed back in tool_result")
		add("awaitingLeaderApproval", "boolean", "When true, the teammate has sent a plan approval request to the team leader")
		add("requestId", "string", "Unique identifier for the plan approval request")
		required = []string{"plan", "isAgent"}
	case "Write":
		enum("type", "Whether a new file was created or an existing file was updated", "create", "update")
		add("filePath", "string", "The path to the file that was written")
		add("content", "string", "The content that was written to the file")
		var edit protocol.JSONObject
		_ = edit.UnmarshalJSON(nativeMCPOutputSchema("Edit"))
		editProperties, _ := edit.Object("properties")
		properties.Set("structuredPatch", editProperties.Get("structuredPatch"))
		nullable("originalFile", "The original file content before the write (null for new files)")
		properties.Set("gitDiff", editProperties.Get("gitDiff"))
		required = []string{"type", "filePath", "content", "structuredPatch", "originalFile"}
	case "NotebookEdit":
		add("new_source", "string", "The new source code that was written to the cell")
		add("cell_id", "string", "The ID of the cell that was edited")
		enum("cell_type", "The type of the cell", "code", "markdown")
		add("language", "string", "The programming language of the notebook")
		add("edit_mode", "string", "The edit mode that was used")
		add("error", "string", "Error message if the operation failed")
		add("notebook_path", "string", "The path to the notebook file")
		add("original_file", "string", "The original notebook content before modification")
		add("updated_file", "string", "The updated notebook content after modification")
		required = []string{"new_source", "cell_type", "language", "edit_mode", "notebook_path", "original_file", "updated_file"}
	case "WebFetch":
		add("bytes", "number", "Size of the fetched content in bytes")
		add("code", "number", "HTTP response code")
		add("codeText", "string", "HTTP response code text")
		add("result", "string", "Processed result from applying the prompt to the content")
		add("durationMs", "number", "Time taken to fetch and process the content")
		add("url", "string", "The URL that was fetched")
		required = []string{"bytes", "code", "codeText", "result", "durationMs", "url"}
	case "TodoWrite":
		item := mcpOriginalArrayItem(name, "todos")
		array("oldTodos", "The todo list before the update", item)
		array("newTodos", "The todo list after the update", item)
		add("verificationNudgeNeeded", "boolean", "")
		required = []string{"oldTodos", "newTodos"}
	case "WebSearch":
		add("query", "string", "The search query that was executed")
		hit := protocol.NewJSONObject(protocol.JSONField{Name: "title", Value: rawMain(mcpSchemaField("string", "The title of the search result"))}, protocol.JSONField{Name: "url", Value: rawMain(mcpSchemaField("string", "The URL of the search result"))})
		content := mcpSchemaField("array", "Array of search hits")
		content.Set("items", rawMain(mcpObjectSchema(hit, []string{"title", "url"}, false)))
		result := protocol.NewJSONObject(protocol.JSONField{Name: "tool_use_id", Value: rawMain(mcpSchemaField("string", "ID of the tool use"))}, protocol.JSONField{Name: "content", Value: rawMain(content)})
		item := protocol.NewJSONObject(protocol.JSONField{Name: "anyOf", Value: rawMain([]protocol.JSONObject{mcpObjectSchema(result, []string{"tool_use_id", "content"}, false), mcpSchemaField("string", "")})})
		array("results", "Search results and/or text commentary from the model", rawMain(item))
		add("durationSeconds", "number", "Time taken to complete the search operation")
		required = []string{"query", "results", "durationSeconds"}
	case "TaskStop":
		add("message", "string", "Status message about the operation")
		add("task_id", "string", "The ID of the task that was stopped")
		add("task_type", "string", "The type of the task that was stopped")
		add("command", "string", "The command or description of the stopped task")
		required = []string{"message", "task_id", "task_type"}
	case "AskUserQuestion":
		array("questions", "The questions that were asked", mcpOriginalArrayItem(name, "questions"))
		def, _ := toolschema.NativeBuiltinDefinition(name)
		input, _ := toolschema.OriginalInputSchema(def)
		var original protocol.JSONObject
		_ = original.UnmarshalJSON(input)
		inputProperties, _ := original.Object("properties")
		answers, _ := inputProperties.Object("answers")
		answers.Set("description", quoteMain("The answers provided by the user (question text -> answer string; multi-select answers are comma-separated)"))
		properties.Set("answers", rawMain(answers))
		properties.Set("annotations", inputProperties.Get("annotations"))
		selection := mcpSchemaField("string", "")
		selection.Set("const", quoteMain("automatic"))
		properties.Set("selectionSource", rawMain(selection))
		required = []string{"questions", "answers"}
	case "EnterPlanMode":
		add("message", "string", "Confirmation that plan mode was entered")
		required = []string{"message"}
	case "EnterWorktree":
		add("worktreePath", "string", "")
		add("worktreeBranch", "string", "")
		add("message", "string", "")
		required = []string{"worktreePath", "message"}
	case "ExitWorktree":
		enum("action", "", "keep", "remove")
		for _, key := range []string{"originalCwd", "worktreePath", "worktreeBranch", "tmuxSessionName"} {
			add(key, "string", "")
		}
		add("discardedFiles", "number", "")
		add("discardedCommits", "number", "")
		add("message", "string", "")
		required = []string{"action", "originalCwd", "worktreePath", "message"}
	case "Workflow":
		enum("status", "", "async_launched", "remote_launched")
		add("taskId", "string", "")
		enum("taskType", "", "local_workflow", "remote_agent")
		for _, key := range []string{"workflowName", "runId", "summary", "transcriptDir", "scriptPath", "sessionUrl", "warning", "error"} {
			add(key, "string", "")
		}
		required = []string{"status", "taskId"}
	case "ToolSearch":
		array("matches", "", rawMain(mcpSchemaField("string", "")))
		add("query", "string", "")
		add("total_deferred_tools", "number", "")
		array("pending_mcp_servers", "", rawMain(mcpSchemaField("string", "")))
		required = []string{"matches", "query", "total_deferred_tools"}
	default:
		return nil
	}
	return rawMain(mcpObjectSchema(properties, required, true))
}
func mcpOriginalArrayItem(name, key string) json.RawMessage {
	definition, _ := toolschema.NativeBuiltinDefinition(name)
	input, _ := toolschema.OriginalInputSchema(definition)
	var original protocol.JSONObject
	_ = original.UnmarshalJSON(input)
	properties, _ := original.Object("properties")
	array, _ := properties.Object(key)
	return array.Get("items")
}
