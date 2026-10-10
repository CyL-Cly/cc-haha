package builtintools

// The migrated external-release entries retain getAllBaseTools order. Feature
// and permission filtering happens on the real bindings in Catalog; request
// assembly retains its existing later sorting/deduplication producer.
func nativeBaseToolNames(images bool) []string {
	names := []string{"Agent", "TaskOutput", "Bash", "Glob", "Grep", "ExitPlanMode", "Read", "Edit", "Write", "NotebookEdit", "WebFetch", "TodoWrite", "WebSearch", "TaskStop", "AskUserQuestion", "Skill", "EnterPlanMode", "TaskCreate", "TaskGet", "TaskUpdate", "TaskList", "EnterWorktree", "ExitWorktree", "SendMessage", "ListSessions", "ReadSession", "CreateSession", "SendSessionMessage", "WaitSessions", "TeamCreate", "TeamPlan", "TeamDelete", "Workflow"}
	if images {
		names = append(names, "ImageGen", "ImageEdit")
	}
	return append(names, "ListMcpResourcesTool", "ReadMcpResourceTool", "ToolSearch")
}
