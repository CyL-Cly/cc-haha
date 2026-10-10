package toolsearch

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestActualSourceAutoThresholdMemoNamesOrderModelAndNullFallback(t *testing.T) {
	rows := []map[string]any{}
	add := func(name, model string, count float64, countError bool, prompt string, mode string) {
		rows = append(rows, map[string]any{"mode": mode, "model": model, "count": count, "countError": countError, "tools": []map[string]any{{"name": "ToolSearch"}, {"name": name, "isMcp": true, "prompt": prompt, "input_schema": map[string]any{"type": "object", "properties": map[string]any{}}}}})
	}
	add("mcp__equal__read", "fixture-model", 20500, false, "small", "auto")
	add("mcp__equal__read", "fixture-model[1m]", 1000000, false, "changed ignored by token memo", "auto")
	add("mcp__below__read", "fixture-model", 20499, false, "small", "auto")
	add("mcp__zeroAdjusted__read", "fixture-model", 1, false, strings.Repeat("large", 20000), "auto")
	add("mcp__rawZero__read", "fixture-model", 0, false, strings.Repeat("文𐐀", 17000), "auto")
	add("mcp__rawZero__read", "fixture-model", 999999, false, "now short", "auto")
	add("mcp__failed__read", "fixture-model", 0, true, strings.Repeat("文𐐀", 17000), "auto")
	add("mcp__failed__read", "fixture-model", 999999, false, "now short", "auto")
	add("mcp__negative__read", "fixture-model", -1, false, strings.Repeat("large", 20000), "auto")
	add("mcp__percent__read", "fixture-model", 2500, false, "small", "auto:1tail")
	add("mcp__not_called__read", "fixture-model", 0, false, "small", "auto:0")
	add("mcp__not_called__read", "fixture-model", 0, false, "small", "auto:100")
	ordered := []map[string]any{{"name": "ToolSearch"}, {"name": "mcp__z__read", "isMcp": true, "prompt": "z"}, {"name": "mcp__a__read", "isMcp": true, "prompt": "a"}}
	rows = append(rows, map[string]any{"mode": "auto", "model": "fixture-model", "count": 20500, "tools": ordered})
	rows = append(rows, map[string]any{"mode": "auto", "model": "fixture-model", "count": 20499, "tools": []map[string]any{ordered[0], ordered[2], ordered[1]}})
	rows = append(rows, map[string]any{"mode": "auto", "model": "fixture-model", "count": 0, "tools": []map[string]any{{"name": "ToolSearch"}, {"name": "Read"}}})
	for _, mode := range []string{"auto:invalid", "auto:99suffix"} {
		add("mcp__edge__"+mode, "fixture-model", 30000, false, "small", mode)
	}
	original := source(t, map[string]any{"autoPlans": rows})
	var observations []protocolAutoPlan
	if e := json.Unmarshal(original.Get("autoPlans"), &observations); e != nil || len(observations) != len(rows) {
		t.Fatal(e, len(observations))
	}
	service := New()
	countTrace := []map[string]any{}
	promptTrace := []string{}
	for i, row := range rows {
		tools := []Tool{}
		for _, tool := range row["tools"].([]map[string]any) {
			d := tool
			tools = append(tools, Tool{Name: quoted(d["name"].(string)), IsMCP: d["isMcp"] == true, Prompt: func(context.Context) (json.RawMessage, error) {
				promptTrace = append(promptTrace, d["name"].(string))
				p, _ := d["prompt"].(string)
				return quoted(p), nil
			}, Schema: func(context.Context) (json.RawMessage, error) {
				if value := d["input_schema"]; value != nil {
					return raw(value), nil
				}
				return raw(map[string]any{"type": "object", "properties": map[string]any{}}), nil
			}})
		}
		plan, e := service.Plan(context.Background(), row["model"].(string), tools, nil, false, GateInputs{Environment: map[string]string{"ENABLE_TOOL_SEARCH": row["mode"].(string)}, ContextWindow: func(context.Context, string) (float64, error) { return observations[i].Window, nil }, CountDefinitionTokens: func(_ context.Context, deferred []Tool, model string) (*float64, error) {
			names := []string{}
			for _, tool := range deferred {
				names = append(names, string(utf16.Decode(units(tool.Name))))
			}
			countTrace = append(countTrace, map[string]any{"names": names, "model": model})
			if row["countError"] == true {
				return nil, errors.New("physical API failure fixture")
			}
			var n float64
			_ = json.Unmarshal(raw(row["count"]), &n)
			return &n, nil
		}})
		if e != nil {
			t.Fatal(i, e)
		}
		if plan.Enabled != observations[i].Enabled || !equalJSON(raw(countTrace), observations[i].CountTrace) || !reflect.DeepEqual(promptTrace, observations[i].PromptTrace) {
			t.Errorf("case%d Go=%v/%s/%v Source=%v/%s/%v", i, plan.Enabled, raw(countTrace), promptTrace, observations[i].Enabled, observations[i].CountTrace, observations[i].PromptTrace)
		}
	}
}

type protocolAutoPlan struct {
	Enabled     bool
	Window      float64
	CountTrace  json.RawMessage
	PromptTrace []string
}
