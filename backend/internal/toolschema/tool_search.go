package toolschema

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolsearch"
)

func toolSearchFields() []Field {
	results := numberSchema("Maximum number of results to return (default: 5)")
	results.Semantic = false
	results.Optional = false
	results.Default = json.RawMessage("5")
	return []Field{{"query", textSchema("Query to find deferred tools. Use \"select:<tool_name>\" for direct selection, or keywords to search.", false)}, {"max_results", results}}
}

func setNativeSearchMetadata(d *Definition) {
	if isTeamTool(d.Name) {
		d.SearchHint = quote(map[string]string{"TeamCreate": "create a multi-agent swarm team", "TeamDelete": "disband a swarm team and clean up", "TeamPlan": "prepare and submit a complete team for human model allocation before execution"}[d.Name])
		d.AlwaysLoad, d.ShouldDefer = d.Name == "TeamPlan", d.Name != "TeamPlan"
		return
	}
	if d.Name == "Workflow" {
		d.SearchHint = quote("orchestrate many subagents from a script")
		return
	}
	hints := map[string]string{
		"Agent": "delegate work to a subagent", "TaskOutput": "read output/logs from a background task", "Bash": "execute shell commands", "Glob": "find files by name pattern or wildcard", "Grep": "search file contents with regex (ripgrep)", "ExitPlanMode": "present plan for approval and start coding (plan mode only)", "Read": "read files, images, PDFs, notebooks", "Edit": "modify file contents in place", "Write": "create or overwrite files", "NotebookEdit": "edit Jupyter notebook cells (.ipynb)", "WebFetch": "fetch and extract content from a URL", "TodoWrite": "manage the session task checklist", "WebSearch": "search the web for current information", "TaskStop": "kill a running background task", "AskUserQuestion": "prompt the user with a multiple-choice question", "Skill": "invoke a slash-command skill", "EnterPlanMode": "switch to plan mode to design an approach before coding", "TaskCreate": "create a task in the task list", "TaskGet": "retrieve a task by ID", "TaskUpdate": "update a task", "TaskList": "list all tasks", "ListMcpResourcesTool": "list resources from connected MCP servers", "ReadMcpResourceTool": "read a specific MCP resource by URI", "EnterWorktree": "create an isolated git worktree and switch into it", "ExitWorktree": "exit a worktree session and return to the original directory",
	}
	d.SearchHint = quote(hints[d.Name])
	switch d.Name {
	case "TaskOutput", "ExitPlanMode", "NotebookEdit", "WebFetch", "TodoWrite", "WebSearch", "TaskStop", "AskUserQuestion", "EnterPlanMode", "ListMcpResourcesTool", "ReadMcpResourceTool", "EnterWorktree", "ExitWorktree":
		d.ShouldDefer = true
	}
	switch d.Name {
	case "TaskCreate", "TaskGet", "TaskUpdate", "TaskList":
		d.AlwaysLoad = true
	}
}

// OriginalPrompt follows the tool's prompt recipe without constructing or
// caching an API schema. ToolSearch has its own name-keyed description memo.
func (s *Session) OriginalPrompt(definition Definition, host Host) (json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	text, e := s.prompt(definition, host)
	if e == nil {
		s.promptCalls[definition.Name]++
	}
	return text, e
}
func OriginalInputSchema(definition Definition) (json.RawMessage, error) {
	if len(definition.InputJSONSchema) > 0 && string(definition.InputJSONSchema) != "null" {
		return append(json.RawMessage(nil), definition.InputJSONSchema...), nil
	}
	if definition.InputSchema == nil {
		return nil, unsupported("actual original tool input schema")
	}
	return compileSchema(*definition.InputSchema, true)
}
func toolSearchPrompt(delta bool) json.RawMessage { return quote(toolsearch.Prompt(delta)) }
