package windowshell

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

type sourceGitRow struct {
	CWD         string            `json:"cwd"`
	Environment map[string]string `json:"environment"`
	Exists      []string          `json:"exists"`
	Where       string            `json:"where"`
	WhereError  bool              `json:"whereError"`
}

type sourceOverrideRow struct {
	Override string            `json:"override"`
	Platform string            `json:"platform"`
	Files    []string          `json:"files"`
	PSHome   string            `json:"psHome"`
	Which    map[string]string `json:"which"`
}

type sourceAvailabilityRow struct {
	Platform    string  `json:"platform"`
	UserType    string  `json:"userType,omitempty"`
	Configured  string  `json:"configured,omitempty"`
	GitBashPath *string `json:"gitBashPath,omitempty"`
	Setting     *string `json:"setting,omitempty"`
}

func TestActualSourceShellAvailabilityDefaultSelectionAndPowerShellInvocation(t *testing.T) {
	text := func(value string) *string { return &value }
	rows := []sourceAvailabilityRow{}
	for _, platform := range []string{"windows", "darwin", "linux"} {
		for _, path := range []*string{nil, text(`C:\Git\bin\bash.exe`)} {
			for _, setting := range []*string{nil, text("0"), text("false"), text(""), text("true"), text(" yes ")} {
				for _, configured := range []string{"", "bash", "powershell"} {
					rows = append(rows, sourceAvailabilityRow{Platform: platform, GitBashPath: path, Setting: setting, Configured: configured})
				}
			}
		}
	}
	rows = append(rows, sourceAvailabilityRow{Platform: "windows", GitBashPath: text("git-bash"), UserType: "ant"}, sourceAvailabilityRow{Platform: "windows", GitBashPath: text("git-bash"), UserType: "ant", Setting: text("off")})
	providers := []map[string]string{
		{"path": `C:\Program Files\PowerShell\7\pwsh.exe`, "command": `Write-Output 'literal $dollar and ! !'; $x = 'quote''value'`, "id": "ordinary"},
		{"path": `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, "command": "Write-Output @'\n中文 $literal `backtick\n'@", "id": "Unicode'quote"},
	}
	input := map[string]any{"availability": rows, "providers": providers}
	output := actualWindowsShellSource(t, input, nil)
	var source struct {
		Availability []struct {
			Available struct{ Bash, PowerShell bool }
			Shell     *string
		}
		Providers []struct {
			CommandString, CwdFilePath string
			Args                       []string
			Detached                   bool
		}
	}
	if err := json.Unmarshal(output, &source); err != nil || len(source.Availability) != len(rows) || len(source.Providers) != len(providers) {
		t.Fatal(err, string(output))
	}
	for index, row := range rows {
		path, setting := "", ""
		if row.GitBashPath != nil {
			path = *row.GitBashPath
		}
		if row.Setting != nil {
			setting = *row.Setting
		}
		got := ResolveAvailability(row.Platform, path, row.UserType, setting, row.Setting != nil)
		shell := DefaultShell(row.Configured, got)
		expectedShell := ""
		if source.Availability[index].Shell != nil {
			expectedShell = *source.Availability[index].Shell
		}
		if got.Bash != source.Availability[index].Available.Bash || got.PowerShell != source.Availability[index].Available.PowerShell || shell != expectedShell {
			t.Errorf("availability%d Go%+v/%q Source%+v/%q", index, got, shell, source.Availability[index].Available, expectedShell)
		}
	}
	for index, row := range providers {
		path := filepath.Join(input["temp"].(string), "claude-pwd-ps-"+row["id"])
		command := PowerShellCommand(row["command"], path)
		if source.Providers[index].CommandString != command || source.Providers[index].CwdFilePath != path || !reflect.DeepEqual(source.Providers[index].Args, PowerShellArgs(command)) || source.Providers[index].Detached {
			t.Errorf("original argv/cwd/exit trailer row%d Go%v Source%+v", index, PowerShellArgs(command), source.Providers[index])
		}
	}
}

func actualWindowsShellSource(t *testing.T, input map[string]any, environment map[string]string) []byte {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun not configured")
	}
	home := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	config, temp := filepath.Join(home, ".claude"), filepath.Join(home, "tmp")
	for _, directory := range []string{config, temp, filepath.Join(home, "bin")} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	// Prompt edition detection probes a private file only; it is never invoked.
	name := "pwsh"
	if input["edition"] == "desktop" {
		name = "powershell"
	}
	ps := filepath.Join(home, "bin", name)
	if input["edition"] == "unknown" {
		ps = filepath.Join(home, "bin", "absent.exe")
	} else if err := os.WriteFile(ps, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if executable, ok := input["powerShellPath"].(string); ok {
		if executable != "/opt/homebrew/bin/pwsh" {
			t.Fatal("unapproved physical PowerShell fixture executable", executable)
		}
		if _, err := os.Stat(executable); err != nil {
			t.Skip("native PowerShell is unavailable")
		}
		ps = executable
	}
	input["home"] = home
	input["temp"] = temp
	data, _ := json.Marshal(input)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-windows-shell.fixture.ts")
	sandbox, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_WINDOWS_SHELL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "ANTHROPIC_API_KEY=offline-windows-shell", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "CLAUDE_CODE_POWERSHELL_PATH=" + ps, "PATH=/usr/bin:/bin"}
	for name, value := range environment {
		command.Env = append(command.Env, name+"="+value)
	}
	command.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatal("actual Source Windows shell", err, stderr.String())
	}
	return output
}

func TestActualSourceGitBashDiscoveryAndPowerShellOverrides(t *testing.T) {
	const git64, git32 = `C:\Program Files\Git\cmd\git.exe`, `C:\Program Files (x86)\Git\cmd\git.exe`
	const bash64, bash32 = `C:\Program Files\Git\bin\bash.exe`, `C:\Program Files (x86)\Git\bin\bash.exe`
	git := []sourceGitRow{
		{CWD: `C:\Project`, Environment: map[string]string{"CLAUDE_CODE_GIT_BASH_PATH": `D:\Chosen Bash\bash.exe`}, Exists: []string{`D:\Chosen Bash\bash.exe`, git64, bash64}},
		{CWD: `C:\Project`, Environment: map[string]string{"CLAUDE_CODE_GIT_BASH_PATH": `D:\missing.exe`}, Exists: []string{git64, bash64}},
		{CWD: `C:\Project`, Exists: []string{git64, bash64, git32, bash32}},
		{CWD: `C:\Project`, Exists: []string{git32, bash32}},
		{CWD: `C:\PROJECT`, Where: "c:\\project\\git.exe\r\nC:\\Project\\shadow\\git.exe\r\nD:\\Tools\\Git\\cmd\\git.exe\r\n", Exists: []string{`D:\Tools\Git\bin\bash.exe`}},
		{CWD: `C:\Project`, Where: "D:\\First\\cmd\\git.exe\r\nE:\\Second\\cmd\\git.exe\r\n", Exists: []string{`E:\Second\bin\bash.exe`}},
		{CWD: `C:\工作 空间`, Where: "c:\\工作 空间\\BIN\\git.exe\r\nD:\\安装 空间\\Git\\cmd\\git.exe\r\n", Exists: []string{`D:\安装 空间\Git\bin\bash.exe`}},
		{CWD: `C:\Project`, WhereError: true},
	}
	overrides := []sourceOverrideRow{
		{Override: `C:\Chosen\pwsh.exe`, Platform: "windows", Files: []string{`C:\Chosen\pwsh.exe`}},
		{Override: `  pwsh  `, Platform: "windows", Files: []string{"pwsh"}},
		{Override: `other.exe`, Platform: "windows", Files: []string{"other.exe"}},
		{Override: `pwsh`, Platform: "windows", PSHome: `D:\Alias PowerShell`, Files: []string{`D:\Alias PowerShell\pwsh.exe`}},
		{Override: `pwsh`, Platform: "windows", Files: []string{`C:\Program Files\PowerShell\7\pwsh.exe`}},
		{Override: `powershell`, Platform: "windows", Files: []string{`C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`}},
		{Override: `pwsh`, Platform: "windows", Which: map[string]string{"pwsh": `D:\Search\pwsh.exe`}},
		{Override: `D:\Missing\pwsh.exe`, Platform: "windows", Which: map[string]string{`D:\Missing\pwsh.exe`: `D:\not-eligible.exe`}},
		{Override: `PWsh`, Platform: "darwin", Which: map[string]string{"PWsh": "/private/native/pwsh"}},
		{Override: `/private/native/powershell`, Platform: "darwin", Files: []string{"/private/native/powershell"}},
	}
	output := actualWindowsShellSource(t, map[string]any{"git": git, "overrides": overrides}, nil)
	var source struct {
		Git []struct {
			Path  *string
			Calls []string
		}
		Overrides []struct {
			Path  *string
			Calls [][]string
		}
	}
	if err := json.Unmarshal(output, &source); err != nil || len(source.Git) != len(git) || len(source.Overrides) != len(overrides) {
		t.Fatal(err, string(output))
	}
	for index, row := range git {
		calls := []string{}
		exists := map[string]bool{}
		for _, path := range row.Exists {
			exists[path] = true
		}
		resolver := New("windows", row.CWD, row.Environment, Dependencies{PathExists: func(_ context.Context, path string) bool {
			calls = append(calls, `dir "`+path+`"`)
			return exists[path]
		}, Where: func(context.Context, string) (string, error) {
			calls = append(calls, "where.exe git")
			if row.WhereError {
				return "", errors.New("where unavailable")
			}
			return row.Where, nil
		}})
		path := resolver.GitBash(context.Background())
		expected := ""
		if source.Git[index].Path != nil {
			expected = *source.Git[index].Path
		}
		if path != expected || !reflect.DeepEqual(calls, source.Git[index].Calls) {
			t.Errorf("Git row%d path Go%q Source%q, probes Go%v Source%v", index, path, expected, calls, source.Git[index].Calls)
		}
		resolver.GitBash(context.Background())
		if !reflect.DeepEqual(calls, source.Git[index].Calls) {
			t.Error("original cached Git Bash resolver repeated native probes")
		}
	}
	for index, row := range overrides {
		calls := [][]string{}
		files := map[string]bool{}
		for _, path := range row.Files {
			files[path] = true
		}
		resolver := New(row.Platform, `C:\Project`, nil, Dependencies{FileExists: func(_ context.Context, path string) bool {
			calls = append(calls, []string{"probe", path})
			return files[path]
		}, ReadWindowsPwshHome: func(context.Context) string {
			calls = append(calls, []string{"home"})
			return row.PSHome
		}, Which: func(_ context.Context, command string) string {
			calls = append(calls, []string{"which", command})
			return row.Which[command]
		}})
		path := resolver.powerShellOverride(context.Background(), row.Override)
		expected := ""
		if source.Overrides[index].Path != nil {
			expected = *source.Overrides[index].Path
		}
		if path != expected || !reflect.DeepEqual(calls, source.Overrides[index].Calls) {
			t.Errorf("PowerShell row%d path Go%q Source%q, probes Go%v Source%v", index, path, expected, calls, source.Overrides[index].Calls)
		}
	}
}
