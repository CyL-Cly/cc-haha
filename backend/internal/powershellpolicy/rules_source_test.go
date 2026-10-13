package powershellpolicy

import (
	"encoding/json"
	"testing"
)

func TestActualSourcePowerShellPermissionRuleMatching(t *testing.T) {
	pairs := [][2]string{
		{"Remove-Item:*", "rm private"}, {"rm:*", "Remove-Item\tprivate"}, {"Module\\Remove-Item:*", "rm private"}, {"ModuleA\\Get-Thing:*", "ModuleB\\Get-Thing private"}, {"ModuleA\\Get-Thing:*", "ModuleA\\Get-Thing private"},
		{"Get-Process *", "Get-Process"}, {"* run *", "npm run"}, {"Get-Process private", "Get-Process\tprivate"}, {"rm private", "Remove-Item\tprivate"},
		{`Get-Process \*`, `Get-Process *`}, {`Get-Process \\*`, `Get-Process \private`}, {"Get-Process *", "Get-Process\nprivate"}, {"Get-Process:*\n", "Get-Process private"}, {"Get-Process\n:*", "Get-Process private"},
		{"Get-Process s*", "Get-Process ſ"}, {"Get-Process K*", "Get-Process K"}, {"Get-Process İ*", "Get-Process i\u0307"}, {"Get-Process Σ", "Get-Process ς"},
		{"Get-Process 中*", "Get-Process 中文😀"}, {"Get-Process Σ*", "Get-Process ς"}, {"Get-Process 𐐀*", "Get-Process 𐐨"}, {"Get-Process 😀*", "Get-Process 😀中文"},
	}
	rows := []map[string]any{}
	for _, pair := range pairs {
		for _, behavior := range []string{"deny", "ask", "allow"} {
			for _, matchMode := range []string{"exact", "prefix"} {
				rows = append(rows, map[string]any{"command": pair[1], "content": pair[0], "behavior": behavior, "matchMode": matchMode, "rule": Rule{Source: "localSettings", RuleBehavior: behavior, RuleValue: RuleValue{ToolName: "PowerShell", RuleContent: pair[0]}}})
			}
		}
	}
	var source struct {
		RuleRows []struct {
			Parsed  json.RawMessage
			Matched []Rule
		}
	}
	sourcePolicy(t, map[string]any{"ruleRows": rows}, &source)
	if len(rows) != len(source.RuleRows) {
		t.Fatal("Source rule corpus incomplete")
	}
	for i, row := range rows {
		content := row["content"].(string)
		sameJSON(t, "rule parse "+content, ParseRule(content), source.RuleRows[i].Parsed)
		matched := MatchingRules(row["command"].(string), []Rule{row["rule"].(Rule)}, row["matchMode"].(string), row["behavior"].(string))
		expected, _ := json.Marshal(source.RuleRows[i].Matched)
		sameJSON(t, "rule match "+content+"/"+row["command"].(string)+"/"+row["behavior"].(string), matched, expected)
	}
}
