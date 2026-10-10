package toolschema

import (
	_ "embed"
	"encoding/json"
)

//go:embed assets/team-create.md
var teamCreatePrompt string

//go:embed assets/team-create-review.md
var teamCreateReviewPrompt string

//go:embed assets/team-delete.md
var teamDeletePrompt string

const teamPlanPrompt = `After TeamCreate, use TeamPlan to submit the complete roster and tasks together. Give each member a stable id, a launchable name using only letters, numbers, underscores or hyphens (team-lead is reserved), available agentType, task prompt, suggested runtime and a short reason. Task ownerId refers to a member id; dependencies refer to task ids. Use get to read the current revision. Replace and submit require expected_revision. Submit may include a complete replacement plan. After submit, end the planning turn and wait for human approval. Do not start work, claim tasks, poll the plan or call Agent to bypass review. Only the user can approve in the team panel.`

func isTeamTool(name string) bool {
	return name == "TeamCreate" || name == "TeamDelete" || name == "TeamPlan"
}
func teamPrompt(name string, review bool) string {
	switch name {
	case "TeamCreate":
		if review {
			return teamCreateReviewPrompt + "\n\n" + teamCreatePrompt
		}
		return teamCreatePrompt
	case "TeamDelete":
		return teamDeletePrompt
	default:
		return teamPlanPrompt
	}
}
func teamString(optional, nonempty bool) Schema {
	s := Schema{Kind: "string", Native: true, Optional: optional}
	if nonempty {
		one := 1
		s.MinimumLength = &one
	}
	return s
}
func proposedTeamPlanSchema() Schema {
	runtime := Schema{Kind: "object", Native: true, UnknownKeys: "strip", Optional: true, Description: quote("Suggested provider and model; the user makes the final choice."), Fields: []Field{{"providerId", teamString(false, true)}, {"modelId", teamString(false, true)}, {"effortLevel", teamString(true, false)}}}
	agent := teamString(true, false)
	agent.Description = quote("An available Agent preset; defaults to general-purpose.")
	member := Schema{Kind: "object", Native: true, UnknownKeys: "strip", Fields: []Field{{"id", teamString(false, true)}, {"name", teamString(false, true)}, {"agentType", agent}, {"prompt", teamString(false, true)}, {"runtime", runtime}, {"reason", teamString(true, false)}, {"difficulty", Schema{Kind: "enum", Values: []string{"low", "medium", "high"}, Optional: true}}}}
	owner := teamString(true, false)
	owner.Description = quote("Stable member id, not its display name.")
	str := teamString(false, false)
	task := Schema{Kind: "object", Native: true, UnknownKeys: "strip", Fields: []Field{{"id", teamString(false, true)}, {"subject", teamString(false, true)}, {"description", teamString(true, false)}, {"ownerId", owner}, {"dependencies", Schema{Kind: "array", Native: true, Items: &str, Default: json.RawMessage("[]")}}}}
	return Schema{Kind: "object", Native: true, UnknownKeys: "strip", Optional: true, Fields: []Field{{"members", Schema{Kind: "array", Native: true, Items: &member}}, {"tasks", Schema{Kind: "array", Native: true, Items: &task}}}}
}
func teamFields(name string) []Field {
	plan := proposedTeamPlanSchema()
	switch name {
	case "TeamDelete":
		return []Field{}
	case "TeamCreate":
		plan.Description = quote("Complete proposed roster and tasks. Saved as a draft for human review; does not start members.")
		return []Field{{"team_name", textSchema("Name for the new team to create.", false)}, {"description", textSchema("Team description/purpose.", true)}, {"plan", plan}, {"agent_type", textSchema("Type/role of the team lead (e.g., \"researcher\", \"test-runner\"). Used for team file and inter-agent coordination.", true)}}
	default:
		zero := 0.0
		revision := Schema{Kind: "number", Integer: true, ExclusiveMinimum: &zero, Optional: true, Description: quote("Required for replace and submit; use the revision returned by TeamCreate, Agent or TeamPlan.")}
		return []Field{{"team_name", teamString(false, true)}, {"operation", Schema{Kind: "enum", Values: []string{"get", "replace", "submit"}}}, {"expected_revision", revision}, {"plan", plan}}
	}
}
