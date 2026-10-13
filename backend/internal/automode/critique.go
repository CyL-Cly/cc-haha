package automode

import (
	"encoding/json"
	"io"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

const CritiqueNoCustomRules = "No custom auto mode rules found.\n\nAdd rules to your settings file under autoMode.{allow, soft_deny, hard_deny, environment}.\nRun `claude auto-mode defaults` to see the default rules for reference.\n"
const CritiqueAnalyzing = "Analyzing your auto mode rules…\n\n"
const critiqueSystem = `You are an expert reviewer of auto mode classifier rules for Claude Code.

Claude Code has an "auto mode" that uses an AI classifier to decide whether tool calls should be auto-approved or require user confirmation. Users can write custom rules in four categories:

- **allow**: Actions the classifier should auto-approve
- **soft_deny**: Actions the classifier should block (require user confirmation)
- **hard_deny**: Actions the classifier must block unconditionally
- **environment**: Context about the user's setup that helps the classifier make decisions

Your job is to critique the user's custom rules for clarity, completeness, and potential issues. The classifier is an LLM that reads these rules as part of its system prompt.

For each rule, evaluate:
1. **Clarity**: Is the rule unambiguous? Could the classifier misinterpret it?
2. **Completeness**: Are there gaps or edge cases the rule doesn't cover?
3. **Conflicts**: Do any of the rules conflict with each other?
4. **Actionability**: Is the rule specific enough for the classifier to act on?

Be concise and constructive. Only comment on rules that could be improved. If all rules look good, say so.`

// CritiqueOptions accepts the same unprocessed trusted settings sources as the
// classifier. An explicitly empty section is still custom; classifyAllShell
// alone does not request a critique. No model work is admitted here.
func CritiqueOptions(snapshot Snapshot) (requestcontext.SideQueryInput, bool, error) {
	config := MergeTrusted(snapshot)
	if config == nil || config.Allow == nil && config.SoftDeny == nil && config.HardDeny == nil && config.Environment == nil {
		return requestcontext.SideQueryInput{}, false, nil
	}
	defaults := Defaults(snapshot.TranscriptClassifier)
	classifier, err := BuildClassifierSystem(Snapshot{TranscriptClassifier: snapshot.TranscriptClassifier})
	if err != nil {
		return requestcontext.SideQueryInput{}, false, err
	}
	parts := []json.RawMessage{quoted("Here is the full classifier system prompt that the auto mode classifier receives:\n\n<classifier_system_prompt>\n"), classifier, quoted("\n</classifier_system_prompt>\n\nHere are the user's custom rules that REPLACE the corresponding default sections:\n\n")}
	for _, section := range []struct {
		name             string
		custom, defaults []json.RawMessage
	}{{"allow", config.Allow, defaults.Allow}, {"soft_deny", config.SoftDeny, defaults.SoftDeny}, {"hard_deny", config.HardDeny, defaults.HardDeny}, {"environment", config.Environment, defaults.Environment}} {
		if section.custom == nil {
			continue
		}
		parts = append(parts, quoted("## "+section.name+" (custom rules replacing defaults)\nEffective:\n"))
		effective := resolve(section.custom, section.defaults)
		if len(effective) == 0 {
			parts = append(parts, quoted("(explicitly empty)"))
		} else {
			parts = appendCritiqueRules(parts, effective)
		}
		parts = append(parts, quoted("\n\nDefaults being replaced:\n"))
		parts = appendCritiqueRules(parts, section.defaults)
		parts = append(parts, quoted("\n\n"))
	}
	parts = append(parts, quoted("\nPlease critique these custom rules."))
	content, err := joinRaw(parts...)
	if err != nil {
		return requestcontext.SideQueryInput{}, false, err
	}
	message := obj("role", quoted("user"))
	message.Set("content", content)
	system := obj("type", quoted("text"))
	system.Set("text", quoted(critiqueSystem))
	return requestcontext.SideQueryInput{MaxTokens: 4096, SkipPrefix: true, System: []protocol.JSONObject{system}, Messages: []protocol.JSONObject{message}}, true, nil
}

func appendCritiqueRules(parts []json.RawMessage, rules []json.RawMessage) []json.RawMessage {
	for index, rule := range rules {
		if index > 0 {
			parts = append(parts, quoted("\n"))
		}
		parts = append(parts, quoted("- "), rule)
	}
	return parts
}

func WriteCritiqueResponse(writer io.Writer, response json.RawMessage) error {
	var message protocol.JSONObject
	if err := message.UnmarshalJSON(response); err != nil {
		return err
	}
	var blocks []protocol.JSONObject
	if err := json.Unmarshal(message.Get("content"), &blocks); err != nil {
		return err
	}
	for _, block := range blocks {
		if block.String("type") == "text" {
			_, err := io.WriteString(writer, block.String("text")+"\n")
			return err
		}
	}
	_, err := io.WriteString(writer, "No critique was generated. Please try again.\n")
	return err
}
