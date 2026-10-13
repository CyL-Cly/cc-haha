// Package compaction owns summary prompts and history transitions shared by all
// native clients. The summary always comes from the configured model.
package compaction

import (
	"embed"
	"regexp"
	"strings"
)

//go:embed prompts/*.txt
var prompts embed.FS

func Prompt(custom, arm, direction string) string {
	shape := "a <summary> block"
	key := "lean"
	if strings.TrimSpace(arm) == "control" {
		key = "control"
		shape = "an <analysis> block followed by a <summary> block"
	}
	if direction != "" {
		key = direction
		shape = "an <analysis> block followed by a <summary> block"
	}
	if key != "lean" && key != "control" && key != "from" && key != "up_to" {
		key = "from"
	}
	body, _ := prompts.ReadFile("prompts/" + key + ".txt")
	out := "CRITICAL: Respond with TEXT ONLY. Do NOT call any tools.\n\n- Do NOT use Read, Bash, Grep, Glob, Edit, Write, or ANY other tool.\n- You already have all the context you need in the conversation above.\n- Tool calls will be REJECTED and will waste your only turn — you will fail the task.\n- Your entire response must be plain text: " + shape + ".\n\n" + string(body)
	if strings.TrimSpace(custom) != "" {
		out += "\n\nAdditional Instructions:\n" + custom
	}
	return out + "\n\nREMINDER: Do NOT call any tools. Respond with plain text only — " + shape + ". Tool calls will be rejected and you will fail the task."
}

var analysis = regexp.MustCompile(`<analysis>[\s\S]*?</analysis>`)
var summary = regexp.MustCompile(`<summary>([\s\S]*?)</summary>`)
var whitespace = regexp.MustCompile(`\n\n+`)

func FormatSummary(text string) string {
	if loc := analysis.FindStringIndex(text); loc != nil {
		text = text[:loc[0]] + text[loc[1]:]
	}
	if loc := summary.FindStringSubmatchIndex(text); loc != nil {
		text = text[:loc[0]] + "Summary:\n" + strings.TrimSpace(text[loc[2]:loc[3]]) + text[loc[1]:]
	}
	return strings.TrimSpace(whitespace.ReplaceAllString(text, "\n\n"))
}
func UserSummary(text string, suppress bool, path string, recent bool) string {
	out := "This session is being continued from a previous conversation that ran out of context. The summary below covers the earlier portion of the conversation.\n\n" + FormatSummary(text)
	if path != "" {
		out += "\n\nIf you need specific details from before compaction (like exact code snippets, error messages, or content you generated), read the full transcript at: " + path
	}
	if recent {
		out += "\n\nRecent messages are preserved verbatim."
	}
	if suppress {
		out += "\nContinue the conversation from where it left off without asking the user any further questions. Resume directly — do not acknowledge the summary, do not recap what was happening, do not preface with \"I'll continue\" or similar. Pick up the last task as if the break never happened."
	}
	return out
}
func MergeInstructions(user, hook string) string {
	if hook == "" {
		return user
	}
	if user == "" {
		return hook
	}
	return user + "\n\n" + hook
}
