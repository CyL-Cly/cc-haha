package requestcontext

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	weburl "github.com/nlnwa/whatwg-url/url"
)

const TaskBudgetBeta = "task-budgets-2026-03-13"

func configureTaskBudget(input Context, output *protocol.JSONObject, betas []string) []string {
	if input.Options.TaskBudget == nil || output.Get("task_budget") != nil || envTrue(input.Host.Env["CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS"]) {
		return betas
	}
	env := input.Host.Env
	if envTrue(env["CLAUDE_CODE_USE_BEDROCK"]) || envTrue(env["CLAUDE_CODE_USE_VERTEX"]) || !envTrue(env["CLAUDE_CODE_USE_FOUNDRY"]) && envTrue(env["CLAUDE_CODE_USE_AZURE_OPENAI"]) {
		return betas
	}
	firstParty := env["ANTHROPIC_BASE_URL"] == ""
	if !firstParty {
		value, err := weburl.Parse(env["ANTHROPIC_BASE_URL"])
		firstParty = err == nil && (value.Host() == "api.anthropic.com" || env["USER_TYPE"] == "ant" && value.Host() == "api-staging.anthropic.com")
	}
	if !envTrue(env["CLAUDE_CODE_USE_FOUNDRY"]) && !firstParty && !(input.Host.NativeClaudeSubscriber && input.Host.NativeClaudeProof.Current()) {
		return betas
	}
	budget := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("tokens")}, protocol.JSONField{Name: "total", Value: rawNumber(input.Options.TaskBudget.Total)})
	if input.Options.TaskBudget.Remaining != nil {
		budget.Set("remaining", rawNumber(*input.Options.TaskBudget.Remaining))
	}
	output.Set("task_budget", rawObject(budget))
	return appendUnique(betas, TaskBudgetBeta)
}
