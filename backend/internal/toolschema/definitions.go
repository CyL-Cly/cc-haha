package toolschema

import "encoding/json"

func textSchema(description string, optional bool) Schema {
	return Schema{Kind: "string", Description: quote(description), Optional: optional}
}
func boolSchema(description string) Schema {
	return Schema{Kind: "boolean", Description: quote(description), Optional: true, Semantic: true}
}
func numberSchema(description string) Schema {
	return Schema{Kind: "number", Description: quote(description), Optional: true, Semantic: true}
}
func builtinDefinition(name string) (Definition, error) {
	definition := Definition{Kind: "builtin", Name: name, Strict: name != "Glob"}
	fields := []Field{}
	switch name {
	case "ImageGen", "ImageEdit":
		return NativeImageDefinition(name)
	case "Agent":
		definition.Strict = false
		definition.Aliases = []string{"Task"}
		model := textSchema("Optional model override for this agent. Takes precedence over the agent definition's model frontmatter. If omitted, uses the agent definition's model, or inherits from the parent.", true)
		model.Kind = "enum"
		model.Values = []string{"fable", "sonnet", "opus", "haiku"}
		mode := textSchema("Permission mode for spawned teammate (e.g., \"plan\" to require plan approval).", true)
		mode.Kind = "enum"
		mode.Values = []string{"acceptEdits", "bypassPermissions", "default", "dontAsk", "plan", "auto"}
		isolation := textSchema("Isolation mode. \"worktree\" creates a temporary git worktree so the agent works on an isolated copy of the repo.", true)
		isolation.Kind = "enum"
		isolation.Values = []string{"worktree"}
		background := boolSchema("Set to true to run this agent in the background. You will be notified when it completes.")
		background.Semantic = false
		fields = []Field{{"description", textSchema("A short (3-5 word) description of the task", false)}, {"prompt", textSchema("The task for the agent to perform", false)}, {"subagent_type", textSchema("The type of specialized agent to use for this task", true)}, {"model", model}, {"run_in_background", background}, {"name", textSchema("Name for the spawned agent. Makes it addressable via SendMessage({to: name}) while running.", true)}, {"team_name", textSchema("Existing team created with TeamCreate. Omit for ordinary subagents; an empty value is treated as omitted for function-calling compatibility.", true)}, {"mode", mode}, {"isolation", isolation}}
	case "Bash":
		fields = []Field{{"command", textSchema("The command to execute", false)}, {"timeout", numberSchema("Optional timeout in milliseconds (max 600000)")}, {"description", textSchema(bashDescription, true)}, {"run_in_background", boolSchema("Set to true to run this command in the background. Use Read to read the output later.")}, {"dangerouslyDisableSandbox", boolSchema("Set this to true to dangerously override sandbox mode and run commands without sandboxing.")}}
	case "Read":
		zero := 0.0
		offset := numberSchema("The line number to start reading from. Only provide if the file is too large to read at once")
		offset.Integer = true
		offset.Minimum = &zero
		limit := numberSchema("The number of lines to read. Only provide if the file is too large to read at once.")
		limit.Integer = true
		limit.ExclusiveMinimum = &zero
		fields = []Field{{"file_path", textSchema("The absolute path to the file to read", false)}, {"offset", offset}, {"limit", limit}, {"pages", textSchema("Page range for PDF files (e.g., \"1-5\", \"3\", \"10-20\"). Only applicable to PDF files. Maximum 20 pages per request.", true)}}
	case "Edit":
		replace := boolSchema("Replace all occurrences of old_string (default false)")
		replace.Default = json.RawMessage("false")
		fields = []Field{{"file_path", textSchema("The absolute path to the file to modify", false)}, {"old_string", textSchema("The text to replace", false)}, {"new_string", textSchema("The text to replace it with (must be different from old_string)", false)}, {"replace_all", replace}}
	case "Write":
		fields = []Field{{"file_path", textSchema("The absolute path to the file to write (must be absolute, not relative)", false)}, {"content", textSchema("The content to write to the file", false)}}
	case "NotebookEdit":
		kind := textSchema("The type of the cell (code or markdown). If not specified, it defaults to the current cell type. If using edit_mode=insert, this is required.", true)
		kind.Kind, kind.Values = "enum", []string{"code", "markdown"}
		mode := textSchema("The type of edit to make (replace, insert, delete). Defaults to replace.", true)
		mode.Kind, mode.Values = "enum", []string{"replace", "insert", "delete"}
		fields = []Field{{"notebook_path", textSchema("The absolute path to the Jupyter notebook file to edit (must be absolute, not relative)", false)}, {"cell_id", textSchema("The ID of the cell to edit. When inserting a new cell, the new cell will be inserted after the cell with this ID, or at the beginning if not specified.", true)}, {"new_source", textSchema("The new source for the cell", false)}, {"cell_type", kind}, {"edit_mode", mode}}
	case "TodoWrite":
		one := 1
		content := Schema{Kind: "string", Native: true, MinimumLength: &one}
		item := Schema{Kind: "object", UnknownKeys: "strip", Fields: []Field{{"content", content}, {"status", Schema{Kind: "enum", Values: []string{"pending", "in_progress", "completed"}}}, {"activeForm", content}}}
		fields = []Field{{"todos", Schema{Kind: "array", Native: true, Items: &item, Description: quote("The updated todo list")}}}
	case "EnterWorktree", "ExitWorktree":
		fields = worktreeFields(name)
	case "WebFetch", "WebSearch":
		fields = webFields(name)
	case "AskUserQuestion":
		fields = questionFields()
	case "TaskOutput", "TaskStop":
		fields = backgroundTaskFields(name)
		if name == "TaskOutput" {
			definition.Aliases = []string{"AgentOutputTool", "BashOutputTool"}
		} else {
			definition.Aliases = []string{"KillShell"}
		}
	case "TaskCreate", "TaskGet", "TaskUpdate", "TaskList":
		fields = taskFields(name)
	case "EnterPlanMode", "ExitPlanMode":
		definition.Strict = false
		fields = planFields(name)
	case "Skill":
		definition.Strict = false
		fields = []Field{{"skill", textSchema("The skill name. E.g., \"commit\", \"review-pr\", or \"pdf\"", false)}, {"args", textSchema("Optional arguments for the skill", true)}}
	case "ListMcpResourcesTool", "ReadMcpResourceTool":
		fields = mcpResourceFields(name)
	case "ToolSearch":
		fields = toolSearchFields()
	case "Workflow":
		fields = workflowFields()
	case "ListSessions", "ReadSession", "CreateSession", "SendSessionMessage", "WaitSessions":
		return SessionCollaborationDefinition(name)
	case "SendMessage":
		definition.Strict = false
		definition.ShouldDefer = true
		fields = sendMessageFields()
	case "TeamCreate", "TeamDelete", "TeamPlan":
		fields = teamFields(name)
		definition.Description = quote(map[string]string{"TeamCreate": "Create a new team for coordinating multiple agents", "TeamDelete": "Clean up team and task directories when the swarm is complete", "TeamPlan": "Read, replace or submit a team draft for human review. Never starts members or approves a plan."}[name])
		if name == "TeamPlan" {
			definition.Strict = false
		}
	case "Glob":
		fields = []Field{{"pattern", textSchema("The glob pattern to match files against", false)}, {"path", textSchema("The directory to search in. If not specified, the current working directory will be used. IMPORTANT: Omit this field to use the default directory. DO NOT enter \"undefined\" or \"null\" - simply omit it for the default behavior. Must be a valid directory path if provided.", true)}}
	case "Grep":
		output := textSchema("Output mode: \"content\" shows matching lines (supports -A/-B/-C context, -n line numbers, head_limit), \"files_with_matches\" shows file paths (supports head_limit), \"count\" shows match counts (supports head_limit). Defaults to \"files_with_matches\".", true)
		output.Kind = "enum"
		output.Values = []string{"content", "files_with_matches", "count"}
		fields = []Field{
			{"pattern", textSchema("The regular expression pattern to search for in file contents", false)},
			{"path", textSchema("File or directory to search in (rg PATH). Defaults to current working directory.", true)},
			{"glob", textSchema("Glob pattern to filter files (e.g. \"*.js\", \"*.{ts,tsx}\") - maps to rg --glob", true)},
			{"output_mode", output},
			{"-B", numberSchema("Number of lines to show before each match (rg -B). Requires output_mode: \"content\", ignored otherwise.")},
			{"-A", numberSchema("Number of lines to show after each match (rg -A). Requires output_mode: \"content\", ignored otherwise.")},
			{"-C", numberSchema("Alias for context.")},
			{"context", numberSchema("Number of lines to show before and after each match (rg -C). Requires output_mode: \"content\", ignored otherwise.")},
			{"-n", boolSchema("Show line numbers in output (rg -n). Requires output_mode: \"content\", ignored otherwise. Defaults to true.")},
			{"-i", boolSchema("Case insensitive search (rg -i)")},
			{"type", textSchema("File type to search (rg --type). Common types: js, py, rust, go, java, etc. More efficient than include for standard file types.", true)},
			{"head_limit", numberSchema("Limit output to first N lines/entries, equivalent to \"| head -N\". Works across all output modes: content (limits output lines), files_with_matches (limits file paths), count (limits count entries). Defaults to 250 when unspecified. Pass 0 for unlimited (use sparingly — large result sets waste context).")},
			{"offset", numberSchema("Skip first N lines/entries before applying head_limit, equivalent to \"| tail -n +N | head -N\". Works across all output modes. Defaults to 0.")},
			{"multiline", boolSchema("Enable multiline mode where . matches newlines and patterns can span lines (rg -U --multiline-dotall). Default: false.")},
		}
	default:
		return definition, unsupported("native tool " + name)
	}
	definition.InputSchema = &Schema{Kind: "object", UnknownKeys: "strict", Fields: fields}
	if name == "Agent" || name == "SendMessage" || name == "Skill" || name == "ListMcpResourcesTool" || name == "ReadMcpResourceTool" || name == "ToolSearch" || name == "TeamPlan" {
		definition.InputSchema.UnknownKeys = "strip"
	}
	if name == "ExitPlanMode" {
		definition.InputSchema.UnknownKeys = "passthrough"
	}
	setNativeSearchMetadata(&definition)
	return definition, nil
}
