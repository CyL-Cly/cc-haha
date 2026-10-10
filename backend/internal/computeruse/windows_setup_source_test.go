package computeruse

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type windowsCommandFixture struct {
	Command    string   `json:"command"`
	Args       []string `json:"args"`
	Code       int      `json:"code"`
	Stdout     string   `json:"stdout"`
	Stderr     string   `json:"stderr"`
	CreateVenv bool     `json:"createVenv"`
	NoPip      bool     `json:"noPip"`
}
type windowsSetupFixture struct {
	Name, Custom string
	Files        map[string]string
	Plan         []windowsCommandFixture
}

func windowsSetupFixtureCases() []windowsSetupFixture {
	python := "$ROOT/config/.runtime/venv/Scripts/python.exe"
	runtime := "$ROOT/config/.runtime"
	check := windowsCommandFixture{Command: "python3", Args: []string{"--version"}, Stdout: "Python 3.12.7"}
	locate := windowsCommandFixture{Command: "where", Args: []string{"python3"}, Stdout: "C:\\Python312\\python3.exe\r\nC:\\other\\python3.exe"}
	create := windowsCommandFixture{Command: "python3", Args: []string{"-m", "venv", runtime + "/venv"}, CreateVenv: true}
	pip := func(args ...string) windowsCommandFixture {
		return windowsCommandFixture{Command: python, Args: append(append([]string{"-m", "pip", "install"}, args...), "-i", "https://pypi.tuna.tsinghua.edu.cn/simple/", "--trusted-host", "pypi.tuna.tsinghua.edu.cn")}
	}
	upgrade := pip("--upgrade", "pip")
	deps := pip("-r", runtime+"/requirements.txt")
	cases := []windowsSetupFixture{{Name: "fresh", Plan: []windowsCommandFixture{check, locate, create, upgrade, deps}}}
	old := check
	old.Stdout = "Python 3.8.20"
	cases = append(cases, windowsSetupFixture{Name: "python-too-old", Plan: []windowsCommandFixture{old, locate}})
	custom := "C:\\custom\\python.exe"
	cases = append(cases, windowsSetupFixture{Name: "custom-unavailable", Custom: custom, Plan: []windowsCommandFixture{{Command: custom, Args: []string{"--version"}, Code: 1, Stderr: "custom fixture unavailable"}}})
	failedCreate := create
	failedCreate.Code = 1
	failedCreate.CreateVenv = false
	failedCreate.Stderr = "venv fixture denied"
	cases = append(cases, windowsSetupFixture{Name: "venv-failed", Plan: []windowsCommandFixture{check, locate, failedCreate}})
	withoutPip := create
	withoutPip.NoPip = true
	ensure := windowsCommandFixture{Command: python, Args: []string{"-m", "ensurepip", "--upgrade"}, Code: 1, Stderr: "ensurepip fixture failed"}
	cases = append(cases, windowsSetupFixture{Name: "ensurepip-failed", Plan: []windowsCommandFixture{check, locate, withoutPip, ensure}})
	mirrorFailed := deps
	mirrorFailed.Code = 1
	mirrorFailed.Stderr = "mirror first failure"
	fallback := deps
	fallback.Args = fallback.Args[:len(fallback.Args)-4]
	cases = append(cases, windowsSetupFixture{Name: "mirror-fallback", Plan: []windowsCommandFixture{check, locate, create, upgrade, mirrorFailed, fallback}})
	unicodeFailure := mirrorFailed
	unicodeFailure.Stderr = strings.Repeat("🧭", 250) + "UNEXPECTED_TAIL"
	cases = append(cases, windowsSetupFixture{Name: "dependency-error-UTF16-500-units", Plan: []windowsCommandFixture{check, locate, create, upgrade, unicodeFailure, windowsCommandFixture{Command: python, Args: deps.Args[:len(deps.Args)-4], Code: 1, Stderr: "second"}}})
	fallback.Code = 1
	fallback.Stderr = "default second failure"
	cases = append(cases, windowsSetupFixture{Name: "both-indexes-failed-first-error", Plan: []windowsCommandFixture{check, locate, create, upgrade, mirrorFailed, fallback}})
	upgradeFail := upgrade
	upgradeFail.Code = 1
	upgradeFail.Stderr = "upgrade mirror failed"
	upgradeFallback := upgradeFail
	upgradeFallback.Args = upgradeFallback.Args[:len(upgradeFallback.Args)-4]
	cases = append(cases, windowsSetupFixture{Name: "upgrade-failure-still-installs-dependencies", Plan: []windowsCommandFixture{check, locate, create, upgradeFail, upgradeFallback, deps}})
	files := map[string]string{"venv/Scripts/python.exe": "old-venv", "venv/Scripts/pip.exe": "old-pip", "requirements.txt": string(windowsRequirements), "requirements.sha256": requirementsDigest(windowsRequirements) + "\n"}
	cases = append(cases, windowsSetupFixture{Name: "legacy-no-marker-reuses-stamp", Files: files, Plan: []windowsCommandFixture{check, locate}})
	changed := map[string]string{}
	for k, v := range files {
		changed[k] = v
	}
	changed["venv-base-interpreter.txt"] = "C:\\old\\python.exe"
	customCheck := check
	customCheck.Command = custom
	customCreate := create
	customCreate.Command = custom
	cases = append(cases, windowsSetupFixture{Name: "custom-change-rebuilds-and-installs", Custom: custom, Files: changed, Plan: []windowsCommandFixture{customCheck, customCreate, upgrade, deps}})
	fail := func(cmd string, args ...string) windowsCommandFixture {
		return windowsCommandFixture{Command: cmd, Args: args, Code: 1}
	}
	cases = append(cases, windowsSetupFixture{Name: "missing-all-python-candidates", Plan: []windowsCommandFixture{fail("python3", "--version"), fail("python", "--version"), fail("py", "-3", "--version"), fail("py", "--version")}})
	launcher := windowsCommandFixture{Command: "py", Args: []string{"-3", "--version"}, Stdout: "Python 3.11.9"}
	launchCreate := create
	launchCreate.Command = "py"
	launchCreate.Args = append([]string{"-3"}, create.Args...)
	cases = append(cases, windowsSetupFixture{Name: "py-launcher-prefix", Plan: []windowsCommandFixture{fail("python3", "--version"), fail("python", "--version"), launcher, {Command: "where", Args: []string{"py"}, Stdout: "C:\\Windows\\py.exe"}, launchCreate, upgrade, deps}})
	return cases
}
func setupFileHashes(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		sum := sha256.Sum256(data)
		files[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestActualWindowsSetupCommandsMarkersAssetsAndFailuresMatchOriginalSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source needs pinned Bun")
	}
	repository, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range windowsSetupFixtureCases() {
		t.Run(c.Name, func(t *testing.T) {
			sourceRoot := t.TempDir()
			input := map[string]any{"root": sourceRoot, "repository": repository, "custom": c.Custom, "files": c.Files, "plan": c.Plan}
			encoded, _ := json.Marshal(input)
			fixture, _ := filepath.Abs("testdata/windows_setup.fixture.ts")
			cmd := exec.Command(bun, "--no-env-file", fixture)
			cmd.Dir = sourceRoot
			cmd.Env = []string{"HOME=" + sourceRoot, "USERPROFILE=" + sourceRoot, "CLAUDE_CONFIG_DIR=" + filepath.Join(sourceRoot, "config"), "TMPDIR=" + sourceRoot, "PATH=/usr/bin:/bin", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
			cmd.Stdin = bytes.NewReader(encoded)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("actual Source setup: %v %s", err, output)
			}
			var expected struct {
				Result   setupResult       `json:"result"`
				Calls    [][]string        `json:"calls"`
				Files    map[string]string `json:"files"`
				Consumed int               `json:"consumed"`
			}
			if err = json.Unmarshal(output, &expected); err != nil {
				t.Fatalf("Source output: %v %s", err, output)
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
			o.Run = func(_ context.Context, command string, args []string, _ []*os.File) (CommandResult, error) {
				call := append([]string{command}, args...)
				for i := range call {
					call[i] = strings.ReplaceAll(call[i], root, "$ROOT")
				}
				calls = append(calls, call)
				if ordinal >= len(c.Plan) {
					t.Errorf("unexpected Go command %v", call)
					return CommandResult{Code: 1}, fmt.Errorf("unexpected command")
				}
				row := c.Plan[ordinal]
				ordinal++
				if !reflect.DeepEqual(call, append([]string{row.Command}, row.Args...)) {
					t.Errorf("different command: %v wanted %s %v", call, row.Command, row.Args)
				}
				if row.CreateVenv {
					dir := filepath.Join(o.ConfigDir, ".runtime/venv/Scripts")
					if err := os.MkdirAll(dir, 0700); err != nil {
						t.Fatal(err)
					}
					_ = os.WriteFile(filepath.Join(dir, "python.exe"), []byte("owned-venv"), 0700)
					if !row.NoPip {
						_ = os.WriteFile(filepath.Join(dir, "pip.exe"), []byte("owned-pip"), 0700)
					}
				}
				return CommandResult{Code: row.Code, Stdout: row.Stdout, Stderr: row.Stderr}, nil
			}
			service := fixtureService(t, o)
			if c.Custom != "" {
				if err := service.UpdateConfig(context.Background(), encode(map[string]any{"pythonPath": c.Custom})); err != nil {
					t.Fatal(err)
				}
			}
			actual, err := service.WindowsSetup(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			var actualWire setupResult
			if err := json.Unmarshal(encode(actual), &actualWire); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actualWire, expected.Result) {
				t.Fatalf("result differs\nGo=%s\nSource=%s", encode(actual), encode(expected.Result))
			}
			if !reflect.DeepEqual(calls, expected.Calls) || ordinal != expected.Consumed {
				t.Fatalf("command sequence differs Go=%v Source=%v", calls, expected.Calls)
			}
			files := setupFileHashes(t, filepath.Join(o.ConfigDir, ".runtime"))
			if !reflect.DeepEqual(files, expected.Files) {
				t.Fatalf("physical runtime/stamp/marker differs Go=%v Source=%v", files, expected.Files)
			}
		})
	}
}
