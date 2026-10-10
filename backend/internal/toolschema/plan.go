package toolschema

import (
	_ "embed"
	"strings"
)

//go:embed assets/plan-enter.md
var planEnterPrompt string

//go:embed assets/plan-enter-ant.md
var planEnterAntPrompt string

//go:embed assets/plan-enter-workflow.md
var planEnterWorkflow string

//go:embed assets/plan-exit.md
var planExitPrompt string

func planFields(name string) []Field {
	if name == "EnterPlanMode" {
		return []Field{}
	}
	tool := textSchema("The tool this prompt applies to", false)
	tool.Kind = "enum"
	tool.Values = []string{"Bash"}
	item := Schema{Kind: "object", Native: true, UnknownKeys: "strip", Fields: []Field{{"tool", tool}, {"prompt", textSchema("Semantic description of the action, e.g. \"run tests\", \"install dependencies\"", false)}}}
	return []Field{{"allowedPrompts", Schema{Kind: "array", Native: true, Items: &item, Optional: true, Description: quote("Prompt-based permissions needed to implement the plan. These describe categories of actions rather than specific commands.")}}}
}
func planPrompt(name string, interview bool, userType string) string {
	if name == "ExitPlanMode" {
		return planExitPrompt
	}
	what := planEnterWorkflow
	if interview {
		what = ""
	}
	prompt := planEnterPrompt
	if userType == "ant" {
		prompt = planEnterAntPrompt
	}
	return strings.ReplaceAll(prompt, "__SOURCE_WHAT_HAPPENS__", what)
}
