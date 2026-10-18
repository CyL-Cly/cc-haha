package powershellpolicy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestActualSourcePowerShellCompoundApprovalPrecedenceAndPathDecisions(t *testing.T) {
	executable := physicalPowerShell(t)
	if _, err := os.Stat(executable); err != nil {
		t.Skip("real native PowerShell AST host required")
	}
	// Both actual processes must observe the same canonical cwd namespace on
	// macOS, where /var aliases /private/var. Keep path containment unchanged.
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(home, "work")
	if err := os.MkdirAll(filepath.Join(work, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, ".git", "HEAD"), []byte("ref: refs/heads/private\n"), 0600); err != nil {
		t.Fatal(err)
	}
	contexts := []map[string]any{
		{"mode": "default"}, {"mode": "acceptEdits"},
		{"mode": "default", "alwaysAskRules": map[string][]string{"localSettings": {"PowerShell(Get-Process:*)"}}, "alwaysDenyRules": map[string][]string{"localSettings": {"PowerShell(Invoke-Expression:*)", "PowerShell(Stop-Process:*)"}}},
		{"mode": "default", "alwaysAllowRules": map[string][]string{"localSettings": {"PowerShell(Get-Process:*)", "PowerShell(Write-Output:*)", "PowerShell(Stop-Process:*)"}}},
	}
	commands := []string{"", `Get-Process`, `Stop-Process private`, `Get-Process; Stop-Process private`, `Get-Process; Invoke-Expression private`, `Get-Process | Stop-Process private`, `Write-Output $env:PRIVATE`, `Where-Object {$true} | Sort-Object {$env:PRIVATE='private'}`,
		`Get-Content private.txt`, `Set-Content private.txt literal`, `Remove-Item /`, `if($true){Remove-Item /}`, `Get-Process > private.txt`, `Get-Process > $null`, `Set-Location .; git status`, `Set-Content hooks/pre-commit literal; git status`, `Set-Content .git/config literal`, `tar -xf private.tar; git status`,
		`$env:PRIVATE`, `if($true){Get-Process; $env:PRIVATE}`, `using module private.psm1`, `Set-Location .; Get-Content private.txt`, `New-Item -ItemType SymbolicLink -Path private; Get-Content private.txt`,
	}
	rows := []map[string]any{}
	for _, command := range commands {
		for _, context := range contexts {
			rows = append(rows, map[string]any{"input": map[string]any{"command": command, "timeout": 777}, "context": context})
		}
	}
	var source struct {
		Evaluations []struct {
			Parsed Parsed
			Result json.RawMessage
		}
	}
	sourcePolicy(t, map[string]any{"home": home, "cwd": work, "executable": executable, "evaluations": rows}, &source)
	if len(source.Evaluations) != len(rows) {
		t.Fatal("Source decision corpus incomplete")
	}
	classifier, err := NewClassifier("darwin", "")
	if err != nil {
		t.Fatal(err)
	}
	for i, row := range rows {
		mode := row["context"].(map[string]any)["mode"].(string)
		input, _ := json.Marshal(row["input"])
		rules := []Rule{}
		for _, behavior := range []string{"deny", "ask", "allow"} {
			key := "always" + strings.ToUpper(behavior[:1]) + behavior[1:] + "Rules"
			if groups, ok := row["context"].(map[string]any)[key].(map[string][]string); ok {
				for source, contents := range groups {
					for _, content := range contents {
						if strings.HasPrefix(content, "PowerShell(") && strings.HasSuffix(content, ")") {
							rules = append(rules, Rule{Source: source, RuleBehavior: behavior, RuleValue: RuleValue{ToolName: "PowerShell", RuleContent: content[len("PowerShell(") : len(content)-1]}})
						}
					}
				}
			}
		}
		paths := PathContext{Platform: "darwin", CWD: work, Home: home, Mode: mode, WorkingDirectories: []string{work}, Resolve: SafeResolvePath, Deny: func(string, string) (*Rule, error) { return nil, nil }, Check: func(resolved, operation string, canonical bool) (PathCheck, error) {
			targets := []string{resolved}
			if !canonical {
				real, _, _ := ownedCanonical(resolved)
				targets = append(targets, real)
			}
			inside := true
			for _, target := range targets {
				rel, e := filepath.Rel(work, target)
				inside = inside && e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
			}
			// This corpus leaves filesystem rules/internal paths empty. Actual
			// path-safety service is exercised separately by the production hook.
			return PathCheck{Allowed: inside && (operation == "read" || mode == "acceptEdits")}, nil
		}}
		result, e := classifier.Evaluate(input, source.Evaluations[i].Parsed, EvaluationContext{Mode: mode, Rules: rules, Paths: paths})
		if e != nil {
			t.Fatal(e)
		}
		sameJSON(t, "approval "+row["input"].(map[string]any)["command"].(string)+"/"+mode, result, source.Evaluations[i].Result)
	}
}
