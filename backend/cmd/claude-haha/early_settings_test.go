package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func normalEnvironment(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Chdir(home)
	for _, key := range []string{"COREPACK_ENABLE_AUTO_PIN", "NODE_OPTIONS", "CLAUDE_CODE_SIMPLE", "NoDefaultCurrentDirectoryInExePath", "CLAUDE_CODE_ENTRYPOINT", "DEBUG", "DEBUG_SDK", "CLAUDE_CODE_REMOTE", "CLAUDE_CODE_FORCE_RECOVERY_CLI", "FORCE_COLOR", "CLAUDE_CODE_ACTION", "CLAUDE_CODE_PROFILE_STARTUP"} {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}
	t.Setenv("HOME", home)
	t.Setenv("USER_TYPE", "external")
	t.Setenv("NO_COLOR", "1")
	t.Setenv("TMPDIR", filepath.Join(home, "tmp"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	for _, dir := range []string{"tmp", ".claude"} {
		if err := os.Mkdir(filepath.Join(home, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{".claude.json", "settings.json"} {
		if err := os.WriteFile(filepath.Join(home, ".claude", name), []byte("{}"), 0640); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func normalRun(t *testing.T, args []string) (int, string, string) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer outR.Close()
	defer errR.Close()
	os.Stdout, os.Stderr = outW, errW
	defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()
	code := run(context.Background(), args)
	outW.Close()
	errW.Close()
	out, _ := io.ReadAll(outR)
	stderr, _ := io.ReadAll(errR)
	return code, string(out), string(stderr)
}

func TestActualSourceEarlySettingsEntry(t *testing.T) {
	var input struct {
		Cases []struct {
			Name        string
			Args        []string
			ExitCode    int
			Stdout      string
			Stderr      string
			Environment map[string]*string
			Files       map[string]struct {
				RawBase64 string
				Mode      uint32
			}
		}
	}
	data, err := os.ReadFile("testdata/normal-bootstrap-source.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &input); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range input.Cases {
		t.Run(fixture.Name, func(t *testing.T) {
			home := normalEnvironment(t)
			code, out, stderr := normalRun(t, fixture.Args)
			if code != fixture.ExitCode || out != fixture.Stdout || stderr != fixture.Stderr {
				t.Fatalf("actual Source entry mismatch: code=%d stdout=%q stderr=%q", code, out, stderr)
			}
			for key, want := range fixture.Environment {
				value, exists := os.LookupEnv(key)
				if (want == nil && exists) || (want != nil && (!exists || value != *want)) {
					t.Fatalf("Source environment mismatch %s: exists=%v value=%q want=%v", key, exists, value, want)
				}
			}
			seen := map[string]bool{}
			for _, dir := range []string{".claude", "tmp"} {
				if err := filepath.WalkDir(filepath.Join(home, dir), func(path string, entry os.DirEntry, err error) error {
					if err != nil || entry.IsDir() {
						return err
					}
					name, _ := filepath.Rel(home, path)
					want, exists := fixture.Files[filepath.ToSlash(name)]
					if !exists {
						t.Fatalf("unexpected file: %s", name)
					}
					seen[filepath.ToSlash(name)] = true
					data, err := os.ReadFile(path)
					if err != nil {
						return err
					}
					info, err := entry.Info()
					if err != nil {
						return err
					}
					if base64.StdEncoding.EncodeToString(data) != want.RawBase64 || uint32(info.Mode().Perm()) != want.Mode {
						t.Fatalf("Source raw file/mode mismatch %s", name)
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			if len(seen) != len(fixture.Files) {
				t.Fatal("Source file omitted")
			}
		})
	}
}

func TestEarlySettingsAdmissionAndUnmigratedGuards(t *testing.T) {
	for _, args := range [][]string{nil, {"--help"}, {"--bare"}, {"--bare", "--settings"}, {"--bare", "--model", "fixture", "--setting-sources", "future"}, {"--setting-sources", "future"}} {
		if earlySettingsArgs(args) {
			t.Fatalf("unmigrated family admitted: %q", args)
		}
	}
	for _, args := range [][]string{{"--bare", "--settings={broken}"}, {"--bare", "--setting-sources=future"}} {
		if !earlySettingsArgs(args) {
			t.Fatalf("eager settings equals form not classified: %q", args)
		}
	}
	t.Run("successful-bootstrap-hands-off-to-command", func(t *testing.T) {
		home := normalEnvironment(t)
		code, handled := earlySettingsFailure([]string{"--bare", "--settings", " {} "})
		if code != 0 || handled {
			t.Fatal("successful eager settings load intercepted the migrated command", code, handled)
		}
		entries, err := os.ReadDir(filepath.Join(home, "tmp"))
		if err != nil || len(entries) != 1 {
			t.Fatal("real eager settings write missing before unfinished boundary")
		}
	})
	for _, key := range []string{"USER_TYPE", "NO_COLOR", "DEBUG", "DEBUG_SDK", "CLAUDE_CODE_REMOTE", "NODE_OPTIONS", "FORCE_COLOR", "CLAUDE_CODE_ACTION", "CLAUDE_CODE_PROFILE_STARTUP"} {
		t.Run(key, func(t *testing.T) {
			home := normalEnvironment(t)
			t.Setenv(key, "unported-host")
			before := map[string]string{}
			for _, field := range []string{"COREPACK_ENABLE_AUTO_PIN", "CLAUDE_CODE_SIMPLE", "NoDefaultCurrentDirectoryInExePath", "CLAUDE_CODE_ENTRYPOINT"} {
				before[field] = os.Getenv(field)
			}
			code, handled := earlySettingsFailure([]string{"--bare", "--settings", "{}", "--setting-sources", "future"})
			if code != 0 || handled {
				t.Fatal("unsupported eager-settings profile was admitted", code, handled)
			}
			after := map[string]string{}
			for field := range before {
				after[field] = os.Getenv(field)
			}
			entries, _ := os.ReadDir(filepath.Join(home, "tmp"))
			if !reflect.DeepEqual(before, after) || len(entries) != 0 {
				t.Fatal("unsupported host mutated startup environment or settings before guard")
			}
		})
	}
}

func TestEarlySettingsUnavailableOutputAndCWD(t *testing.T) {
	for _, fixture := range []string{"closed-output", "device-output", "absent-cwd"} {
		t.Run(fixture, func(t *testing.T) {
			home := normalEnvironment(t)
			oldOut := os.Stdout
			defer func() { os.Stdout = oldOut }()
			if fixture == "device-output" {
				file, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
				os.Stdout = file
			} else {
				read, write, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				defer read.Close()
				defer write.Close()
				os.Stdout = write
				if fixture == "closed-output" {
					write.Close()
				} else {
					t.Chdir(filepath.Join(home, "tmp"))
					if err := os.Remove(filepath.Join(home, "tmp")); err != nil {
						t.Fatal(err)
					}
				}
			}
			if fixture == "absent-cwd" {
				// Darwin getcwd can still return the removed directory spelling.
				// Removal is not evidence of a Getwd error. Preserve the genuine
				// native fact rather than claiming an exercised failure branch.
				if cwd, err := os.Getwd(); err == nil {
					if code, handled := earlySettingsFailure([]string{"--bare", "--setting-sources", "future"}); code != 1 || !handled {
						t.Fatalf("native removed-CWD control cwd=%q did not execute original settings failure", cwd)
					}
					return
				}
			}
			if _, handled := earlySettingsFailure([]string{"--bare", "--setting-sources", "future"}); handled {
				t.Fatal("unrepresented physical output/CWD profile admitted")
			}
			if os.Getenv("CLAUDE_CODE_SIMPLE") != "" {
				t.Fatal("guard ran after startup effects")
			}
		})
	}
}

func TestEarlySettingsNativeDomain(t *testing.T) {
	if !earlySettingsPlatform("darwin", "arm64") {
		t.Fatal("observed native Source domain rejected")
	}
	for _, target := range [][2]string{{"darwin", "amd64"}, {"linux", "arm64"}, {"windows", "amd64"}, {"", ""}} {
		if earlySettingsPlatform(target[0], target[1]) {
			t.Fatalf("unmigrated startup target admitted: %s/%s", target[0], target[1])
		}
	}
}
