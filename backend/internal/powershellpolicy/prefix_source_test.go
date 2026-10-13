package powershellpolicy

import (
	"encoding/json"
	"os"
	"testing"
)

func TestActualSourcePowerShellApprovalPrefixSafetyAndCompoundCollapse(t *testing.T) {
	executable := "/opt/homebrew/bin/pwsh"
	if _, err := os.Stat(executable); err != nil {
		t.Skip("physical PowerShell AST host required")
	}
	commands := []string{`Get-Process -Name private`, `Remove-Item private`, `Invoke-Expression private`, `Start-Process private`, `New-Object private`, `Write-Output private`, `Where-Object {$true}`, `ipconfig /flushdns`, `node private`, `Get-Process; Get-Service`, `Remove-Item one; Remove-Item two`, `Get-Process; Remove-Item one`, `git status`, `Git status`, `git push origin`, `git 'push origin'`, `git 'push origin' push origin`, `git -C '/my repo' status`, `git`, `git -c private`, `git status; git log`, `git push one; git push two`, `npm run test; npm run lint`, `Npm run test; npm run lint`, `kubectl get pods`, `kubectl get pods; kubectl delete pods`, `gcloud compute instances list`, `gcloud beta private one two three`, `aws s3 cp private other`, `docker run private`, `dotnet build private`, `rg private .`, `./private.exe one`, `& $private status`, `git $private`, `git $(Get-Process)`, `git "private $x"`, `Get-Process (`, `1`, `Get-Process && 1`, `timeout 2s echo private`, `timeout 2s 'echo private'`, `pyright --pythonversion 3.11`, `sleep 1`, `# private` + "\nRemove-Item private"}
	var source struct {
		PrefixRows []struct {
			Parsed                    Parsed
			First, Compound, Filtered json.RawMessage
		}
		BuiltInSpecs, NeverSuggest json.RawMessage
		PrefixSpecs                [][2]json.RawMessage
	}
	sourcePolicy(t, map[string]any{"executable": executable, "prefixCommands": commands, "prefixSpecNames": []string{"git", "npm", "kubectl", "gcloud", "aws", "az", "docker", "dotnet", "gh"}}, &source)
	if len(source.PrefixRows) != len(commands) {
		t.Fatal("incomplete actual Source prefix corpus")
	}
	var data any
	if err := json.Unmarshal(prefixSpecsJSON, &data); err != nil {
		t.Fatal(err)
	}
	sameJSON(t, "actual built-in registry data", data, source.BuiltInSpecs)
	if err := json.Unmarshal(neverSuggestJSON, &data); err != nil {
		t.Fatal(err)
	}
	sameJSON(t, "actual never-suggest aliases and cmdlet data", data, source.NeverSuggest)
	for _, row := range source.PrefixSpecs {
		if string(row[1]) != "null" {
			t.Fatalf("Source installed dynamic specification changed: %s -> %s", row[0], row[1])
		}
	}
	catalog, err := NewPrefixCatalog()
	if err != nil {
		t.Fatal(err)
	}
	classifier, err := NewClassifier("darwin", "")
	if err != nil {
		t.Fatal(err)
	}
	for i, command := range commands {
		row := source.PrefixRows[i]
		sameJSON(t, "first prefix "+command, catalog.First(row.Parsed), row.First)
		sameJSON(t, "compound prefix "+command, catalog.Compound(row.Parsed, nil), row.Compound)
		sameJSON(t, "filtered approval prefix "+command, catalog.Compound(row.Parsed, func(command Command) bool { return classifier.Allowlisted(command, command.Text) }), row.Filtered)
	}
}
