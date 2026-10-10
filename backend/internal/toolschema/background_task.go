package toolschema

import "encoding/json"

func backgroundTaskFields(name string) []Field {
	if name == "TaskStop" {
		return []Field{{"task_id", textSchema("The ID of the background task to stop", true)}, {"shell_id", textSchema("Deprecated: use task_id instead", true)}}
	}
	block := boolSchema("Whether to wait for completion")
	block.Default = json.RawMessage("true")
	block.Optional = false
	minimum, maximum := 0.0, 600000.0
	timeout := Schema{Native: true, Kind: "number", Optional: false, Description: quote("Max wait time in ms"), Default: json.RawMessage("30000"), Minimum: &minimum, Maximum: &maximum}
	return []Field{{"task_id", textSchema("The task ID to get output from", false)}, {"block", block}, {"timeout", timeout}}
}

const taskOutputPrompt = "DEPRECATED: Prefer using the Read tool on the task's output file path instead. Background tasks return their output file path in the tool result, and you receive a <task-notification> with the same path when the task completes — Read that file directly.\n\n- Retrieves output from a running or completed task (background shell, agent, or remote session)\n- Takes a task_id parameter identifying the task\n- Returns the task output along with status information\n- Use block=true (default) to wait for task completion\n- Use block=false for non-blocking check of current status\n- Task IDs can be found using the /tasks command\n- Works with all task types: background shells, async agents, and remote sessions"

const taskStopPrompt = "\n- Stops a running background task by its ID\n- Takes a task_id parameter identifying the task to stop\n- Returns a success or failure status\n- Use this tool only when the user explicitly asks to cancel/stop a task, or when\n  the task is clearly runaway, harmful, duplicative, or no longer useful.\n- Do not stop a background agent merely because you have already read enough of\n  its output. Prefer waiting for the automatic completion notification or\n  leaving it running while you summarize available progress.\n"
