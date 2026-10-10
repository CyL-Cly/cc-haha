package insights

import (
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func entries(value json.RawMessage) ([]protocol.JSONProperty, error) {
	if len(value) == 0 || string(value) == "null" {
		return nil, errors.New("Cannot convert undefined or null to object")
	}
	if value[0] == '{' {
		o, err := object(value)
		if err != nil {
			return nil, err
		}
		return o.OwnProperties(), nil
	}
	if value[0] == '[' {
		var values []json.RawMessage
		if err := json.Unmarshal(value, &values); err != nil {
			return nil, err
		}
		out := []protocol.JSONProperty{}
		for i, v := range values {
			out = append(out, protocol.JSONProperty{NameJSON: raw(strconv.Itoa(i)), Value: v})
		}
		return out, nil
	}
	return []protocol.JSONProperty{}, nil
}
func count(object *protocol.JSONObject, name string, value json.RawMessage) {
	if name == "__proto__" {
		return
	}
	previous := object.Get(name)
	if previous == nil {
		if function, exists := objectPrototypeFunctions[name]; exists {
			previous = raw("function " + function + "() { [native code] }")
		}
	}
	object.Set(name, add(previous, value))
}
func copyCounts(target *protocol.JSONObject, value json.RawMessage, positive bool) error {
	if positive && !truthy(value) {
		return nil
	}
	rows, err := entries(value)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if positive && !(jsNumber(row.Value) > 0) {
			continue
		}
		var name string
		if err = json.Unmarshal(row.NameJSON, &name); err != nil {
			return err
		}
		count(target, name, row.Value)
	}
	return nil
}

// Aggregate preserves original ordering, branch counts and upper median. The
// source reports human messages, including interruption/meta text, not turns.
func Aggregate(sessions []protocol.JSONObject, facets map[string]protocol.JSONObject, parse ParseDate) (protocol.JSONObject, error) {
	result := protocol.NewJSONObject()
	for _, field := range []string{"total_sessions", "sessions_with_facets", "total_messages", "total_duration_hours", "total_input_tokens", "total_output_tokens", "git_commits", "git_pushes", "total_interruptions", "total_tool_errors", "median_response_time", "avg_response_time", "sessions_using_task_agent", "sessions_using_mcp", "sessions_using_web_search", "sessions_using_web_fetch", "total_lines_added", "total_lines_removed", "total_files_modified", "days_active", "messages_per_day"} {
		result.Set(field, raw(0))
	}
	result.Set("total_sessions", raw(len(sessions)))
	result.Set("sessions_with_facets", raw(len(facets)))
	dictionaries := map[string]protocol.JSONObject{}
	for _, name := range []string{"tool_counts", "languages", "projects", "goal_categories", "outcomes", "satisfaction", "helpfulness", "session_types", "friction", "success", "tool_error_categories"} {
		dictionaries[name] = protocol.NewJSONObject()
	}
	update := func(name string, key string, value json.RawMessage) {
		d := dictionaries[name]
		count(&d, key, value)
		dictionaries[name] = d
	}
	sums := map[string]jsSum{}
	dates := []string{}
	responseTimes := []float64{}
	hours := []json.RawMessage{}
	summaries := []protocol.JSONObject{}
	for _, session := range sessions {
		start := session.String("start_time")
		dates = append(dates, start)
		for _, mapping := range [][2]string{{"total_messages", "user_message_count"}, {"total_input_tokens", "input_tokens"}, {"total_output_tokens", "output_tokens"}, {"git_commits", "git_commits"}, {"git_pushes", "git_pushes"}, {"total_interruptions", "user_interruptions"}, {"total_tool_errors", "tool_errors"}, {"total_lines_added", "lines_added"}, {"total_lines_removed", "lines_removed"}, {"total_files_modified", "files_modified"}} {
			sums[mapping[0]] = sums[mapping[0]].plus(session.Get(mapping[1]))
		}
		sums["total_duration_hours"] = sums["total_duration_hours"].plusNumber(jsNumber(session.Get("duration_minutes")) / 60)
		for _, field := range []string{"tool_error_categories", "tool_counts", "languages"} {
			target := dictionaries[field]
			if err := copyCounts(&target, session.Get(field), false); err != nil {
				return protocol.JSONObject{}, err
			}
			dictionaries[field] = target
		}
		for _, feature := range []string{"task_agent", "mcp", "web_search", "web_fetch"} {
			if truthy(session.Get("uses_" + feature)) {
				sums["sessions_using_"+feature] = sums["sessions_using_"+feature].plus(raw(1))
			}
		}
		var responses []float64
		if err := json.Unmarshal(session.Get("user_response_times"), &responses); err != nil {
			return protocol.JSONObject{}, err
		}
		responseTimes = append(responseTimes, responses...)
		var dailyHours []json.RawMessage
		if err := json.Unmarshal(session.Get("message_hours"), &dailyHours); err != nil {
			return protocol.JSONObject{}, err
		}
		hours = append(hours, dailyHours...)
		if project := session.String("project_path"); project != "" {
			update("projects", project, raw(1))
		}
		facet, hasFacet := facets[session.String("session_id")]
		if hasFacet {
			for _, mapping := range [][2]string{{"goal_categories", "goal_categories"}, {"satisfaction", "user_satisfaction_counts"}, {"friction", "friction_counts"}} {
				target := dictionaries[mapping[0]]
				if err := copyCounts(&target, facet.Get(mapping[1]), true); err != nil {
					return protocol.JSONObject{}, err
				}
				dictionaries[mapping[0]] = target
			}
			for _, mapping := range [][2]string{{"outcomes", "outcome"}, {"helpfulness", "claude_helpfulness"}, {"session_types", "session_type"}} {
				key := facet.String(mapping[1])
				if facet.Get(mapping[1]) == nil {
					key = "undefined"
				}
				update(mapping[0], key, raw(1))
			}
			if key := facet.String("primary_success"); key != "none" {
				if facet.Get("primary_success") == nil {
					key = "undefined"
				}
				update("success", key, raw(1))
			}
		}
		if len(summaries) < 50 {
			summary := session.Get("summary")
			if !truthy(summary) {
				summary = sliceString(session.Get("first_prompt"), 100)
			}
			row := protocol.NewJSONObject(protocol.JSONField{Name: "id", Value: sliceString(session.Get("session_id"), 8)}, protocol.JSONField{Name: "date", Value: raw(strings.Split(start, "T")[0])}, protocol.JSONField{Name: "summary", Value: summary})
			if hasFacet {
				row.Set("goal", facet.Get("underlying_goal"))
			}
			summaries = append(summaries, row)
		}
	}
	for name, value := range sums {
		result.Set(name, value.json())
	}
	for name, value := range dictionaries {
		result.Set(name, raw(value))
	}
	sort.SliceStable(dates, func(i, j int) bool { return compare(dates[i], dates[j]) < 0 })
	start, end := "", ""
	if len(dates) > 0 {
		start = strings.Split(dates[0], "T")[0]
		end = strings.Split(dates[len(dates)-1], "T")[0]
	}
	result.Set("date_range", raw(protocol.NewJSONObject(protocol.JSONField{Name: "start", Value: raw(start)}, protocol.JSONField{Name: "end", Value: raw(end)})))
	result.Set("session_summaries", raw(summaries))
	result.Set("user_response_times", raw(responseTimes))
	result.Set("message_hours", raw(hours))
	if len(responseTimes) > 0 {
		sorted := append([]float64{}, responseTimes...)
		sort.Float64s(sorted)
		result.Set("median_response_time", number(sorted[len(sorted)/2]))
		sum := 0.0
		for _, v := range responseTimes {
			sum += v
		}
		result.Set("avg_response_time", number(sum/float64(len(responseTimes))))
	}
	days := map[string]bool{}
	for _, date := range dates {
		days[strings.Split(date, "T")[0]] = true
	}
	result.Set("days_active", raw(len(days)))
	if len(days) > 0 {
		result.Set("messages_per_day", number(math.Floor(sums["total_messages"].number()/float64(len(days))*10+0.5)/10))
	}
	overlaps, err := MultiClauding(sessions, parse)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	result.Set("multi_clauding", raw(overlaps))
	return result, nil
}
