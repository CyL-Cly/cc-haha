package attachments

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func projectCompactAttachment(value protocol.JSONObject) (json.RawMessage, error) {
	text := func(name string) (json.RawMessage, error) { return protocol.JavaScriptStringJSON(value.Get(name)) }
	join := func(parts ...json.RawMessage) (json.RawMessage, error) { return concatenate(parts...) }
	switch value.String("type") {
	case "compact_file_reference":
		filename, e := text("filename")
		if e != nil {
			return nil, e
		}
		return join(quoted("Note: "), filename, quoted(" was read before the last conversation was summarized, but the contents are too large to include. Use Read tool if you need to access it."))
	case "plan_file_reference":
		path, e := text("planFilePath")
		if e != nil {
			return nil, e
		}
		content, e := text("planContent")
		if e != nil {
			return nil, e
		}
		return join(quoted("A plan file exists from plan mode at: "), path, quoted("\n\nPlan contents:\n\n"), content, quoted("\n\nIf this plan is relevant to the current work and not already complete, continue working on it."))
	case "task_status":
		description, e := text("description")
		if e != nil {
			return nil, e
		}
		id, e := text("taskId")
		if e != nil {
			return nil, e
		}
		if value.String("status") == "killed" {
			return join(quoted("Task \""), description, quoted("\" ("), id, quoted(") was stopped by the user."))
		}
		parts := []json.RawMessage{}
		if value.String("status") == "running" {
			first, e := join(quoted("Background agent \""), description, quoted("\" ("), id, quoted(") is still running."))
			if e != nil {
				return nil, e
			}
			parts = append(parts, first)
			if truthy(value.Get("deltaSummary")) {
				delta, e := text("deltaSummary")
				if e != nil {
					return nil, e
				}
				v, e := join(quoted("Progress: "), delta)
				if e != nil {
					return nil, e
				}
				parts = append(parts, quoted(" "), v)
			}
			if truthy(value.Get("outputFilePath")) {
				path, e := text("outputFilePath")
				if e != nil {
					return nil, e
				}
				v, e := join(quoted("Do NOT spawn a duplicate. You will be notified when it completes. You can read partial output at "), path, quoted(" or send it a message with SendMessage."))
				if e != nil {
					return nil, e
				}
				parts = append(parts, quoted(" "), v)
			} else {
				parts = append(parts, quoted(" Do NOT spawn a duplicate. You will be notified when it completes. You can check its progress with the TaskOutput tool or send it a message with SendMessage."))
			}
		} else {
			kind, e := text("taskType")
			if e != nil {
				return nil, e
			}
			status, e := text("status")
			if e != nil {
				return nil, e
			}
			v, e := join(quoted("Task "), id, quoted(" (type: "), kind, quoted(") (status: "), status, quoted(") (description: "), description, quoted(")"))
			if e != nil {
				return nil, e
			}
			parts = append(parts, v)
			if truthy(value.Get("deltaSummary")) {
				delta, e := text("deltaSummary")
				if e != nil {
					return nil, e
				}
				v, e := join(quoted(" Delta: "), delta)
				if e != nil {
					return nil, e
				}
				parts = append(parts, v)
			}
			if truthy(value.Get("outputFilePath")) {
				path, e := text("outputFilePath")
				if e != nil {
					return nil, e
				}
				v, e := join(quoted(" Read the output file to retrieve the result: "), path)
				if e != nil {
					return nil, e
				}
				parts = append(parts, v)
			} else {
				parts = append(parts, quoted(" You can check its output using the TaskOutput tool."))
			}
		}
		return join(parts...)
	}
	return nil, &CapabilityError{Boundary: "compact attachment " + value.String("type")}
}
