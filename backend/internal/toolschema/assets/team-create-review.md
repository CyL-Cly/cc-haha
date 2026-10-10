## Human review before execution

This desktop session requires whole-team review. TeamCreate creates a draft, not running teammates.
Prefer passing a complete plan with members and tasks. Each member has id, name, agentType,
prompt, an optional suggested runtime {providerId, modelId, effortLevel}, reason and difficulty.
Each task has id, subject, description, ownerId and dependencies (task IDs).
Member names may contain letters, numbers, underscores and hyphens only; team-lead is reserved.
Agent calls with name/team_name only register draft members. They do NOT start execution.
Finish the entire plan, then call TeamPlan with operation="submit" and expected_revision.
After submitting, end this planning turn. The user will review presets, task owners and model
allocation in the desktop team panel. Do not claim tasks or execute the team's work before
approval. Do not poll while waiting. Approval starts the exact approved roster automatically;
do not spawn those members again. Additional members require a new incremental review.