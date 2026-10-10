package insights

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

var languages = map[string]string{".ts": "TypeScript", ".tsx": "TypeScript", ".js": "JavaScript", ".jsx": "JavaScript", ".py": "Python", ".rb": "Ruby", ".go": "Go", ".rs": "Rust", ".java": "Java", ".md": "Markdown", ".json": "JSON", ".yaml": "YAML", ".yml": "YAML", ".sh": "Shell", ".css": "CSS", ".html": "HTML"}

type StatsOptions struct {
	ParseDate ParseDate
	Location  *time.Location
}

func requiredString(value json.RawMessage) (string, error) {
	var text string
	if json.Unmarshal(value, &text) != nil {
		return "", errors.New("Insights expected a string")
	}
	return text, nil
}
func contentBlocks(content json.RawMessage) ([]protocol.JSONObject, error) {
	if len(content) == 0 || content[0] != '[' {
		return nil, nil
	}
	var blocks []protocol.JSONObject
	if err := json.Unmarshal(content, &blocks); err != nil {
		return nil, err
	}
	for _, block := range blocks {
		if !block.IsObject() {
			return nil, errors.New("Insights message content contains a non-object")
		}
	}
	return blocks, nil
}
func isHuman(content json.RawMessage) (bool, error) {
	if len(content) > 0 && content[0] == '"' {
		text, _ := requiredString(content)
		return protocol.TrimJavaScriptString(text) != "", nil
	}
	blocks, err := contentBlocks(content)
	if err != nil {
		return false, err
	}
	for _, block := range blocks {
		if block.String("type") == "text" && block.Get("text") != nil {
			return true, nil
		}
	}
	return false, nil
}

// LogMeta derives original Insights metadata from actual canonical branches.
// No token estimates, fixed tool totals, or cache markers are introduced.
func LogMeta(log Log, o StatsOptions) (protocol.JSONObject, error) {
	if o.ParseDate == nil {
		return protocol.JSONObject{}, errors.New("Insights requires its owning Date parser")
	}
	if o.Location == nil {
		o.Location = time.Local
	}
	if math.IsNaN(log.CreatedMillis) || math.IsNaN(log.ModifiedMillis) {
		return protocol.JSONObject{}, errors.New("Invalid time value")
	}
	tools, langs, categories := protocol.NewJSONObject(), protocol.NewJSONObject(), protocol.NewJSONObject()
	increment := func(object *protocol.JSONObject, key string, n float64) {
		if key == "__proto__" {
			return
		}
		previous := object.Get(key)
		if previous == nil {
			if name, exists := objectPrototypeFunctions[key]; exists {
				previous = raw("function " + name + "() { [native code] }")
			}
		}
		object.Set(key, add(previous, raw(n)))
	}
	inputs, outputs := json.RawMessage("0"), json.RawMessage("0")
	commits, pushes, interruptions, toolErrors, added, removed, userCount, assistantCount := 0, 0, 0, 0, 0, 0, 0, 0
	task, mcp, search, fetch := false, false, false, false
	files := map[string]bool{}
	hours := []json.RawMessage{}
	stamps := []string{}
	responses := []float64{}
	lastAssistant := ""
	for _, record := range log.Messages {
		body, present := record.Object("message")
		content := body.Get("content")
		stamp := record.String("timestamp")
		if record.String("type") == "assistant" {
			assistantCount++
		}
		if record.String("type") == "assistant" && present {
			if stamp != "" {
				lastAssistant = stamp
			}
			if usage, exists := body.Object("usage"); exists {
				a, b := usage.Get("input_tokens"), usage.Get("output_tokens")
				if !truthy(a) {
					a = raw(0)
				}
				if !truthy(b) {
					b = raw(0)
				}
				inputs = add(inputs, a)
				outputs = add(outputs, b)
			}
			blocks, err := contentBlocks(content)
			if err != nil {
				return protocol.JSONObject{}, err
			}
			for _, block := range blocks {
				if block.String("type") != "tool_use" || block.Get("name") == nil {
					continue
				}
				name, err := requiredString(block.Get("name"))
				if err != nil {
					return protocol.JSONObject{}, err
				}
				increment(&tools, name, 1)
				task = task || name == "Agent" || name == "Task"
				mcp = mcp || strings.HasPrefix(name, "mcp__")
				search = search || name == "WebSearch"
				fetch = fetch || name == "WebFetch"
				input, exists := block.Object("input")
				if !exists {
					continue
				}
				path := ""
				if truthy(input.Get("file_path")) {
					path, err = requiredString(input.Get("file_path"))
					if err != nil {
						return protocol.JSONObject{}, err
					}
				}
				if path != "" {
					if language := languages[strings.ToLower(nodeExtension(path))]; language != "" {
						increment(&langs, language, 1)
					}
					if name == "Edit" || name == "Write" {
						files[path] = true
					}
				}
				if name == "Edit" {
					before, after := "", ""
					if truthy(input.Get("old_string")) {
						before, err = requiredString(input.Get("old_string"))
						if err != nil {
							return protocol.JSONObject{}, err
						}
					}
					if truthy(input.Get("new_string")) {
						after, err = requiredString(input.Get("new_string"))
						if err != nil {
							return protocol.JSONObject{}, err
						}
					}
					a, r := builtinwrite.DiffLineCounts(before, after)
					added += a
					removed += r
				}
				if name == "Write" && truthy(input.Get("content")) {
					text, err := requiredString(input.Get("content"))
					if err != nil {
						return protocol.JSONObject{}, err
					}
					added += strings.Count(text, "\n") + 1
				}
				if truthy(input.Get("command")) {
					command, err := requiredString(input.Get("command"))
					if err != nil {
						return protocol.JSONObject{}, err
					}
					if strings.Contains(command, "git commit") {
						commits++
					}
					if strings.Contains(command, "git push") {
						pushes++
					}
				}
			}
		}
		if record.String("type") != "user" || !present {
			continue
		}
		human, err := isHuman(content)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		if human {
			userCount++
			if stamp != "" {
				ms, err := o.ParseDate(stamp)
				if err != nil {
					return protocol.JSONObject{}, err
				}
				hour := math.NaN()
				if !math.IsNaN(ms) {
					hour = float64(time.UnixMilli(int64(ms)).In(o.Location).Hour())
				}
				hours = append(hours, number(hour))
				stamps = append(stamps, stamp)
			}
			if lastAssistant != "" && stamp != "" {
				previous, err := o.ParseDate(lastAssistant)
				if err != nil {
					return protocol.JSONObject{}, err
				}
				current, err := o.ParseDate(stamp)
				if err != nil {
					return protocol.JSONObject{}, err
				}
				delta := (current - previous) / 1000
				if delta > 2 && delta < 3600 {
					responses = append(responses, delta)
				}
			}
		}
		blocks, err := contentBlocks(content)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		for _, block := range blocks {
			if block.String("type") == "tool_result" && block.Get("content") != nil && truthy(block.Get("is_error")) {
				toolErrors++
				category := "Other"
				if value := block.Get("content"); len(value) > 0 && value[0] == '"' {
					text, _ := requiredString(value)
					text = strings.ToLower(text)
					switch {
					case strings.Contains(text, "exit code"):
						category = "Command Failed"
					case strings.Contains(text, "rejected") || strings.Contains(text, "doesn't want"):
						category = "User Rejected"
					case strings.Contains(text, "string to replace not found") || strings.Contains(text, "no changes"):
						category = "Edit Failed"
					case strings.Contains(text, "modified since read"):
						category = "File Changed"
					case strings.Contains(text, "exceeds maximum") || strings.Contains(text, "too large"):
						category = "File Too Large"
					case strings.Contains(text, "file not found") || strings.Contains(text, "does not exist"):
						category = "File Not Found"
					}
				}
				increment(&categories, category, 1)
			}
		}
		if len(content) > 0 && content[0] == '"' {
			text, _ := requiredString(content)
			if strings.Contains(text, "[Request interrupted by user") {
				interruptions++
			}
		} else {
			for _, block := range blocks {
				if block.String("type") == "text" && block.Get("text") != nil {
					text, err := requiredString(block.Get("text"))
					if err != nil {
						return protocol.JSONObject{}, err
					}
					if strings.Contains(text, "[Request interrupted by user") {
						interruptions++
						break
					}
				}
			}
		}
	}
	sid := log.SessionID
	if sid == "" {
		sid = "unknown"
	}
	created := time.UnixMilli(int64(log.CreatedMillis)).UTC().Format("2006-01-02T15:04:05.000Z")
	values := []protocol.JSONField{{Name: "session_id", Value: raw(sid)}, {Name: "project_path", Value: raw(log.ProjectPath)}, {Name: "start_time", Value: raw(created)}, {Name: "duration_minutes", Value: number(math.Floor((log.ModifiedMillis-log.CreatedMillis)/60000 + 0.5))}, {Name: "user_message_count", Value: raw(userCount)}, {Name: "assistant_message_count", Value: raw(assistantCount)}, {Name: "tool_counts", Value: raw(tools)}, {Name: "languages", Value: raw(langs)}, {Name: "git_commits", Value: raw(commits)}, {Name: "git_pushes", Value: raw(pushes)}, {Name: "input_tokens", Value: inputs}, {Name: "output_tokens", Value: outputs}, {Name: "first_prompt", Value: raw(log.FirstPrompt)}, {Name: "summary", Value: log.Summary}, {Name: "user_interruptions", Value: raw(interruptions)}, {Name: "user_response_times", Value: raw(responses)}, {Name: "tool_errors", Value: raw(toolErrors)}, {Name: "tool_error_categories", Value: raw(categories)}, {Name: "uses_task_agent", Value: raw(task)}, {Name: "uses_mcp", Value: raw(mcp)}, {Name: "uses_web_search", Value: raw(search)}, {Name: "uses_web_fetch", Value: raw(fetch)}, {Name: "lines_added", Value: raw(added)}, {Name: "lines_removed", Value: raw(removed)}, {Name: "files_modified", Value: raw(len(files))}, {Name: "message_hours", Value: raw(hours)}, {Name: "user_message_timestamps", Value: raw(stamps)}}
	return protocol.NewJSONObject(values...), nil
}
