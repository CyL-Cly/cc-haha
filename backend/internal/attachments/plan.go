package attachments

import (
	_ "embed"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

type PlanOptions struct {
	// MapToolResult borrows the actual owning tool formatter. Retained file
	// attachments must use that formatter rather than another tool runtime.
	MapToolResult                                 func(string, json.RawMessage, json.RawMessage) (protocol.JSONObject, error)
	Environment                                   map[string]string
	SubscriptionType, RateLimitTier               string
	InterviewPhase                                bool
	Phase4Variant                                 string
	EmbeddedSearchTools, ExplorePlanAgentsEnabled bool
	ProjectAllowedTools                           []string
}

//go:embed assets/plan-full.md
var planFull string

//go:embed assets/plan-multi-agent.md
var planMultiAgent string

//go:embed assets/plan-interview.md
var planInterview string

//go:embed assets/plan-sparse.md
var planSparse string

//go:embed assets/plan-subagent.md
var planSubagent string

//go:embed assets/plan-reentry.md
var planReentry string

//go:embed assets/plan-exit.md
var planExit string

//go:embed assets/plan-phase4-control.md
var planPhase4Control string

//go:embed assets/plan-phase4-trim.md
var planPhase4Trim string

//go:embed assets/plan-phase4-cut.md
var planPhase4Cut string

//go:embed assets/plan-phase4-cap.md
var planPhase4Cap string

func fillPlanTemplate(template string, values map[string]json.RawMessage) (json.RawMessage, error) {
	parts := []json.RawMessage{}
	for {
		start := strings.Index(template, "__")
		if start < 0 {
			parts = append(parts, quoted(template))
			break
		}
		end := strings.Index(template[start+2:], "__")
		if end < 0 {
			return nil, errors.New("invalid authored plan template token")
		}
		end += start + 4
		parts = append(parts, quoted(template[:start]))
		key := template[start:end]
		value, exists := values[key]
		if !exists {
			return nil, errors.New("missing authored plan template value " + key)
		}
		parts = append(parts, value)
		template = template[end:]
	}
	return concatenate(parts...)
}
func planEnvCount(value string, fallback int) int {
	value = strings.TrimLeftFunc(value, func(r rune) bool {
		return strings.ContainsRune("\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff", r)
	})
	negative := false
	if strings.HasPrefix(value, "+") {
		value = value[1:]
	} else if strings.HasPrefix(value, "-") {
		negative = true
		value = value[1:]
	}
	count, digits := 0, 0
	for _, r := range value {
		if r < '0' || r > '9' {
			break
		}
		digits++
		if count <= 10 {
			count = count*10 + int(r-'0')
		}
	}
	if digits > 0 && !negative && count > 0 && count <= 10 {
		return count
	}
	return fallback
}
func planCount(options PlanOptions) int {
	fallback := 1
	if options.SubscriptionType == "team" || options.SubscriptionType == "enterprise" || options.SubscriptionType == "max" && options.RateLimitTier == "default_claude_max_20x" {
		fallback = 3
	}
	return planEnvCount(options.Environment["CLAUDE_CODE_PLAN_V2_AGENT_COUNT"], fallback)
}
func planReadOnlyTools(options PlanOptions) string {
	tools := []string{"Read", "Glob", "Grep"}
	if options.EmbeddedSearchTools {
		return "Read, `find`, `grep`"
	}
	if len(options.ProjectAllowedTools) > 0 {
		filtered := []string{}
		for _, tool := range tools {
			for _, allowed := range options.ProjectAllowedTools {
				if tool == allowed {
					filtered = append(filtered, tool)
					break
				}
			}
		}
		tools = filtered
	}
	return strings.Join(tools, ", ")
}

func ProjectPlan(payload json.RawMessage, factory *messages.Factory, options PlanOptions) ([]protocol.JSONObject, error) {
	value, e := object(payload)
	if e != nil || !value.IsObject() {
		return nil, &CapabilityError{Boundary: "nonobject or invalid plan payload"}
	}
	path, e := protocol.JavaScriptStringJSON(value.Get("planFilePath"))
	if e != nil {
		return nil, e
	}
	values := map[string]json.RawMessage{"__PATH__": path}
	info := func(sub bool) (json.RawMessage, error) {
		if truthy(value.Get("planExists")) {
			suffix := ". You can read it and make incremental edits using the Edit tool."
			if sub {
				suffix = ". You can read it and make incremental edits using the Edit tool if you need to."
			}
			return concatenate(quoted("A plan file already exists at "), path, quoted(suffix))
		}
		suffix := " using the Write tool."
		if sub {
			suffix = " using the Write tool if you need to."
		}
		return concatenate(quoted("No plan file exists yet. You should create your plan at "), path, quoted(suffix))
	}
	template := ""
	switch value.String("type") {
	case "plan_mode_reentry":
		template = planReentry
	case "plan_mode_exit":
		reference := quoted("")
		if truthy(value.Get("planExists")) {
			reference, e = concatenate(quoted(" The plan file is located at "), path, quoted(" if you need to reference it."))
			if e != nil {
				return nil, e
			}
		}
		values["__PLAN_REFERENCE__"] = reference
		template = planExit
	case "plan_mode":
		sub := truthy(value.Get("isSubAgent"))
		values["__PLAN_INFO__"], e = info(sub)
		if e != nil {
			return nil, e
		}
		if sub {
			template = planSubagent
		} else if value.String("reminderType") == "sparse" {
			template = planSparse
			workflow := "Follow 5-phase workflow."
			if options.InterviewPhase {
				workflow = "Follow iterative workflow: explore codebase, interview user, write to plan incrementally."
			}
			values["__WORKFLOW__"] = quoted(workflow)
		} else if options.InterviewPhase {
			template = planInterview
			values["__READ_TOOLS__"] = quoted(planReadOnlyTools(options))
			hint := ""
			if options.ExplorePlanAgentsEnabled {
				hint = " You can use the Explore agent type to parallelize complex searches without filling your context, though for straightforward queries direct tools are simpler."
			}
			values["__EXPLORE_HINT__"] = quoted(hint)
		} else {
			template = planFull
			count := planCount(options)
			values["__AGENT_COUNT__"] = quoted(strconv.Itoa(count))
			values["__EXPLORE_COUNT__"] = quoted(strconv.Itoa(planEnvCount(options.Environment["CLAUDE_CODE_PLAN_V2_EXPLORE_AGENT_COUNT"], 3)))
			guidance := quoted("")
			if count > 1 {
				guidance, e = fillPlanTemplate(planMultiAgent, values)
				if e != nil {
					return nil, e
				}
			}
			values["__MULTI_AGENT_GUIDANCE__"] = guidance
			phase := planPhase4Control
			switch options.Phase4Variant {
			case "trim":
				phase = planPhase4Trim
			case "cut":
				phase = planPhase4Cut
			case "cap":
				phase = planPhase4Cap
			}
			values["__PHASE4__"] = quoted(phase)
		}
	default:
		return nil, &CapabilityError{Boundary: "kind " + value.String("type")}
	}
	content, e := fillPlanTemplate(template, values)
	if e != nil {
		return nil, e
	}
	if factory == nil {
		return nil, errors.New("plan attachment requires the shared message factory")
	}
	wrapped, e := concatenate(quoted("<system-reminder>\n"), content, quoted("\n</system-reminder>"))
	if e != nil {
		return nil, e
	}
	created := factory.CreateUser(storage.Object{"content": wrapped, "isMeta": json.RawMessage("true")})
	api := protocol.NewJSONObject(field("role", quoted("user")), field("content", wrapped))
	apiRaw, e := api.MarshalJSON()
	if e != nil {
		return nil, e
	}
	result := protocol.NewJSONObject(field("type", created["type"]), field("message", apiRaw), field("isMeta", created["isMeta"]), field("uuid", created["uuid"]), field("timestamp", created["timestamp"]))
	return []protocol.JSONObject{result}, nil
}
