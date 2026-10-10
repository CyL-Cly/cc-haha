package toolschema

import (
	_ "embed"
	"fmt"
	"runtime"
	"strings"
)

//go:embed assets/workflow-prompt.md
var workflowPromptTemplate string

func workflowFields() []Field {
	maximum := 524288
	script := textSchema("Self-contained workflow script. Must begin with `export const meta = { name, description, phases }` (pure literal, no computed values) followed by the script body using agent()/parallel()/pipeline()/phase().", true)
	script.MaximumLength = &maximum
	script.Native = true
	resume := textSchema("Run ID of a prior Workflow invocation to resume from. Completed agent() calls with unchanged (prompt, opts) return their cached results instantly; only edited or new calls re-run.", true)
	resume.Pattern = `^wf_[a-z0-9-]{6,}$`
	resume.Native = true
	return []Field{{"script", script}, {"scriptPath", textSchema("Path to a workflow script file on disk. Every Workflow invocation persists its script under the session directory and returns the path in the tool result. Takes precedence over `script` and `name`.", true)}, {"name", textSchema("Name of a predefined workflow (built-in or from .claude/workflows/).", true)}, {"args", Schema{Kind: "unknown", Native: true, Optional: true, Description: quote("Optional input value exposed to the script as the global `args`, verbatim. Pass arrays/objects as actual JSON values, NOT as a JSON-encoded string.")}}, {"title", textSchema("Ignored — set the title in `meta`.", true)}, {"description", textSchema("Ignored — set the description in `meta`.", true)}, {"resumeFromRunId", resume}}
}
func workflowPrompt(host Host) string {
	n := host.WorkflowConcurrency
	if n < 1 {
		n = min(16, max(2, runtime.NumCPU()-2))
	}
	guideline := host.WorkflowSizeGuideline
	target := 15
	switch guideline {
	case "unrestricted":
		return strings.ReplaceAll(strings.ReplaceAll(workflowPromptTemplate, "{{WORKFLOW_CONCURRENCY}}", fmt.Sprint(n)), "{{SIZE_GUIDELINE}}", "This session has no workflow size guideline: size the workflow to the task.")
	case "small":
		target = 5
	case "large":
		target = 50
	default:
		guideline = "medium"
	}
	advice := fmt.Sprintf("This session has the workflow size guideline: %s — keep workflows under %d agents. This is a guideline, not a hard limit — follow it unless the user's prompt calls for a different scale. The user can raise or remove it with \"Dynamic workflow size\" in /config.", guideline, target)
	return strings.ReplaceAll(strings.ReplaceAll(workflowPromptTemplate, "{{WORKFLOW_CONCURRENCY}}", fmt.Sprint(n)), "{{SIZE_GUIDELINE}}", advice)
}
