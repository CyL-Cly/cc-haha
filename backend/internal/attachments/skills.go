// Package attachments projects retained original attachment payloads into
// model-visible users. Discovery, skill registry state and compact producers
// are separate host capabilities, not substitutes for persisted payloads.
package attachments

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/ultracode"
)

type CapabilityError struct{ Boundary string }

func (failure *CapabilityError) Error() string {
	return "attachment capability is not migrated: " + failure.Boundary
}

// MalformedAttachmentError is the actual supported renderer's thrown error.
// Request normalization catches this type; it must not catch capability or
// infrastructure errors and silently discard unsupported model-visible data.
type MalformedAttachmentError struct {
	Class       string
	MessageJSON json.RawMessage
}

func (failure *MalformedAttachmentError) Error() string {
	var message string
	_ = json.Unmarshal(failure.MessageJSON, &message)
	return message
}

func malformed(message string) error {
	raw := protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(message)))
	return &MalformedAttachmentError{Class: "TypeError", MessageJSON: raw}
}

func quoted(value string) json.RawMessage { raw, _ := json.Marshal(value); return raw }
func field(name string, raw json.RawMessage) protocol.JSONField {
	return protocol.JSONField{Name: name, Value: raw}
}
func object(raw json.RawMessage) (protocol.JSONObject, error) {
	var value protocol.JSONObject
	err := json.Unmarshal(raw, &value)
	return value, err
}

func truthy(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) || bytes.Equal(raw, []byte("false")) {
		return false
	}
	if raw[0] == '"' {
		return len(raw) > 2
	}
	if raw[0] == '{' || raw[0] == '[' || raw[0] == 't' {
		return true
	}
	number, _ := strconv.ParseFloat(string(raw), 64)
	return number != 0 && !math.IsNaN(number)
}

func exactString(raw json.RawMessage) (json.RawMessage, error) {
	value, err := protocol.ExactJavaScriptStringJSON(raw)
	var typeError *protocol.ExactJSONStringTypeError
	if errors.As(err, &typeError) {
		return nil, malformed(typeError.Message)
	}
	return value, err
}

// Concatenate quoted UTF-16 values without replacement decoding or another
// coercion pass. All fields were independently converted before allocation.
func concatenate(parts ...json.RawMessage) (json.RawMessage, error) {
	units := []uint16{}
	for _, raw := range parts {
		value, err := protocol.DecodeJavaScriptJSONString(raw)
		if err != nil {
			return nil, err
		}
		units = append(units, value...)
	}
	return protocol.QuoteJavaScriptUTF16(units), nil
}

const mapError = "attachment.skills.map is not a function. (In 'attachment.skills.map((skill) => `### Skill: ${skill.name}\nPath: ${skill.path}\n\n${skill.content}`)', 'attachment.skills.map' is undefined)"

func skillsContent(raw json.RawMessage) (json.RawMessage, bool, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, false, malformed("undefined is not an object (evaluating 'attachment.skills.length')")
	}
	if bytes.Equal(raw, []byte("null")) {
		return nil, false, malformed("null is not an object (evaluating 'attachment.skills.length')")
	}
	if raw[0] != '[' {
		// The exported renderer tests .length before .map. These malformed
		// collections are outside resume validation but have real Source behavior.
		if bytes.Equal(raw, []byte(`""`)) {
			return nil, true, nil
		}
		if raw[0] == '{' {
			value, _ := object(raw) // Project already validated the entire JSON object.
			length := bytes.TrimSpace(value.Get("length"))
			if len(length) > 0 && (length[0] == '-' || length[0] >= '0' && length[0] <= '9') {
				number, _ := strconv.ParseFloat(string(length), 64)
				if number == 0 {
					return nil, true, nil
				}
			}
			if value.Get("map") != nil {
				return nil, false, &CapabilityError{Boundary: "nonarray skills with own map"}
			}
		}
		return nil, false, malformed(mapError)
	}
	var skills []json.RawMessage
	_ = json.Unmarshal(raw, &skills) // Validated original array token, retained as RawMessage.
	if len(skills) == 0 {
		return nil, true, nil
	}
	parts := []json.RawMessage{}
	for index, skill := range skills {
		if bytes.Equal(bytes.TrimSpace(skill), []byte("null")) {
			return nil, false, malformed("null is not an object (evaluating 'skill.name')")
		}
		value := protocol.JSONObject{}
		if bytes.TrimSpace(skill)[0] == '{' {
			value, _ = object(skill) // Nested token shares Project's parser/depth validation.
		}
		if index > 0 {
			parts = append(parts, quoted("\n\n---\n\n"))
		}
		for _, part := range []struct{ literal, property string }{{"### Skill: ", "name"}, {"\nPath: ", "path"}, {"\n\n", "content"}} {
			converted, err := exactString(value.Get(part.property))
			if err != nil {
				return nil, false, err
			}
			parts = append(parts, quoted(part.literal), converted)
		}
	}
	value, err := concatenate(parts...)
	return value, false, err
}

// Project accepts the original raw attachment, never a rendered text/schema
// or a restored dedup map. Only these retained kinds are migrated.
func Project(payload json.RawMessage, factory *messages.Factory) ([]protocol.JSONObject, error) {
	return ProjectWithOptions(payload, factory, nil)
}

func ProjectWithOptions(payload json.RawMessage, factory *messages.Factory, plan *PlanOptions) ([]protocol.JSONObject, error) {
	value, err := object(payload)
	if err != nil || !value.IsObject() {
		return nil, &CapabilityError{Boundary: "nonobject or invalid payload"}
	}
	if Invisible(payload) {
		return []protocol.JSONObject{}, nil
	}
	var content json.RawMessage
	var prefix string
	switch value.String("type") {
	case "workflow_keyword_request":
		content = quoted(ultracode.KeywordReminder)
	case "ultra_effort_enter":
		text := ultracode.StillOnReminder
		if value.String("reminderType") == "full" {
			text = ultracode.EnterReminder
		}
		content = quoted(text)
	case "ultra_effort_exit":
		content = quoted(ultracode.ExitReminder)
	case "async_hook_response":
		return projectAsyncHook(value, factory)
	case "teammate_mailbox":
		return projectTeammateMailbox(value, factory)
	case "hook_success":
		if string(value.Get("content")) == `""` {
			return []protocol.JSONObject{}, nil
		}
		name, e := exactString(value.Get("hookName"))
		if e != nil {
			return nil, e
		}
		text, e := exactString(value.Get("content"))
		if e != nil {
			return nil, e
		}
		content, err = concatenate(name, quoted(" hook success: "), text)
		if err != nil {
			return nil, err
		}
	case "hook_additional_context":
		text, empty, e := hookAdditionalContent(value.Get("content"))
		if e != nil {
			return nil, e
		}
		if empty {
			return []protocol.JSONObject{}, nil
		}
		name, e := exactString(value.Get("hookName"))
		if e != nil {
			return nil, e
		}
		content, err = concatenate(name, quoted(" hook additional context: "), text)
		if err != nil {
			return nil, err
		}
	case "hook_blocking_error":
		blocked, ok := value.Object("blockingError")
		if !ok {
			return nil, malformed("undefined is not an object (evaluating 'attachment.blockingError.command')")
		}
		name, e := protocol.JavaScriptStringJSON(value.Get("hookName"))
		if e != nil {
			return nil, e
		}
		command, e := protocol.JavaScriptStringJSON(blocked.Get("command"))
		if e != nil {
			return nil, e
		}
		errorText, e := protocol.JavaScriptStringJSON(blocked.Get("blockingError"))
		if e != nil {
			return nil, e
		}
		content, err = concatenate(name, quoted(" hook blocking error from command: \""), command, quoted("\": "), errorText)
		if err != nil {
			return nil, err
		}
	case "hook_stopped_continuation":
		name, e := protocol.JavaScriptStringJSON(value.Get("hookName"))
		if e != nil {
			return nil, e
		}
		message, e := protocol.JavaScriptStringJSON(value.Get("message"))
		if e != nil {
			return nil, e
		}
		content, err = concatenate(name, quoted(" hook stopped continuation: "), message)
		if err != nil {
			return nil, err
		}
	case "queued_command":
		return projectQueued(value, factory)
	case "deferred_tools_delta":
		content, err = projectDeferredToolsDelta(value)
		if err != nil {
			return nil, err
		}
		if string(content) == `""` {
			content = quoted("(no content)")
		}
	case "mcp_resource":
		return projectSkillResource(value, factory)
	case "file", "directory", "agent_mention":
		return projectSkillFile(value, factory, plan)
	case "plan_mode", "plan_mode_reentry", "plan_mode_exit":
		if plan == nil {
			return nil, &CapabilityError{Boundary: "plan attachment without owning runtime options"}
		}
		return ProjectPlan(payload, factory, *plan)
	case "auto_mode":
		content = quoted(autoModeFull)
		if value.String("reminderType") == "sparse" {
			content = quoted(autoModeSparse)
		}
	case "auto_mode_exit":
		content = quoted("## Exited Auto Mode\n\nYou have exited auto mode. The user may now want to interact more directly. You should ask clarifying questions when the approach is ambiguous rather than making assumptions.")
	case "compact_file_reference", "plan_file_reference", "task_status":
		content, err = projectCompactAttachment(value)
		if err != nil {
			return nil, err
		}
	case "invoked_skills":
		var empty bool
		content, empty, err = skillsContent(value.Get("skills"))
		if err != nil {
			return nil, err
		}
		if empty {
			return []protocol.JSONObject{}, nil
		}
		prefix = "The following skills were invoked in this session. Continue to follow these guidelines:\n\n"
	case "skill_listing":
		if !truthy(value.Get("content")) {
			return []protocol.JSONObject{}, nil
		}
		content, err = exactString(value.Get("content"))
		if err != nil {
			return nil, err
		}
		prefix = "The following skills are available for use with the Skill tool:\n\n"
	default:
		return nil, &CapabilityError{Boundary: "kind " + value.String("type")}
	}
	if factory == nil {
		return nil, errors.New("retained attachment requires a message factory")
	}
	wrapped, err := concatenate(quoted("<system-reminder>\n"+prefix), content, quoted("\n</system-reminder>"))
	if err != nil {
		return nil, err
	}
	created := factory.CreateUser(storage.Object{"content": wrapped, "isMeta": json.RawMessage("true")})
	// The shared factory preserves raw fields; restore its Source enumeration
	// here rather than inheriting Go map iteration order.
	api := protocol.NewJSONObject(field("role", quoted("user")), field("content", wrapped))
	apiRaw, err := api.MarshalJSON()
	if err != nil {
		return nil, err
	}
	result := protocol.NewJSONObject(field("type", created["type"]), field("message", apiRaw), field("isMeta", created["isMeta"]), field("uuid", created["uuid"]), field("timestamp", created["timestamp"]))
	return []protocol.JSONObject{result}, nil
}

// MergeIntoUser is attachment-specific. Generic adjacent-user merging has
// different LF/UUID semantics and must stay with requestcontext's user branch.
func MergeIntoUser(left, right protocol.JSONObject) (protocol.JSONObject, error) {
	decode := func(value protocol.JSONObject) (storage.Object, error) {
		raw, err := value.MarshalJSON()
		if err != nil {
			return nil, err
		}
		var result storage.Object
		err = json.Unmarshal(raw, &result)
		return result, err
	}
	a, err := decode(left)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	b, err := decode(right)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	merged, err := messages.MergeUserMessagesAndToolResults(a, b, false)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	api, err := object(merged["message"])
	if err != nil {
		return protocol.JSONObject{}, err
	}
	retained, err := object(left.Get("message"))
	if err != nil {
		return protocol.JSONObject{}, err
	}
	retained.Set("content", api.Get("content"))
	raw, err := retained.MarshalJSON()
	if err != nil {
		return protocol.JSONObject{}, fmt.Errorf("attachment merged message: %w", err)
	}
	result := left.Clone()
	result.Set("message", raw)
	return result, nil
}
