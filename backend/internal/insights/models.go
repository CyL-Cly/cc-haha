package insights

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type ModelRequest struct {
	Prompt    json.RawMessage
	MaxTokens float64
}
type ModelResponse struct {
	Text json.RawMessage
	Err  error
}
type ModelOptions struct {
	Stats      StatsOptions
	QueryBatch func(context.Context, []ModelRequest) ([]ModelResponse, error)
	LogError   func(error)
}
type Models struct{ options ModelOptions }

func NewModels(o ModelOptions) (*Models, error) {
	if o.QueryBatch == nil || o.Stats.ParseDate == nil || o.LogError == nil {
		return nil, errors.New("Insights requires actual model batch, date, and diagnostics producers")
	}
	return &Models{o}, nil
}
func jsText(v json.RawMessage) string {
	if v == nil {
		return "undefined"
	}
	value, err := protocol.JavaScriptStringJSON(v)
	if err != nil {
		return "undefined"
	}
	var text string
	_ = json.Unmarshal(value, &text)
	return text
}
func joinRaw(values ...json.RawMessage) json.RawMessage {
	result, err := protocol.JoinJavaScriptValuesJSON(values, "")
	if err != nil {
		panic(err)
	}
	return result
}
func pretty(v any) string {
	var b bytes.Buffer
	_ = json.Indent(&b, raw(v), "", "  ")
	return b.String()
}
func (m *Models) transcript(log Log) (json.RawMessage, error) {
	meta, err := LogMeta(log, m.options.Stats)
	if err != nil {
		return nil, err
	}
	lines := []json.RawMessage{joinRaw(raw("Session: "), sliceString(meta.Get("session_id"), 8)), raw("Date: " + jsText(meta.Get("start_time"))), raw("Project: " + jsText(meta.Get("project_path"))), raw("Duration: " + jsText(meta.Get("duration_minutes")) + " min"), raw("")}
	for _, record := range log.Messages {
		body, ok := record.Object("message")
		if !ok {
			continue
		}
		content := body.Get("content")
		if record.String("type") == "user" && len(content) > 0 && content[0] == '"' {
			lines = append(lines, joinRaw(raw("[User]: "), sliceString(content, 500)))
			continue
		}
		blocks, e := contentBlocks(content)
		if e != nil {
			return nil, e
		}
		for _, block := range blocks {
			if record.String("type") == "user" && block.String("type") == "text" && block.Get("text") != nil {
				if _, e = requiredString(block.Get("text")); e != nil {
					return nil, e
				}
				lines = append(lines, joinRaw(raw("[User]: "), sliceString(block.Get("text"), 500)))
			}
			if record.String("type") == "assistant" {
				if block.String("type") == "text" && block.Get("text") != nil {
					if _, e = requiredString(block.Get("text")); e != nil {
						return nil, e
					}
					lines = append(lines, joinRaw(raw("[Assistant]: "), sliceString(block.Get("text"), 300)))
				} else if block.String("type") == "tool_use" && block.Get("name") != nil {
					lines = append(lines, joinRaw(raw("[Tool: "), block.Get("name"), raw("]")))
				}
			}
		}
	}
	return protocol.JoinJavaScriptValuesJSON(lines, "\n")
}
func (m *Models) Transcript(ctx context.Context, log Log) (json.RawMessage, error) {
	full, err := m.transcript(log)
	if err != nil {
		return nil, err
	}
	units, err := protocol.DecodeJavaScriptJSONString(full)
	if err != nil {
		return nil, err
	}
	if len(units) <= 30000 {
		return full, nil
	}
	chunks := []json.RawMessage{}
	requests := []ModelRequest{}
	for i := 0; i < len(units); i += 25000 {
		end := min(i+25000, len(units))
		chunk := protocol.QuoteJavaScriptUTF16(units[i:end])
		chunks = append(chunks, chunk)
		requests = append(requests, ModelRequest{joinRaw(raw(assets.Summary), chunk), 500})
	}
	responses, err := m.options.QueryBatch(ctx, requests)
	if err != nil {
		responses = make([]ModelResponse, len(chunks))
	} else if len(responses) != len(chunks) {
		return nil, errors.New("Insights model batch response count mismatch")
	}
	summaries := make([]json.RawMessage, len(chunks))
	for i, response := range responses {
		if response.Err == nil && truthy(response.Text) {
			summaries[i] = response.Text
		} else {
			summaries[i] = sliceString(chunks[i], 2000)
		}
	}
	meta, err := LogMeta(log, m.options.Stats)
	if err != nil {
		return nil, err
	}
	header := joinRaw(raw("Session: "), sliceString(meta.Get("session_id"), 8), raw("\nDate: "+jsText(meta.Get("start_time"))+"\nProject: "+jsText(meta.Get("project_path"))+"\nDuration: "+jsText(meta.Get("duration_minutes"))+" min\n"+fmt.Sprintf("[Long session - %d parts summarized]\n", len(chunks))))
	body, err := protocol.JoinJavaScriptValuesJSON(summaries, "\n\n---\n\n")
	if err != nil {
		return nil, err
	}
	return joinRaw(header, body), nil
}

const facetSchema = `

RESPOND WITH ONLY A VALID JSON OBJECT matching this schema:
{
  "underlying_goal": "What the user fundamentally wanted to achieve",
  "goal_categories": {"category_name": count, ...},
  "outcome": "fully_achieved|mostly_achieved|partially_achieved|not_achieved|unclear_from_transcript",
  "user_satisfaction_counts": {"level": count, ...},
  "claude_helpfulness": "unhelpful|slightly_helpful|moderately_helpful|very_helpful|essential",
  "session_type": "single_task|multi_task|iterative_refinement|exploration|quick_question",
  "friction_counts": {"friction_type": count, ...},
  "friction_detail": "One sentence describing friction or empty",
  "primary_success": "none|fast_accurate_search|correct_code_edits|good_explanations|proactive_help|multi_file_changes|good_debugging",
  "brief_summary": "One sentence: what user wanted and whether they got it"
}`

func braceJSON(text json.RawMessage) json.RawMessage {
	units, err := protocol.DecodeJavaScriptJSONString(text)
	if err != nil {
		return nil
	}
	start, end := -1, -1
	for i, u := range units {
		if u == '{' && start < 0 {
			start = i
		}
		if u == '}' {
			end = i
		}
	}
	if start < 0 || end < start {
		return nil
	}
	value, err := protocol.ParseAndStringifyJavaScriptJSON([]byte(protocol.JavaScriptJSONSourceUTF16(units[start : end+1])))
	if err != nil {
		return nil
	}
	return value
}
func (m *Models) Facets(ctx context.Context, logs []Log) ([]*protocol.JSONObject, error) {
	requests := []ModelRequest{}
	indices := []int{}
	out := make([]*protocol.JSONObject, len(logs))
	for i, log := range logs {
		transcript, err := m.Transcript(ctx, log)
		if err != nil {
			m.options.LogError(fmt.Errorf("Facet extraction failed: %w", err))
			continue
		}
		requests = append(requests, ModelRequest{joinRaw(raw(assets.Facet), transcript, raw(facetSchema)), 4096})
		indices = append(indices, i)
	}
	responses, err := m.options.QueryBatch(ctx, requests)
	if err != nil {
		return out, err
	}
	if len(responses) != len(requests) {
		return nil, errors.New("Insights model batch response count mismatch")
	}
	for i, response := range responses {
		if response.Err != nil {
			m.options.LogError(fmt.Errorf("Facet extraction failed: %w", response.Err))
			continue
		}
		parsed := braceJSON(response.Text)
		if !ValidFacets(parsed) {
			continue
		}
		facet, _ := object(parsed)
		facet.Set("session_id", raw(logs[indices[i]].SessionID))
		out[indices[i]] = &facet
	}
	return out, nil
}
func top(value json.RawMessage) [][]json.RawMessage {
	rows, _ := entries(value)
	sort.SliceStable(rows, func(i, j int) bool { return jsNumber(rows[i].Value) > jsNumber(rows[j].Value) })
	if len(rows) > 8 {
		rows = rows[:8]
	}
	out := [][]json.RawMessage{}
	for _, row := range rows {
		out = append(out, []json.RawMessage{row.NameJSON, row.Value})
	}
	return out
}
func narrativeContext(data protocol.JSONObject, facets []protocol.JSONObject) string {
	context := protocol.NewJSONObject()
	for _, pair := range [][2]string{{"sessions", "total_sessions"}, {"analyzed", "sessions_with_facets"}, {"date_range", "date_range"}, {"messages", "total_messages"}} {
		context.Set(pair[0], data.Get(pair[1]))
	}
	context.Set("hours", number(math.Floor(jsNumber(data.Get("total_duration_hours"))+0.5)))
	context.Set("commits", data.Get("git_commits"))
	context.Set("top_tools", raw(top(data.Get("tool_counts"))))
	context.Set("top_goals", raw(top(data.Get("goal_categories"))))
	for _, name := range []string{"outcomes", "satisfaction", "friction", "success", "languages"} {
		context.Set(name, data.Get(name))
	}
	summaries, frictions, instructions := []string{}, []string{}, []string{}
	for i, facet := range facets {
		if i < 50 {
			summaries = append(summaries, "- "+jsText(facet.Get("brief_summary"))+" ("+jsText(facet.Get("outcome"))+", "+jsText(facet.Get("claude_helpfulness"))+")")
		}
		if truthy(facet.Get("friction_detail")) && len(frictions) < 20 {
			frictions = append(frictions, "- "+jsText(facet.Get("friction_detail")))
		}
		values := facet.Get("user_instructions_to_claude")
		if !truthy(values) {
			continue
		}
		var list []json.RawMessage
		if len(values) > 0 && values[0] == '[' {
			_ = json.Unmarshal(values, &list)
		} else {
			list = []json.RawMessage{values}
		}
		for _, value := range list {
			if len(instructions) < 15 {
				instructions = append(instructions, "- "+jsText(value))
			}
		}
	}
	instruction := strings.Join(instructions, "\n")
	if instruction == "" {
		instruction = "None captured"
	}
	return pretty(context) + "\n\nSESSION SUMMARIES:\n" + strings.Join(summaries, "\n") + "\n\nFRICTION DETAILS:\n" + strings.Join(frictions, "\n") + "\n\nUSER INSTRUCTIONS TO CLAUDE:\n" + instruction
}
func summaryRows(insights protocol.JSONObject, sectionName, arrayName, left, right string) (string, error) {
	section, ok := insights.Object(sectionName)
	if !ok {
		return "", nil
	}
	value := section.Get(arrayName)
	if !truthy(value) {
		return "", nil
	}
	var rows []protocol.JSONObject
	if json.Unmarshal(value, &rows) != nil {
		return "", errors.New("Insights section list is not an array")
	}
	lines := []string{}
	for _, row := range rows {
		if !row.IsObject() {
			return "", errors.New("Insights section item is null")
		}
		lines = append(lines, "- "+jsText(row.Get(left))+": "+jsText(row.Get(right)))
	}
	return strings.Join(lines, "\n"), nil
}
func (m *Models) Narrative(ctx context.Context, data protocol.JSONObject, facets []protocol.JSONObject) (protocol.JSONObject, error) {
	full := narrativeContext(data, facets)
	requests := []ModelRequest{}
	for _, section := range assets.Sections {
		requests = append(requests, ModelRequest{raw(section.Prompt + "\n\nDATA:\n" + full), section.MaxTokens})
	}
	responses, err := m.options.QueryBatch(ctx, requests)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if len(responses) != len(requests) {
		return protocol.JSONObject{}, errors.New("Insights model batch response count mismatch")
	}
	out := protocol.NewJSONObject()
	for i, response := range responses {
		if response.Err != nil {
			m.options.LogError(fmt.Errorf("%s failed: %w", assets.Sections[i].Name, response.Err))
			continue
		}
		value := braceJSON(response.Text)
		if truthy(value) {
			out.Set(assets.Sections[i].Name, value)
		}
	}
	texts := []string{full}
	for _, row := range [][4]string{{"project_areas", "areas", "name", "description"}, {"what_works", "impressive_workflows", "title", "description"}, {"friction_analysis", "categories", "category", "description"}, {"suggestions", "features_to_try", "feature", "one_liner"}, {"suggestions", "usage_patterns", "title", "suggestion"}, {"on_the_horizon", "opportunities", "title", "whats_possible"}} {
		text, e := summaryRows(out, row[0], row[1], row[2], row[3])
		if e != nil {
			return protocol.JSONObject{}, e
		}
		texts = append(texts, text)
	}
	glance := interpolate(assets.Glance, texts...) + "\n\nDATA:\n"
	last, err := m.options.QueryBatch(ctx, []ModelRequest{{raw(glance), 8192}})
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if len(last) != 1 {
		return protocol.JSONObject{}, errors.New("Insights model batch response count mismatch")
	}
	if last[0].Err != nil {
		m.options.LogError(fmt.Errorf("at_a_glance failed: %w", last[0].Err))
	} else if value := braceJSON(last[0].Text); truthy(value) {
		out.Set("at_a_glance", value)
	}
	return out, nil
}
