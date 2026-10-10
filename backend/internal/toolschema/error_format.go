package toolschema

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// ValidationFeedback ports Source formatZodValidationError and preserves the
// separate Zod error.message receipt used by the transcript.
func ValidationFeedback(name string, failure error) (content, receipt string) {
	var validation *ValidationError
	if !errors.As(failure, &validation) {
		return failure.Error(), failure.Error()
	}
	var pretty bytes.Buffer
	if e := json.Indent(&pretty, validation.Issues, "", "  "); e != nil {
		return failure.Error(), failure.Error()
	}
	receipt = pretty.String()
	content = receipt
	var issues []protocol.JSONObject
	_ = json.Unmarshal(validation.Issues, &issues)
	missing, unexpected, mismatches := []string{}, []string{}, []string{}
	for _, issue := range issues {
		path := []json.RawMessage{}
		_ = json.Unmarshal(issue.Get("path"), &path)
		formatted := ""
		for i, part := range path {
			if len(part) > 0 && part[0] == '"' {
				var key string
				_ = json.Unmarshal(part, &key)
				if i > 0 {
					formatted += "."
				}
				formatted += key
			} else {
				formatted += "[" + string(part) + "]"
			}
		}
		switch issue.String("code") {
		case "invalid_type":
			if strings.Contains(issue.String("message"), "received undefined") {
				missing = append(missing, "The required parameter `"+formatted+"` is missing")
			} else {
				received := "unknown"
				if match := regexp.MustCompile(`received (\w+)`).FindStringSubmatch(issue.String("message")); len(match) > 1 {
					received = match[1]
				}
				mismatches = append(mismatches, "The parameter `"+formatted+"` type is expected as `"+issue.String("expected")+"` but provided as `"+received+"`")
			}
		case "unrecognized_keys":
			var keys []string
			_ = json.Unmarshal(issue.Get("keys"), &keys)
			for _, key := range keys {
				unexpected = append(unexpected, "An unexpected parameter `"+key+"` was provided")
			}
		}
	}
	parts := append(append(missing, unexpected...), mismatches...)
	if len(parts) > 0 {
		word := "issue"
		if len(parts) > 1 {
			word = "issues"
		}
		content = name + " failed due to the following " + word + ":\n" + strings.Join(parts, "\n")
	}
	return content, receipt
}
