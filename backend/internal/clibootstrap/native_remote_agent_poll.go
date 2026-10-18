package clibootstrap

import (
	"encoding/json"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/ccrsessions"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

type nativeRemoteAgentPoll struct {
	log    []protocol.JSONObject
	last   json.RawMessage
	idle   int
	review *string
}

type nativeRemoteAgentPollResult struct {
	task                protocol.JSONObject
	delta, notification string
	terminal            bool
}

func remoteAgentText(event protocol.JSONObject) string {
	message, _ := event.Object("message")
	var blocks []protocol.JSONObject
	if json.Unmarshal(message.Get("content"), &blocks) != nil {
		return ""
	}
	parts := []string{}
	for _, block := range blocks {
		if block.String("type") == "text" {
			parts = append(parts, block.String("text"))
		}
	}
	return strings.Join(parts, "\n")
}

func remoteAgentTag(text, tag string) *string {
	open, close := "<"+tag+">", "</"+tag+">"
	at := strings.Index(text, open)
	if at < 0 {
		return nil
	}
	end := strings.Index(text[at+len(open):], close)
	if end < 0 {
		return nil
	}
	value := protocol.TrimJavaScriptString(text[at+len(open) : at+len(open)+end])
	if value == "" {
		return nil
	}
	return &value
}

func remoteAgentReview(log []protocol.JSONObject, taggedOnly bool) *string {
	for _, hook := range []bool{true, false} {
		for i := len(log) - 1; i >= 0; i-- {
			event := log[i]
			var text string
			if hook && event.String("type") == "system" && (event.String("subtype") == "hook_progress" || event.String("subtype") == "hook_response") {
				text = event.String("stdout")
			} else if !hook && event.String("type") == "assistant" {
				text = remoteAgentText(event)
			}
			if tagged := remoteAgentTag(text, "remote-review"); tagged != nil {
				return tagged
			}
		}
	}
	hooks, texts := []string{}, []string{}
	for _, event := range log {
		if event.String("type") == "system" && (event.String("subtype") == "hook_progress" || event.String("subtype") == "hook_response") {
			hooks = append(hooks, event.String("stdout"))
		}
		if event.String("type") == "assistant" {
			texts = append(texts, remoteAgentText(event))
		}
	}
	if tagged := remoteAgentTag(strings.Join(hooks, ""), "remote-review"); tagged != nil {
		return tagged
	}
	if taggedOnly {
		return nil
	}
	text := protocol.TrimJavaScriptString(strings.Join(texts, "\n"))
	if text == "" {
		return nil
	}
	return &text
}

func remoteAgentTodos(log []protocol.JSONObject) json.RawMessage {
	definition, err := toolschema.NativeBuiltinDefinition("TodoWrite")
	if err != nil {
		return rawMain([]any{})
	}
	for i := len(log) - 1; i >= 0; i-- {
		if log[i].String("type") != "assistant" {
			continue
		}
		message, _ := log[i].Object("message")
		var blocks []protocol.JSONObject
		_ = json.Unmarshal(message.Get("content"), &blocks)
		for _, block := range blocks {
			if block.String("type") == "tool_use" && block.String("name") == "TodoWrite" {
				parsed, err := toolschema.ParseInputJSON(definition, block.Get("input"))
				if err != nil {
					return rawMain([]any{})
				}
				return parsed.Get("todos")
			}
		}
	}
	return rawMain([]any{})
}

func remoteAgentNotification(task protocol.JSONObject, status, path string) string {
	word := "completed successfully"
	if status == "failed" {
		word = "failed"
	} else if status == "killed" {
		word = "was stopped"
	}
	tool := ""
	if task.String("toolUseId") != "" {
		tool = "\n<tool-use-id>" + task.String("toolUseId") + "</tool-use-id>"
	}
	return "<task-notification>\n<task-id>" + task.String("id") + "</task-id>" + tool + "\n<task-type>remote_agent</task-type>\n<output-file>" + path + "</output-file>\n<status>" + status + "</status>\n<summary>Remote task \"" + task.String("title") + "\" " + word + "</summary>\n</task-notification>"
}

func remoteAgentReviewNotification(task protocol.JSONObject, review *string, reason string) string {
	head := "<task-notification>\n<task-id>" + task.String("id") + "</task-id>\n<task-type>remote_agent</task-type>\n<status>"
	if review != nil {
		return head + "completed</status>\n<summary>Remote review completed</summary>\n</task-notification>\nThe remote review produced the following findings:\n\n" + *review
	}
	return head + "failed</status>\n<summary>Remote review failed: " + reason + "</summary>\n</task-notification>\nRemote review did not produce output (" + reason + "). Tell the user to retry /ultrareview, or use /review for a local review instead."
}

func (poll *nativeRemoteAgentPoll) consume(task protocol.JSONObject, response ccrsessions.TaskPoll, now float64) nativeRemoteAgentPollResult {
	poll.last = append(json.RawMessage(nil), response.LastEventID...)
	grew := len(response.Events) != 0
	parts := []string{}
	if grew {
		poll.log = append(poll.log, response.Events...)
		for _, event := range response.Events {
			if event.String("type") == "assistant" {
				parts = append(parts, remoteAgentText(event))
			} else {
				parts = append(parts, string(protocol.StringifyValidatedJavaScriptJSON(rawMain(event))))
			}
		}
	}
	delta := strings.Join(parts, "\n")
	if delta != "" {
		delta += "\n"
	}
	out := nativeRemoteAgentPollResult{task: task.Clone(), delta: delta}
	if string(response.SessionStatus) == `"archived"` {
		out.terminal = true
		if task.String("status") == "running" {
			out.task.Set("status", quoteMain("completed"))
			out.task.Set("endTime", rawMain(now))
		}
		out.notification = remoteAgentNotification(task, "completed", task.String("outputFile"))
		return out
	}
	// A stop racing the HTTP result may still append its real delta. It cannot
	// overwrite the terminal task, enqueue twice or resurrect the poller.
	if task.String("status") != "running" {
		out.terminal = true
		return out
	}
	var result *protocol.JSONObject
	if !nativeSettingsTruthy(task.Get("isUltraplan")) && !nativeSettingsTruthy(task.Get("isLongRunning")) {
		for i := len(poll.log) - 1; i >= 0; i-- {
			if poll.log[i].String("type") == "result" {
				found := poll.log[i]
				result = &found
				break
			}
		}
	}
	review := nativeSettingsTruthy(task.Get("isRemoteReview"))
	if review && grew && poll.review == nil {
		poll.review = remoteAgentReview(response.Events, true)
	}
	var progress json.RawMessage
	if review && grew {
		for _, event := range response.Events {
			if event.String("type") != "system" || event.String("subtype") != "hook_progress" && event.String("subtype") != "hook_response" {
				continue
			}
			text := event.String("stdout")
			end := strings.LastIndex(text, "</remote-review-progress>")
			if end < 0 {
				continue
			}
			start := strings.LastIndex(text[:end], "<remote-review-progress>")
			if start < 0 {
				continue
			}
			var value protocol.JSONObject
			if value.UnmarshalJSON([]byte(text[start+len("<remote-review-progress>"):end])) != nil || !value.IsObject() {
				continue
			}
			p := protocol.NewJSONObject()
			if v := value.Get("stage"); v != nil {
				p.Set("stage", v)
			}
			for _, names := range [][2]string{{"bugsFound", "bugs_found"}, {"bugsVerified", "bugs_verified"}, {"bugsRefuted", "bugs_refuted"}} {
				v := value.Get(names[1])
				if v == nil || string(v) == "null" {
					v = rawMain(0)
				}
				p.Set(names[0], v)
			}
			progress = rawMain(p)
		}
	}
	hasOutput, assistant, hook := false, false, false
	for _, event := range poll.log {
		if event.String("type") == "assistant" {
			assistant = true
			hasOutput = true
		}
		if event.String("type") == "system" {
			sub := event.String("subtype")
			if review && (sub == "hook_progress" || sub == "hook_response") {
				hasOutput = true
			}
			if (sub == "hook_started" || sub == "hook_progress" || sub == "hook_response") && event.String("hook_event") == "SessionStart" {
				hook = true
			}
		}
	}
	if string(response.SessionStatus) == `"idle"` && !grew && hasOutput {
		poll.idle++
	} else {
		poll.idle = 0
	}
	done := review && (poll.review != nil || !hook && poll.idle >= 5 && assistant)
	var started float64
	_ = json.Unmarshal(task.Get("pollStartedAt"), &started)
	timedOut := review && now-started > 30*60*1000
	out.terminal = result != nil || done || timedOut
	status := "running"
	if out.terminal {
		status = "completed"
		if result != nil && result.String("subtype") != "success" {
			status = "failed"
		}
	}
	if grew || out.terminal {
		out.task.Set("status", quoteMain(status))
		out.task.Set("log", rawMain(poll.log))
		if grew {
			out.task.Set("todoList", remoteAgentTodos(poll.log))
		}
		if progress != nil {
			out.task.Set("reviewProgress", progress)
		}
		if out.terminal {
			out.task.Set("endTime", rawMain(now))
		} else {
			out.task.Delete("endTime")
		}
	}
	if !out.terminal {
		return out
	}
	if review {
		content := poll.review
		if content == nil {
			content = remoteAgentReview(poll.log, false)
		}
		if content != nil && status == "completed" {
			out.notification = remoteAgentReviewNotification(task, content, "")
		} else {
			reason := "no review output — orchestrator may have exited early"
			if result != nil && result.String("subtype") != "success" {
				reason = "remote session returned an error"
			} else if timedOut && !done {
				reason = "remote session exceeded 30 minutes"
			}
			out.task.Set("status", quoteMain("failed"))
			out.notification = remoteAgentReviewNotification(task, nil, reason)
		}
	} else {
		out.notification = remoteAgentNotification(task, status, task.String("outputFile"))
	}
	return out
}
