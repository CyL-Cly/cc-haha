package powershellpolicy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func sourcePolicy(t *testing.T, input map[string]any, output any) {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("requires pinned actual Source Bun")
	}
	home, _ := input["home"].(string)
	if home == "" {
		home = t.TempDir()
	}
	input["home"] = home
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../../.."))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", filepath.Join(root, "scripts/quality-gate/go-parity/native-powershell-policy.fixture.ts"))
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CLAUDE_CODE_TMPDIR=" + home, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_POWERSHELL_POLICY_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(input)
	command.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	data, err := command.Output()
	if err != nil {
		t.Fatal("actual PowerShell Source", err, stderr.String(), string(data))
	}
	if err = json.Unmarshal(data, output); err != nil {
		t.Fatal(err, string(data), stderr.String())
	}
}
func sameJSON(t *testing.T, label string, got any, want json.RawMessage) {
	t.Helper()
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var a, c any
	if json.Unmarshal(b, &a) != nil || json.Unmarshal(want, &c) != nil || !reflect.DeepEqual(a, c) {
		t.Errorf("%s native=%s Source=%s", label, b, want)
	}
}
func TestActualSourceNativePowerShellASTAndSecurityCorpus(t *testing.T) {
	executable := os.Getenv("CC_HAHA_TEST_PWSH")
	if executable == "" {
		if _, e := os.Stat("/opt/homebrew/bin/pwsh"); e == nil {
			executable = "/opt/homebrew/bin/pwsh"
		} else {
			executable, _ = exec.LookPath("pwsh")
		}
	}
	if executable == "" {
		t.Skip("requires real PowerShell AST host; native Windows gate supplies its executable")
	}
	commands := []string{
		`Get-Process`, `Get-ChildItem -Path 'C:\private\path' | Select-Object Name, Id`, `Get-Content 'quote''value'`, `Write-Output '中文😀 $literal'`, `Get-Process; Get-Service`,
		`Get-Process > $null`, `Get-Process 2>&1`, `Get-Process *> private.txt`, `1 > private.txt`, `Get-Process && 1 > private.txt`, `Get-Process -Name:('payload' > private.txt)`,
		`$x=1; Get-Process`, `if($true){Get-Process}`, `param($x=(Remove-Item /)); Get-Process`, `param([ValidateScript({rm /;$true})]$x='t'); Get-Process`, `function f { param($x=(Remove-Item /)); Get-Process }`,
		`Invoke-Expression 'literal'`, `iex 'literal'`, `Microsoft.PowerShell.Utility\Invoke-Expression 'literal'`, `& ('i'+'ex') 'literal'`, `& $cmd 'literal'`, `& 1`,
		`powershell /enc:literal`, `pwsh -EncodedCommand literal`, `C:\Windows\powershell.exe -e literal`, `Get-Content x | pwsh`, `pwsh`,
		`Start-BitsTransfer private`, `certutil /urlcache private`, `bitsadmin /transfer private`, `Add-Type -TypeDefinition 'literal'`,
		`New-Object -ComObject WScript.Shell`, `New-Object –Com:WScript.Shell`, `New-Object System.Net.WebClient`, `New-Object -TypeName:'System.String'`, `New-Object -Strict System.Net.WebClient`,
		`Start-Job -FilePath private.ps1`, `sajb private.ps1`, `Start-ThreadJob -f:private.ps1`, `Invoke-Item private.txt`, `ii private.ps1`,
		`Register-ScheduledTask private`, `schtasks /create private`, `Get-Process | ForEach-Object -MemberName Kill`, `Get-Process | % Kill`,
		`Start-Process cmd -Verb RunAs`, `Start-Process cmd –Verb:'RunAs'`, "Start-Process cmd -V`erb:`runas", `saps pwsh -ArgumentList literal`, `Start-Process 'C:\Program Files\PowerShell\pwsh.exe'`,
		`Get-Process | Where-Object { $_.Name }`, `Start-Job { Get-Process }`, `Get-Process $(Get-Service)`, `Get-Process @(Remove-Item private)`, `Write-Output "$env:PATH"`, `Write-Output @args`, `cmd --% /c echo private`,
		`[System.IO.File]::ReadAllText('private')`, `[System.Diagnostics.Process]'private'`, `[int]1`, `[string[]]'private'`,
		`$env:PRIVATE='literal'`, `Set-Item env:PRIVATE $env:PRIVATE`, `Import-Module private.psm1`, `ipmo private.psm1`, `Set-Alias private Get-Process`, `sv private literal`, `Invoke-WmiMethod -Class Win32_Process -Name Create`,
		`using module private.psm1`, `#Requires -Modules private` + "\nGet-Process", `Set-Location private; Get-Process`, `& 'ſtart-proceſſ' private`, `Get-Process (`,
		`Get-Content private.txt -ErrorAction SilentlyContinue`, `Get-Process –ComputerName private`, `Get-Process -Name:$env:PRIVATE`, `Get-Process | Out-Null`, `Get-Process | Out-Null -InputObject:(Remove-Item private)`, `Get-Process | Format-Table -AutoSize`,
		`where.exe /R private`, `scripts\where.exe /R private`, `ipconfig /all`, `ipconfig set private`, `hostname`, `hostname private`, `route -4 print`, `route add private print`,
		`git status`, `git log -n 5`, `git -c core.pager=sh log`, `git -Cprivate status`, `git --namespace private status`, `git --attr-source HEAD log status`, `git diff --output=private`, `git ls-remote private`, `git ls-remote https://private.invalid`, `docker ps --format private`, `docker logs --tail 5 private`, `gh version`, `dotnet --info`, `dotnet build`,
		`Set-Content private.txt literal`, `Remove-Item private.txt | Out-Null`, `Remove-Item $env:PRIVATE`, `New-Item -it:Sym` + "`" + `bolicLink -Path private; Get-Content private`, `Set-Location private; Set-Content private literal`, `Set-Content private @{k='payload' > private}`, `Remove-Item private | Format-Table -AutoSize`,
		`Start-Process cmd -Verb:RunAſ`, `Start-Process cmd -Verb:RUNAS`, "\uFEFFGet-Process\uFEFF", "\u0085Get-Process\u0085",
	}
	types := []string{"string", " String[] ", "String[]", "List[int]", "System.IO.File", "System.Net.WebClient", "adsi", "wmi", "int[][]", "PSCustomObject", "System.String"}
	var source struct {
		Rows []struct {
			Command                                                                        string
			Raw                                                                            json.RawMessage
			Parsed, Security, Flags, AllNames, AllCommands, Redirections, FileRedirections json.RawMessage
			Single, DirectoryChange, ReadOnly                                              bool
			Modes                                                                          []json.RawMessage
			ExtractedPaths                                                                 []json.RawMessage
		}
		Aliases                                   map[string]string
		Allowlist                                 json.RawMessage
		PathConfigs                               json.RawMessage
		Clm                                       []bool
		Script, Authority                         string
		WindowsMaxCommandLength, MaxCommandLength int
		Capabilities                              struct{ Blocked []string }
	}
	modes := []string{"default", "acceptEdits", "bypassPermissions", "dontAsk", "plan", "auto"}
	sourcePolicy(t, map[string]any{"executable": executable, "commands": commands, "types": types, "modes": modes}, &source)
	classifier, err := NewClassifier("darwin", "")
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, "original cmdlet data and callback contracts", classifier.Allowlist, source.Allowlist)
	configs, err := PathConfigs()
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, "original path parameter configurations", configs, source.PathConfigs)
	t.Logf("actual original AST commands=%d modes=%d cmdletConfigurations=%d pathConfigurations=%d", len(commands), len(modes), len(classifier.Allowlist), len(configs))
	if len(source.Rows) != len(commands) || len(source.Capabilities.Blocked) != 0 {
		t.Fatal("incomplete actual Source native AST")
	}
	if source.Script != ParseScriptBody || source.WindowsMaxCommandLength != WindowsMaxCommandLength() || source.MaxCommandLength != 4500 || !reflect.DeepEqual(CommonAliases, source.Aliases) {
		t.Fatal("Source parser script/budget/alias drift")
	}
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../../.."))
	bytes, e := os.ReadFile(filepath.Join(root, "src/utils/powershell/parser.ts"))
	if e != nil {
		t.Fatal(e)
	}
	hash := sha256.Sum256(bytes)
	if hex.EncodeToString(hash[:]) != source.Authority {
		t.Fatal("actual Source changed during capture")
	}
	for i, name := range types {
		if IsClmAllowedType(name) != source.Clm[i] {
			t.Errorf("CLM %q Source %v", name, source.Clm[i])
		}
	}
	for i, row := range source.Rows {
		t.Run(string(rune('A'+i))+"/"+row.Command, func(t *testing.T) {
			parsed, err := TransformRaw(row.Raw)
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, "AST", parsed, row.Parsed)
			sameJSON(t, "security", Security(parsed), row.Security)
			sameJSON(t, "flags", DeriveSecurityFlags(parsed), row.Flags)
			sameJSON(t, "names", AllCommandNames(parsed), row.AllNames)
			sameJSON(t, "commands", AllCommands(parsed), row.AllCommands)
			all := AllCommands(parsed)
			if len(all) != len(row.ExtractedPaths) {
				t.Fatal("actual Source path extraction missing")
			}
			for i, command := range all {
				sameJSON(t, "bound paths", ExtractPaths(command, configs), row.ExtractedPaths[i])
			}
			sameJSON(t, "redirections", AllRedirections(parsed), row.Redirections)
			sameJSON(t, "fileRedirections", FileRedirections(parsed), row.FileRedirections)
			if classifier.ReadOnly(row.Command, parsed) != row.ReadOnly {
				t.Errorf("read-only native=%v Source=%v", classifier.ReadOnly(row.Command, parsed), row.ReadOnly)
			}
			input, _ := json.Marshal(map[string]string{"command": row.Command})
			if len(row.Modes) != len(modes) {
				t.Fatal("actual Source mode corpus missing")
			}
			for i, mode := range modes {
				sameJSON(t, "permission mode "+mode, classifier.CheckMode(input, parsed, mode), row.Modes[i])
			}
			if IsSingleCommand(parsed) != row.Single || HasDirectoryChange(parsed) != row.DirectoryChange {
				t.Fatal("AST state predicates drift")
			}
			// These independently spawn the Go-owned producer, preserving original
			// quotes/Unicode, top-level parameter defaults, nested redirections and
			// command-name detail through the physical native AST boundary.
			if i < 5 || strings.HasPrefix(row.Command, "param(") || row.Command == `Get-Process -Name:('payload' > private.txt)` || row.Command == `Start-Process cmd –Verb:'RunAs'` {
				home := t.TempDir()
				parser := NewParser(ParserOptions{Platform: "darwin", Executable: executable, CWD: home, Environment: map[string]string{"HOME": home, "PATH": "/usr/bin:/bin", "DOTNET_CLI_HOME": home}})
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				sameJSON(t, "physical Go producer", parser.Parse(ctx, row.Command), row.Parsed)
			}
		})
	}
}
