package toolschema

import "strings"

func agentPrompt(host Host) (string, error) {
	if host.AgentDefinitions == nil {
		return "", unsupported("actual Agent definitions")
	}
	lines := []string{}
	for _, agent := range host.AgentDefinitions {
		tools := "All tools"
		if len(agent.Tools) > 0 {
			selected := []string{}
			for _, tool := range agent.Tools {
				denied := false
				for _, deny := range agent.DisallowedTools {
					denied = denied || tool == deny
				}
				if !denied {
					selected = append(selected, tool)
				}
			}
			if len(selected) == 0 {
				tools = "None"
			} else {
				tools = strings.Join(selected, ", ")
			}
		} else if len(agent.DisallowedTools) > 0 {
			tools = "All tools except " + strings.Join(agent.DisallowedTools, ", ")
		}
		lines = append(lines, "- "+agent.Type+": "+agent.WhenToUse+" (Tools: "+tools+")")
	}
	shared := "Launch a new agent to handle complex, multi-step tasks autonomously.\n\nThe Agent tool launches specialized agents (subprocesses) that autonomously handle complex tasks. Each agent type has specific capabilities and tools available to it.\n\nAvailable agent types and the tools they have access to:\n" + strings.Join(lines, "\n") + "\n\nWhen using the Agent tool, specify a subagent_type parameter to select which agent type to use. If omitted, the general-purpose agent is used."
	whenNot := "\nWhen NOT to use the Agent tool:\n- If you want to read a specific file path, use the Read tool or the Glob tool instead of the Agent tool, to find the match more quickly\n- If you are searching for a specific class definition like \"class Foo\", use the Glob tool instead, to find the match more quickly\n- If you are searching for code within a specific file or set of 2-3 files, use the Read tool instead of the Agent tool, to find the match more quickly\n- Other tasks that are not related to the agent descriptions above\n"
	background := ""
	if !promptTruthy(host.BashEnvironment["CLAUDE_CODE_DISABLE_BACKGROUND_TASKS"]) {
		background = "\n- You can optionally run agents in the background using the run_in_background parameter. When an agent runs in the background, you will be automatically notified when it completes — do NOT sleep, poll, or proactively check on its progress. Continue with other work or respond to the user instead.\n- **Foreground vs background**: Use foreground (default) when you need the agent's results before you can proceed — e.g., research agents whose findings inform your next steps. Use background when you have genuinely independent work to do in parallel."
	}
	return shared + "\n" + whenNot + "\n\nUsage notes:\n- Always include a short description (3-5 words) summarizing what the agent will do\n- Launch multiple agents concurrently whenever possible, to maximize performance; to do that, use a single message with multiple tool uses\n- When the agent is done, it will return a single message back to you. The result returned by the agent is not visible to the user. To show the user the result, you should send a text message back to the user with a concise summary of the result." + background + "\n- To continue a previously spawned agent, use SendMessage with the agent's ID or name as the `to` field. The agent resumes with its full context preserved. Each Agent invocation starts fresh — provide a complete task description.\n- The agent's outputs should generally be trusted\n- Clearly tell the agent whether you expect it to write code or just to do research (search, file reads, web fetches, etc.), since it is not aware of the user's intent\n- If the agent description mentions that it should be used proactively, then you should try your best to use it without the user having to ask for it first. Use your judgement.\n- If the user specifies that they want you to run agents \"in parallel\", you MUST send a single message with multiple Agent tool use content blocks. For example, if you need to launch both a build-validator agent and a test-runner agent in parallel, send a single message with both tool calls.\n- You can optionally set `isolation: \"worktree\"` to run the agent in a temporary git worktree, giving it an isolated copy of the repository. The worktree is automatically cleaned up if the agent makes no changes; if changes are made, the worktree path and branch are returned in the result. Outside a git repository the agent runs in the workspace directory instead of failing." + agentWritingPrompt + "\n\n" + agentCurrentExamples, nil
}
func promptTruthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

const agentWritingPrompt = "\n\n## Writing the prompt\n\nBrief the agent like a smart colleague who just walked into the room — it hasn't seen this conversation, doesn't know what you've tried, doesn't understand why this task matters.\n- Explain what you're trying to accomplish and why.\n- Describe what you've already learned or ruled out.\n- Give enough context about the surrounding problem that the agent can make judgment calls rather than just following a narrow instruction.\n- If you need a short response, say so (\"report in under 200 words\").\n- Lookups: hand over the exact command. Investigations: hand over the question — prescribed steps become dead weight when the premise is wrong.\n\nTerse command-style prompts produce shallow, generic work.\n\n**Never delegate understanding.** Don't write \"based on your findings, fix the bug\" or \"based on the research, implement it.\" Those phrases push synthesis onto the agent instead of doing it yourself. Write prompts that prove you understood: include file paths, line numbers, what specifically to change.\n"

const agentCurrentExamples = "Example usage:\n\n<example_agent_descriptions>\n\"test-runner\": use this agent after you are done writing code to run tests\n\"greeting-responder\": use this agent to respond to user greetings with a friendly joke\n</example_agent_descriptions>\n\n<example>\nuser: \"Please write a function that checks if a number is prime\"\nassistant: I'm going to use the Write tool to write the following code:\n<code>\nfunction isPrime(n) {\n  if (n <= 1) return false\n  for (let i = 2; i * i <= n; i++) {\n    if (n % i === 0) return false\n  }\n  return true\n}\n</code>\n<commentary>\nSince a significant piece of code was written and the task was completed, now use the test-runner agent to run the tests\n</commentary>\nassistant: Uses the Agent tool to launch the test-runner agent\n</example>\n\n<example>\nuser: \"Hello\"\n<commentary>\nSince the user is greeting, use the greeting-responder agent to respond with a friendly joke\n</commentary>\nassistant: \"I'm going to use the Agent tool to launch the greeting-responder agent\"\n</example>\n"
