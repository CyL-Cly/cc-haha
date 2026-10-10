package systemprompt

import "strings"

func bullets(header string, items []string) string {
	result := []string{header}
	for _, item := range items {
		result = append(result, " - "+item)
	}
	return strings.Join(result, "\n")
}

func (s *Service) usingTools(names map[string]bool) string {
	task := ""
	if names["TaskCreate"] {
		task = "TaskCreate"
	} else if names["TodoWrite"] {
		task = "TodoWrite"
	}
	taskText := ""
	if task != "" {
		taskText = "Break down and manage your work with the " + task + " tool. These tools are helpful for planning your work and helping the user track your progress. Mark each task as completed as soon as you are done with the task. Do not batch up multiple tasks before marking them as completed."
	}
	if enabled(s.options.REPLMode) {
		if taskText == "" {
			return ""
		}
		return bullets("# Using your tools", []string{taskText})
	}
	result := []string{"# Using your tools", " - Do NOT use the Bash to run commands when a relevant dedicated tool is provided. Using dedicated tools allows the user to better understand and review your work. This is CRITICAL to assisting the user:", "  - To read files use Read instead of cat, head, tail, or sed", "  - To edit files use Edit instead of sed or awk", "  - To create files use Write instead of cat with heredoc or echo redirection"}
	if !enabled(s.options.EmbeddedSearchTools) {
		result = append(result, "  - To search for files use Glob instead of find or ls", "  - To search the content of files, use Grep instead of grep or rg")
	}
	result = append(result, "  - Reserve using the Bash exclusively for system commands and terminal operations that require shell execution. If you are unsure and there is a relevant dedicated tool, default to using the dedicated tool and only fallback on using the Bash tool for these if it is absolutely necessary.")
	if taskText != "" {
		result = append(result, " - "+taskText)
	}
	result = append(result, " - You can call multiple tools in a single response. If you intend to call multiple tools and there are no dependencies between them, make all independent tool calls in parallel. Maximize use of parallel tool calls where possible to increase efficiency. However, if some tool calls depend on previous calls to inform dependent values, do NOT call these tools in parallel and instead call them sequentially. For instance, if one operation must complete before another starts, run these operations sequentially instead.")
	return strings.Join(result, "\n")
}

func (s *Service) sessionGuidance(names map[string]bool, commands []Command) *string {
	items := []string{}
	if names["AskUserQuestion"] {
		items = append(items, "If you do not understand why the user has denied a tool call, use the AskUserQuestion to ask them.")
	}
	if !s.options.NonInteractive() {
		items = append(items, "If you need the user to run a shell command themselves (e.g., an interactive login like `gcloud auth login`), suggest they type `! <command>` in the prompt — the `!` prefix runs the command in this session so its output lands directly in the conversation.")
	}
	if names["Agent"] {
		if enabled(s.options.ForkSubagents) {
			items = append(items, "Calling Agent without a subagent_type creates a fork, which runs in the background and keeps its tool output out of your context — so you can keep chatting with the user while it works. Reach for it when research or multi-step implementation work would otherwise fill your context with raw output you won't need again. **If you ARE the fork** — execute directly; do not re-delegate.")
		} else {
			items = append(items, "Use the Agent tool with specialized agents when the task at hand matches the agent's description. Subagents are valuable for parallelizing independent queries or for protecting the main context window from excessive results, but they should not be used excessively when not needed. Importantly, avoid duplicating work that subagents are already doing - if you delegate research to a subagent, do not also perform the same searches yourself.")
			search := "the Glob or Grep"
			if enabled(s.options.EmbeddedSearchTools) {
				search = "`find` or `grep` via the Bash tool"
			}
			items = append(items, "For simple, directed codebase searches (e.g. for a specific file/class/function) use "+search+" directly.", "For broader codebase exploration and deep research, use the Agent tool with subagent_type=Explore. This is slower than using "+search+" directly, so use this only when a simple, directed search proves to be insufficient or when your task will clearly require more than 3 queries.")
		}
	}
	if len(commands) > 0 && names["Skill"] {
		items = append(items, "/<skill-name> (e.g., /commit) is shorthand for users to invoke a user-invocable skill. When executed, the skill gets expanded to a full prompt. Use the Skill tool to execute them. IMPORTANT: Only use Skill for skills listed in its user-invocable skills section - do not guess or use built-in CLI commands.")
	}
	if len(items) == 0 {
		return nil
	}
	return pointer(bullets("# Session-specific guidance", items))
}

func scratchpad(path string) string {
	return "# Scratchpad Directory\n\nIMPORTANT: Always use this scratchpad directory for temporary files instead of `/tmp` or other system temp directories:\n`" + path + "`\n\nUse this directory for ALL temporary file needs:\n- Storing intermediate results or data during multi-step tasks\n- Writing temporary scripts or configuration files\n- Saving outputs that don't belong in the user's project\n- Creating working files during analysis or processing\n- Any file that would otherwise go to `/tmp`\n\nOnly use `/tmp` if the user explicitly requests it.\n\nThe scratchpad directory is session-specific, isolated from the user's project, and can be used freely without permission prompts."
}
