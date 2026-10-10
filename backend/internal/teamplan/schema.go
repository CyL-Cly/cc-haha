// Package teamplan owns the original durable human-review plan and its launch
// authorization. A persisted team config alone is never execution approval.
package teamplan

import (
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"unicode"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Error struct {
	Message string
	Status  int
}

func (e *Error) Error() string                 { return e.Message }
func failure(message string, status int) error { return &Error{message, status} }
func raw(v any) json.RawMessage                { b, _ := json.Marshal(v); return b }
func object(data json.RawMessage) protocol.JSONObject {
	var v protocol.JSONObject
	_ = v.UnmarshalJSON(data)
	return v
}

type property struct {
	name   string
	schema node
}
type node struct {
	kind                                                  string
	optional, minString, integer, positive, strict, strip bool
	values                                                []string
	fields                                                []property
	item                                                  *node
	literal                                               json.RawMessage
	pattern                                               string
	alternatives                                          []node
}

func text(optional, min bool) node { return node{kind: "string", optional: optional, minString: min} }
func number(integer, positive bool) node {
	return node{kind: "number", integer: integer, positive: positive}
}
func enum(values ...string) node         { return node{kind: "enum", values: values} }
func list(item node, optional bool) node { return node{kind: "array", item: &item, optional: optional} }
func record(item node, optional bool) node {
	return node{kind: "record", item: &item, optional: optional}
}
func obj(fields ...property) node { return node{kind: "object", fields: fields} }
func optional(n node) node        { n.optional = true; return n }
func runtimeSchema() node {
	return obj(property{"providerId", text(false, true)}, property{"modelId", text(false, true)}, property{"effortLevel", text(true, false)})
}
func snapshotSchema() node {
	identity := node{kind: "union", optional: true, alternatives: []node{obj(property{"kind", node{kind: "literal", literal: raw("file")}}, property{"path", text(false, true)}, property{"sha256", node{kind: "string", pattern: `^[a-f0-9]{64}$`}}), obj(property{"kind", node{kind: "literal", literal: raw("builtin")}}), obj(property{"kind", node{kind: "literal", literal: raw("session")}})}}
	return obj(property{"systemPrompt", text(false, false)}, property{"tools", list(text(false, false), true)}, property{"disallowedTools", list(text(false, false), true)}, property{"effortLevel", text(true, false)}, property{"agentType", text(true, false)}, property{"source", text(true, false)}, property{"sourceIdentity", identity}, property{"description", text(true, false)}, property{"model", text(true, false)}, property{"permissionMode", text(true, false)}, property{"skills", list(text(false, false), true)}, property{"memory", text(true, false)}, property{"hooks", node{kind: "any", optional: true}}, property{"maxTurns", optional(number(false, false))}, property{"omitClaudeMd", node{kind: "boolean", optional: true}}, property{"mcpServers", node{kind: "any", optional: true}}, property{"initialPrompt", text(true, false)}, property{"isolation", text(true, false)}, property{"configurationError", text(true, false)})
}
func memberSchema() node {
	return obj(property{"id", text(false, true)}, property{"name", text(false, true)}, property{"agentType", text(false, true)}, property{"prompt", text(false, true)}, property{"runtime", runtimeSchema()}, property{"suggestedRuntime", optional(runtimeSchema())}, property{"reason", text(true, false)}, property{"difficulty", optional(enum("low", "medium", "high"))}, property{"agentSnapshot", optional(snapshotSchema())})
}
func taskSchema() node {
	return obj(property{"id", text(false, true)}, property{"subject", text(false, true)}, property{"description", text(true, false)}, property{"ownerId", text(true, false)}, property{"dependencies", list(text(false, false), false)})
}
func planSchema() node {
	return obj(property{"schemaVersion", node{kind: "literal", literal: raw(1)}}, property{"planId", text(false, true)}, property{"sessionId", text(false, true)}, property{"teamName", text(false, true)}, property{"incarnationId", text(false, true)}, property{"revision", number(true, true)}, property{"state", enum("draft", "review_pending", "launching", "running", "launch_failed", "cancelled", "interrupted")}, property{"workDir", text(false, true)}, property{"leaderRuntime", runtimeSchema()}, property{"agentCatalog", record(snapshotSchema(), true)}, property{"members", list(memberSchema(), false)}, property{"tasks", list(taskSchema(), false)}, property{"createdAt", number(false, false)}, property{"updatedAt", number(false, false)}, property{"parentPlanId", text(true, false)}, property{"feedback", text(true, false)}, property{"approvedSnapshot", optional(obj(property{"revision", number(true, true)}, property{"members", list(memberSchema(), false)}, property{"tasks", list(taskSchema(), false)}, property{"leaderRuntime", runtimeSchema()}, property{"approvedAt", number(false, false)}, property{"requestId", text(false, true)}))}, property{"launch", optional(obj(property{"status", enum("pending", "running", "failed")}, property{"error", text(true, false)}, property{"memberIds", record(text(false, false), true)}))})
}
func (n node) parse(data json.RawMessage) (json.RawMessage, bool) {
	if len(data) == 0 {
		return nil, n.optional
	}
	switch n.kind {
	case "any":
		return data, true
	case "string":
		u, e := protocol.DecodeJavaScriptJSONString(data)
		if e != nil || n.minString && len(u) == 0 {
			return nil, false
		}
		if n.pattern != "" && !regexp.MustCompile(n.pattern).MatchString(object(raw(map[string]json.RawMessage{"s": data})).String("s")) {
			return nil, false
		}
		return data, true
	case "boolean":
		return data, string(data) == "true" || string(data) == "false"
	case "literal":
		return data, string(protocol.StringifyValidatedJavaScriptJSON(data)) == string(n.literal)
	case "number":
		if string(data) == "null" {
			return nil, false
		}
		var f float64
		if json.Unmarshal(data, &f) != nil || math.IsInf(f, 0) || math.IsNaN(f) || n.positive && f <= 0 || n.integer && (math.Trunc(f) != f || math.Abs(f) > 9007199254740991) {
			return nil, false
		}
		return data, true
	case "enum":
		v := object(raw(map[string]json.RawMessage{"s": data}))
		if _, e := protocol.DecodeJavaScriptJSONString(data); e != nil {
			return nil, false
		}
		for _, expected := range n.values {
			if v.String("s") == expected {
				return data, true
			}
		}
		return nil, false
	case "array":
		var values []json.RawMessage
		if len(data) == 0 || data[0] != '[' || json.Unmarshal(data, &values) != nil {
			return nil, false
		}
		out := []json.RawMessage{}
		for _, v := range values {
			v, ok := n.item.parse(v)
			if !ok {
				return nil, false
			}
			out = append(out, v)
		}
		return raw(out), true
	case "record":
		v := object(data)
		if !v.IsObject() {
			return nil, false
		}
		out := protocol.NewJSONObject()
		for _, p := range v.OwnProperties() {
			parsed, ok := n.item.parse(p.Value)
			if !ok {
				return nil, false
			}
			_ = out.SetPropertyJSON(p.NameJSON, parsed)
		}
		return raw(out), true
	case "object":
		v := object(data)
		if !v.IsObject() {
			return nil, false
		}
		out := protocol.NewJSONObject()
		known := map[string]bool{}
		for _, f := range n.fields {
			known[f.name] = true
			parsed, ok := f.schema.parse(v.Get(f.name))
			if !ok {
				return nil, false
			}
			if parsed != nil {
				out.Set(f.name, parsed)
			}
		}
		for _, p := range v.OwnProperties() {
			var key string
			_ = json.Unmarshal(p.NameJSON, &key)
			if known[key] {
				continue
			}
			if n.strict {
				return nil, false
			}
			if n.strip {
				continue
			}
			_ = out.SetPropertyJSON(p.NameJSON, p.Value)
		}
		return raw(out), true
	case "union":
		for _, alternative := range n.alternatives {
			if out, ok := alternative.parse(data); ok {
				return out, true
			}
		}
		return nil, false
	}
	return nil, false
}
func ParseRecord(data json.RawMessage) (protocol.JSONObject, error) {
	normalized, e := protocol.ParseAndStringifyJavaScriptJSON(data)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	out, ok := planSchema().parse(normalized)
	if !ok {
		return protocol.JSONObject{}, failure("Unsupported or invalid persisted team plan", 409)
	}
	return object(out), nil
}
func ValidMemberName(name string) bool {
	if name == "" || name == "team-lead" {
		return false
	}
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '_' && r != '-' {
			return false
		}
	}
	return true
}
func ValidateGraph(plan protocol.JSONObject, runnable bool) error {
	var members, tasks []protocol.JSONObject
	if json.Unmarshal(plan.Get("members"), &members) != nil || json.Unmarshal(plan.Get("tasks"), &tasks) != nil {
		return errors.New("invalid plan arrays")
	}
	ids, names := map[string]bool{}, map[string]bool{}
	unique := true
	for _, m := range members {
		if !ValidMemberName(m.String("name")) {
			return failure("Invalid teammate name: "+m.String("name")+`. Use letters, numbers, underscores or hyphens; "team-lead" is reserved.`, 400)
		}
		if ids[m.String("id")] || names[m.String("name")] {
			unique = false
		}
		ids[m.String("id")] = true
		names[m.String("name")] = true
	}
	taskMap := map[string]protocol.JSONObject{}
	for _, task := range tasks {
		if _, exists := taskMap[task.String("id")]; exists {
			unique = false
		}
		taskMap[task.String("id")] = task
	}
	if !unique {
		return failure("Member names and IDs and task IDs must be unique", 400)
	}
	if runnable && (len(members) == 0 || len(tasks) == 0) {
		return failure("Plan needs members and assigned tasks", 400)
	}
	if runnable {
		for _, m := range members {
			assigned := false
			for _, task := range tasks {
				assigned = assigned || task.String("ownerId") == m.String("id")
			}
			if !assigned {
				return failure("Every member needs an assigned task", 400)
			}
		}
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if visited[id] {
			return nil
		}
		if visiting[id] {
			return failure("Task dependency cycle", 400)
		}
		task, ok := taskMap[id]
		if !ok {
			return failure("Unknown dependency: "+id, 400)
		}
		owner := task.String("ownerId")
		if runnable && owner == "" {
			return failure("Every task needs an assigned member", 400)
		}
		if owner != "" && !ids[owner] {
			return failure("Unknown task owner: "+owner, 400)
		}
		visiting[id] = true
		var dependencies []string
		_ = json.Unmarshal(task.Get("dependencies"), &dependencies)
		for _, dep := range dependencies {
			if e := visit(dep); e != nil {
				return e
			}
		}
		delete(visiting, id)
		visited[id] = true
		return nil
	}
	for _, task := range tasks {
		if e := visit(task.String("id")); e != nil {
			return e
		}
	}
	return nil
}
func ParseAction(data json.RawMessage) (protocol.JSONObject, error) {
	schema := obj(property{"sessionId", text(false, true)}, property{"planId", text(false, true)}, property{"incarnationId", text(false, true)}, property{"expectedRevision", number(true, true)}, property{"requestId", text(false, true)}, property{"feedback", text(true, false)})
	schema.strip = true
	out, ok := schema.parse(data)
	if !ok {
		return protocol.JSONObject{}, failure("Invalid team plan action", 400)
	}
	return object(out), nil
}
func ParsePatchRequest(data json.RawMessage) (protocol.JSONObject, error) {
	member := obj(property{"id", text(false, true)}, property{"agentType", text(true, true)}, property{"runtime", optional(runtimeSchema())})
	member.strict = true
	task := obj(property{"id", text(false, true)}, property{"ownerId", text(false, true)})
	task.strict = true
	schema := obj(property{"sessionId", text(false, true)}, property{"planId", text(false, true)}, property{"incarnationId", text(false, true)}, property{"expectedRevision", number(true, true)}, property{"members", list(member, true)}, property{"tasks", list(task, true)})
	schema.strict = true
	out, ok := schema.parse(data)
	if !ok {
		return protocol.JSONObject{}, failure("Invalid team plan update", 400)
	}
	return object(out), nil
}
