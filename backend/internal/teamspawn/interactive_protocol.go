package teamspawn

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
)

// ParseInteractiveProtocol preserves teammateMailbox's distinct schemas. The
// request type probes are deliberately permissive; responses are strict and
// z.object request/plan schemas strip unknown fields.
func ParseInteractiveProtocol(text string) (string, protocol.JSONObject) {
	p := inboxObject([]byte(text))
	kind := p.String("type")
	strings := func(keys ...string) bool {
		for _, key := range keys {
			if _, e := protocol.DecodeJavaScriptJSONString(p.Get(key)); e != nil {
				return false
			}
		}
		return true
	}
	optionalStrings := func(keys ...string) bool {
		for _, key := range keys {
			if p.Get(key) != nil && !strings(key) {
				return false
			}
		}
		return true
	}
	strict := func(object protocol.JSONObject, allowed ...string) bool {
		for _, field := range object.OwnProperties() {
			var key string
			_ = json.Unmarshal(field.NameJSON, &key)
			found := false
			for _, value := range allowed {
				if key == value {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	}
	strip := func(keys ...string) protocol.JSONObject {
		out := protocol.NewJSONObject()
		for _, key := range keys {
			if p.Get(key) != nil {
				out.Set(key, p.Get(key))
			}
		}
		return out
	}
	valid := false
	switch kind {
	case "permission_request", "sandbox_permission_request":
		valid = p.IsObject()
	case "shutdown_approved":
		valid = ShutdownApproved(text) != nil
	case "permission_response":
		if strings("request_id") && p.String("request_id") != "" {
			if p.String("subtype") == "error" {
				valid = strings("error") && strict(p, "type", "request_id", "subtype", "error")
			} else if p.String("subtype") == "success" && strict(p, "type", "request_id", "subtype", "response") {
				valid = true
				if p.Get("response") != nil {
					r, ok := p.Object("response")
					valid = ok && strict(r, "updated_input", "permission_updates")
					if r.Get("updated_input") != nil {
						input, ok := r.Object("updated_input")
						valid = valid && ok && input.IsObject()
					}
					if r.Get("permission_updates") != nil {
						updates := sdkpermissions.ParsePermissionUpdates(r.Get("permission_updates"))
						valid = valid && updates != nil
						if valid {
							r.Set("permission_updates", rawJSON(updates))
							p.Set("response", rawJSON(r))
						}
					}
				}
			}
		}
	case "sandbox_permission_response":
		valid = strings("requestId", "host", "timestamp") && p.String("requestId") != "" && p.String("host") != "" && (string(p.Get("allow")) == "true" || string(p.Get("allow")) == "false") && strict(p, "type", "requestId", "host", "allow", "timestamp")
	case "team_permission_update":
		u, ok := p.Object("permissionUpdate")
		updates := sdkpermissions.ParsePermissionUpdates(rawJSON([]protocol.JSONObject{u}))
		valid = ok && len(updates) == 1 && u.String("type") == "addRules" && u.String("destination") == "session" && strings("directoryPath", "toolName") && strict(p, "type", "permissionUpdate", "directoryPath", "toolName") && strict(u, "type", "rules", "behavior", "destination")
		if valid {
			p.Set("permissionUpdate", rawJSON(updates[0]))
		}
	case "plan_approval_request":
		valid = strings("from", "timestamp", "planFilePath", "planContent", "requestId")
		p = strip("type", "from", "timestamp", "planFilePath", "planContent", "requestId")
	case "shutdown_request":
		valid = strings("requestId", "from", "timestamp") && optionalStrings("reason")
		p = strip("type", "requestId", "from", "reason", "timestamp")
	case "plan_approval_response":
		valid = strings("requestId", "timestamp") && optionalStrings("feedback") && (string(p.Get("approved")) == "true" || string(p.Get("approved")) == "false")
		if p.Get("permissionMode") != nil {
			valid = valid && interactiveMode(p.String("permissionMode"))
		}
		p = strip("type", "requestId", "approved", "feedback", "timestamp", "permissionMode")
	case "mode_set_request":
		valid = strings("from") && interactiveMode(p.String("mode"))
		p = strip("type", "mode", "from")
	}
	if !valid {
		return "", protocol.JSONObject{}
	}
	return kind, p
}

func interactiveMode(mode string) bool {
	switch mode {
	case "default", "acceptEdits", "bypassPermissions", "plan", "dontAsk":
		return true
	}
	return false
}

// StructuredProtocolMessage is the attachment consumer's type-only exclusion,
// even when a response fails its stricter routing schema.
func StructuredProtocolMessage(text string) bool {
	switch inboxObject([]byte(text)).String("type") {
	case "permission_request", "permission_response", "sandbox_permission_request", "sandbox_permission_response", "shutdown_request", "shutdown_approved", "team_permission_update", "mode_set_request", "plan_approval_request", "plan_approval_response":
		return true
	}
	return false
}

func FormatInteractiveTeammateMessages(messages []protocol.JSONObject) (json.RawMessage, error) {
	rows := make([]json.RawMessage, 0, len(messages))
	for _, message := range messages {
		pieces := []json.RawMessage{quoted(`<teammate-message teammate_id="`)}
		from, err := protocol.ExactJavaScriptStringJSON(message.Get("from"))
		if err != nil {
			return nil, err
		}
		pieces = append(pieces, from, quoted(`"`))
		for _, key := range []string{"color", "summary"} {
			if leaderTruthy(message.Get(key)) {
				v, err := protocol.ExactJavaScriptStringJSON(message.Get(key))
				if err != nil {
					return nil, err
				}
				pieces = append(pieces, quoted(" "+key+`="`), v, quoted(`"`))
			}
		}
		content, err := protocol.ExactJavaScriptStringJSON(message.Get("text"))
		if err != nil {
			return nil, err
		}
		pieces = append(pieces, quoted(">\n"), content, quoted("\n</teammate-message>"))
		row, err := protocol.JoinJavaScriptValuesJSON(pieces, "")
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return protocol.JoinJavaScriptValuesJSON(rows, "\n\n")
}

func TeammateMailboxAttachment(file, pending []protocol.JSONObject) protocol.JSONObject {
	all := []protocol.JSONObject{}
	seen := map[string]bool{}
	for _, source := range [][]protocol.JSONObject{file, pending} {
		for _, row := range source {
			units, _ := protocol.DecodeJavaScriptJSONString(row.Get("text"))
			if len(units) > 100 {
				units = units[:100]
			}
			key := string(row.Get("from")) + "|" + string(row.Get("timestamp")) + "|" + string(protocol.QuoteJavaScriptUTF16(units))
			if seen[key] {
				continue
			}
			seen[key] = true
			out := protocol.NewJSONObject()
			for _, name := range []string{"from", "text", "timestamp", "color", "summary"} {
				if row.Get(name) != nil {
					out.Set(name, row.Get(name))
				}
			}
			all = append(all, out)
		}
	}
	latest := map[string]int{}
	idle := map[int]string{}
	for i, row := range all {
		body := inboxObject([]byte(row.String("text")))
		if body.String("type") == "idle_notification" {
			agent := string(body.Get("from"))
			idle[i] = agent
			latest[agent] = i
		}
	}
	filtered := []protocol.JSONObject{}
	for i, row := range all {
		if agent, ok := idle[i]; ok && latest[agent] != i {
			continue
		}
		filtered = append(filtered, row)
	}
	if len(filtered) == 0 {
		return protocol.JSONObject{}
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("teammate_mailbox")}, protocol.JSONField{Name: "messages", Value: rawJSON(filtered)})
}
