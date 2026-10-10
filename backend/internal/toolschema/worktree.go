package toolschema

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

//go:embed assets/enterworktree-prompt.md
var enterWorktreePrompt string

//go:embed assets/exitworktree-prompt.md
var exitWorktreePrompt string

func worktreeFields(name string) []Field {
	if name == "EnterWorktree" {
		return []Field{{"name", textSchema("Optional name for the worktree. Each \"/\"-separated segment may contain only letters, digits, dots, underscores, and dashes; max 64 chars total. A random name is generated if not provided.", true)}}
	}
	action := textSchema("\"keep\" leaves the worktree and branch on disk; \"remove\" deletes both.", false)
	action.Kind = "enum"
	action.Values = []string{"keep", "remove"}
	discard := boolSchema("Required true when action is \"remove\" and the worktree has uncommitted files or unmerged commits. The tool will refuse and list them otherwise.")
	discard.Semantic = false
	return []Field{{"action", action}, {"discard_changes", discard}}
}

var worktreeSegment = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

func worktreeSlugIssue(value json.RawMessage) json.RawMessage {
	units, e := protocol.DecodeJavaScriptJSONString(value)
	if e != nil {
		return nil
	}
	message := json.RawMessage(nil)
	if len(units) > 64 {
		message = quote(fmt.Sprintf("Invalid worktree name: must be 64 characters or fewer (got %d)", len(units)))
	} else {
		text := string(utf16.Decode(units))
		for _, segment := range strings.Split(text, "/") {
			suffix := ""
			if segment == "." || segment == ".." {
				suffix = ": must not contain \".\" or \"..\" path segments"
			} else if !worktreeSegment.MatchString(segment) {
				suffix = ": each \"/\"-separated segment must be non-empty and contain only letters, digits, dots, underscores, and dashes"
			}
			if suffix != "" {
				out := utf16.Encode([]rune("Invalid worktree name \""))
				out = append(out, units...)
				out = append(out, '"')
				out = append(out, utf16.Encode([]rune(suffix))...)
				message = protocol.QuoteJavaScriptUTF16(out)
				break
			}
		}
	}
	if message == nil {
		return nil
	}
	return raw(protocol.NewJSONObject(protocol.JSONField{Name: "code", Value: quote("custom")}, protocol.JSONField{Name: "message", Value: message}, protocol.JSONField{Name: "path", Value: array([]string{"name"})}))
}
func parseWorktreeInput(def Definition, in protocol.JSONObject) (protocol.JSONObject, error) {
	result, e := parseNativeInput(def, in)
	issue := json.RawMessage(nil)
	if def.Name == "EnterWorktree" {
		issue = worktreeSlugIssue(in.Get("name"))
	}
	if issue == nil {
		return result, e
	}
	// Zod's field refinement precedes the strict object's unknown-key issue.
	if failure, ok := e.(*ValidationError); ok {
		var issues []json.RawMessage
		_ = json.Unmarshal(failure.Issues, &issues)
		refined := []json.RawMessage{issue}
		refined = append(refined, issues...)
		return protocol.JSONObject{}, &ValidationError{Issues: array(refined)}
	}
	return protocol.JSONObject{}, &ValidationError{Issues: array([]json.RawMessage{issue})}
}
