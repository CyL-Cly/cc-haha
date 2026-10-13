package attachments

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/dlclark/regexp2/v2"
)

func shellQuotedPath(path string) (string, error) {
	if path == "" {
		return "''", nil
	}
	space := regexp2.MustCompile(`["\s\\]`, regexp2.ECMAScript)
	matched, e := space.MatchString(path)
	if e != nil {
		return "", e
	}
	if matched && !strings.Contains(path, "'") {
		return "'" + path + "'", nil
	}
	space = regexp2.MustCompile(`["'\s]`, regexp2.ECMAScript)
	matched, e = space.MatchString(path)
	if e != nil {
		return "", e
	}
	if matched {
		out := strings.Builder{}
		out.WriteByte('"')
		for _, r := range path {
			if strings.ContainsRune("\"\\$`!", r) {
				out.WriteByte('\\')
			}
			out.WriteRune(r)
		}
		out.WriteByte('"')
		return out.String(), nil
	}
	pattern := regexp2.MustCompile(`([A-Za-z]:)?([#!"$&'()*,:;<=>?@[\\\]^`+"`"+`{|}])`, regexp2.ECMAScript)
	return pattern.ReplaceFunc(path, func(match regexp2.Match) string {
		return match.Groups()[1].String() + "\\" + match.Groups()[2].String()
	}, -1, -1)
}

func projectSkillFile(value protocol.JSONObject, factory *messages.Factory, options *PlanOptions) ([]protocol.JSONObject, error) {
	if factory == nil {
		return nil, errors.New("file attachment requires the owning message factory")
	}
	contents := []json.RawMessage{}
	var name string
	var input protocol.JSONObject
	var data json.RawMessage
	switch value.String("type") {
	case "agent_mention":
		agent, e := protocol.JavaScriptStringJSON(value.Get("agentType"))
		if e != nil {
			return nil, e
		}
		text, e := concatenate(quoted("The user has expressed a desire to invoke the agent \""), agent, quoted("\". Please invoke the agent appropriately, passing in the required context to it. "))
		if e != nil {
			return nil, e
		}
		contents = append(contents, text)
	case "directory":
		path, e := protocol.JavaScriptStringJSON(value.Get("path"))
		if e != nil {
			return nil, e
		}
		var decoded string
		_ = json.Unmarshal(path, &decoded)
		quotedPath, e := shellQuotedPath(decoded)
		if e != nil {
			return nil, e
		}
		name = "Bash"
		description, e := concatenate(quoted("Lists files in "), path)
		if e != nil {
			return nil, e
		}
		input = protocol.NewJSONObject(field("command", quoted("ls "+quotedPath)), field("description", description))
		payload := protocol.NewJSONObject(field("stdout", value.Get("content")), field("stderr", quoted("")), field("interrupted", json.RawMessage("false")))
		data, _ = payload.MarshalJSON()
	case "file":
		content, e := object(value.Get("content"))
		if e != nil || !content.IsObject() {
			return nil, &CapabilityError{Boundary: "malformed retained file attachment"}
		}
		// The original file-attachment switch has no parts case: extracted
		// pages are supplemental live tool messages, not retained attachments.
		if content.String("type") == "parts" {
			return []protocol.JSONObject{}, nil
		}
		if content.String("type") != "text" && content.String("type") != "notebook" && content.String("type") != "image" && content.String("type") != "pdf" {
			return nil, &CapabilityError{Boundary: "retained file " + content.String("type") + " mapper"}
		}
		name = "Read"
		input = protocol.NewJSONObject(field("file_path", value.Get("filename")))
		data = value.Get("content")
	default:
		return nil, &CapabilityError{Boundary: "file attachment kind " + value.String("type")}
	}
	if name != "" {
		if options == nil || options.MapToolResult == nil {
			return nil, &CapabilityError{Boundary: "owning native tool mapper for retained file"}
		}
		in, _ := input.MarshalJSON()
		call, e := concatenate(quoted("Called the "+name+" tool with the following input: "), quoted(string(protocol.StringifyValidatedJavaScriptJSON(in))))
		if e != nil {
			return nil, e
		}
		contents = append(contents, call)
		mapped, e := options.MapToolResult(name, data, quoted("1"))
		var failure *CapabilityError
		if errors.As(e, &failure) {
			return nil, e
		}
		if e != nil {
			contents = append(contents, quoted("Result of calling the "+name+" tool: Error"))
		} else {
			content := mapped.Get("content")
			var blocks []protocol.JSONObject
			preserveImage := false
			if json.Unmarshal(content, &blocks) == nil {
				for _, block := range blocks {
					if block.String("type") == "image" {
						preserveImage = true
						break
					}
				}
			}
			if preserveImage {
				contents = append(contents, content)
			} else {
				if len(content) == 0 || content[0] != '"' {
					content = quoted(string(protocol.StringifyValidatedJavaScriptJSON(content)))
				}
				result, e := concatenate(quoted("Result of calling the "+name+" tool:\n"), content)
				if e != nil {
					return nil, e
				}
				contents = append(contents, result)
			}
		}
		if value.String("type") == "file" && truthy(value.Get("truncated")) {
			filename, e := protocol.JavaScriptStringJSON(value.Get("filename"))
			if e != nil {
				return nil, e
			}
			text, e := concatenate(quoted("Note: The file "), filename, quoted(" was too large and has been truncated to the first 2000 lines. Don't tell the user about this truncation. Use Read to read more of the file if you need."))
			if e != nil {
				return nil, e
			}
			contents = append(contents, text)
		}
	}
	users := []protocol.JSONObject{}
	// The original renderer allocates every user before wrapping any content.
	for _, content := range contents {
		users = append(users, messages.OrderedUser(factory.CreateUser(storage.Object{"content": content, "isMeta": json.RawMessage("true")})))
	}
	for i := range users {
		api, _ := users[i].Object("message")
		content := api.Get("content")
		if len(content) > 0 && content[0] == '[' {
			var blocks []protocol.JSONObject
			if err := json.Unmarshal(content, &blocks); err != nil {
				return nil, err
			}
			for j, block := range blocks {
				if block.String("type") == "text" {
					wrapped, err := concatenate(quoted("<system-reminder>\n"), block.Get("text"), quoted("\n</system-reminder>"))
					if err != nil {
						return nil, err
					}
					blocks[j].Set("text", wrapped)
				}
			}
			raw, _ := json.Marshal(blocks)
			api.Set("content", raw)
			message, _ := api.MarshalJSON()
			users[i].Set("message", message)
			continue
		}
		wrapped, e := concatenate(quoted("<system-reminder>\n"), api.Get("content"), quoted("\n</system-reminder>"))
		if e != nil {
			return nil, e
		}
		api.Set("content", wrapped)
		raw, _ := api.MarshalJSON()
		users[i].Set("message", raw)
	}
	return users, nil
}
