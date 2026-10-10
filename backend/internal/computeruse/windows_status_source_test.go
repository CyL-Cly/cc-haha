package computeruse

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestActualWindowsStatusReadsLegacyMarkersAndPermissionsLikeOriginalSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source needs pinned Bun")
	}
	repository, _ := filepath.Abs("../../..")
	fixture, _ := filepath.Abs("testdata/windows_setup.fixture.ts")
	check := windowsCommandFixture{Command: "python3", Args: []string{"--version"}, Stdout: "Python 3.12.7"}
	where := windowsCommandFixture{Command: "where", Args: []string{"python3"}, Stdout: "C:\\Python312\\python3.exe"}
	permission := windowsCommandFixture{Command: "$ROOT/config/.runtime/venv/Scripts/python.exe", Args: []string{"$ROOT/config/.runtime/win_helper.py", "check_permissions"}, Stdout: `{"ok":true,"result":{"accessibility":false,"screenRecording":true}}`}
	files := map[string]string{"venv/Scripts/python.exe": "old-venv", "venv/Scripts/pip.exe": "old-pip", "requirements.txt": string(windowsRequirements), "requirements.sha256": requirementsDigest(windowsRequirements) + "\n"}
	mismatch := map[string]string{}
	for k, v := range files {
		mismatch[k] = v
	}
	mismatch["venv-base-interpreter.txt"] = "C:\\old\\python.exe"
	noisy := permission
	noisy.Stdout = "log chatter\n" + permission.Stdout
	cases := []windowsSetupFixture{
		{Name: "cold", Plan: []windowsCommandFixture{check, where}},
		{Name: "legacy-marker-absent-ready", Files: files, Plan: []windowsCommandFixture{check, where, permission}},
		{Name: "changed-interpreter-not-ready", Custom: "C:\\changed\\python.exe", Files: mismatch, Plan: []windowsCommandFixture{{Command: "C:\\changed\\python.exe", Args: []string{"--version"}, Stdout: "Python 3.12.7"}}},
		{Name: "permission-chatter-remains-unknown", Files: files, Plan: []windowsCommandFixture{check, where, noisy}},
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			sourceRoot := t.TempDir()
			input := map[string]any{"root": sourceRoot, "repository": repository, "custom": c.Custom, "files": c.Files, "plan": c.Plan, "action": "status"}
			data, _ := json.Marshal(input)
			cmd := exec.Command(bun, "--no-env-file", fixture)
			cmd.Dir = sourceRoot
			cmd.Env = []string{"HOME=" + sourceRoot, "USERPROFILE=" + sourceRoot, "CLAUDE_CONFIG_DIR=" + filepath.Join(sourceRoot, "config"), "TMPDIR=" + sourceRoot, "PATH=/usr/bin:/bin", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
			cmd.Stdin = bytes.NewReader(data)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("Source %v %s", err, output)
			}
			var source struct {
				Result json.RawMessage   `json:"result"`
				Calls  [][]string        `json:"calls"`
				Files  map[string]string `json:"files"`
			}
			if err = json.Unmarshal(output, &source); err != nil {
				t.Fatal(err, string(output))
			}
			o := fixtureOptions(t)
			o.Platform = "win32"
			root := filepath.Dir(o.ConfigDir)
			for name, data := range c.Files {
				path := filepath.Join(o.ConfigDir, ".runtime", name)
				if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(path, []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			calls := [][]string{}
			ordinal := 0
			o.Run = func(_ context.Context, cmd string, args []string, _ []*os.File) (CommandResult, error) {
				call := append([]string{cmd}, args...)
				for i := range call {
					call[i] = strings.ReplaceAll(call[i], root, "$ROOT")
				}
				calls = append(calls, call)
				if ordinal >= len(c.Plan) {
					t.Fatal("unexpected status command", call)
				}
				row := c.Plan[ordinal]
				ordinal++
				if !reflect.DeepEqual(call, append([]string{row.Command}, row.Args...)) {
					t.Fatalf("command %v expected %s %v", call, row.Command, row.Args)
				}
				return CommandResult{Code: row.Code, Stdout: row.Stdout, Stderr: row.Stderr}, nil
			}
			s := fixtureService(t, o)
			if c.Custom != "" {
				if err = s.UpdateConfig(context.Background(), encode(map[string]any{"pythonPath": c.Custom})); err != nil {
					t.Fatal(err)
				}
			}
			status, body := request(t, s, "GET", "status", "")
			if status != 200 {
				t.Fatal(status, string(body))
			}
			actual := jsonValue(t, []byte(strings.ReplaceAll(string(body), root, "$ROOT")))
			expected := jsonValue(t, []byte(strings.ReplaceAll(string(source.Result), sourceRoot, "$ROOT")))
			if !reflect.DeepEqual(actual, expected) {
				t.Fatalf("Go status=%s\nSource=%s", encode(actual), encode(expected))
			}
			if !reflect.DeepEqual(calls, source.Calls) {
				t.Fatal("status command sequence", calls, source.Calls)
			}
			if files := setupFileHashes(t, filepath.Join(o.ConfigDir, ".runtime")); !reflect.DeepEqual(files, source.Files) {
				t.Fatal("status changed physical runtime differently", files, source.Files)
			}
		})
	}
}
