package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestActualPublicCLIStartupFileErrorsInitOnlyAndRewindAgainstSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	binary := filepath.Join(root, "actual-file-flags-cli")
	build := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-p=4", "-tags=native_process_fixture", "-o", binary, ".")
	capturePublicGoCoverage(t, build)
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	fixture, _ := filepath.Abs("../../testdata/native-cli-resume-errors.fixture.ts")
	for _, row := range []struct {
		name                   string
		flags                  []string
		rewind                 bool
		checkpoint             bool
		missingBackup, noPrint bool
		enabled                bool
		prompt                 string
	}{
		{name: "system-conflict", flags: []string{"--system-prompt", "text", "--system-prompt-file", "missing"}},
		{name: "append-conflict", flags: []string{"--append-system-prompt", "text", "--append-system-prompt-file", "missing"}},
		{name: "system-missing", flags: []string{"--system-prompt-file", "missing"}},
		{name: "append-missing", flags: []string{"--append-system-prompt-file", "missing"}},
		{name: "init-only-without-print", noPrint: true, flags: []string{"--init-only"}},
		{name: "init-only-no-model", flags: []string{"--init-only", "--continue", "--resume", "missing"}},
		{name: "rewind-needs-resume", flags: []string{"--rewind-files", "u1"}},
		{name: "rewind-not-user", rewind: true, flags: []string{"--rewind-files", "a1"}, enabled: true},
		{name: "rewind-disabled", rewind: true, flags: []string{"--rewind-files", "u1"}},
		{name: "rewind-no-checkpoint", rewind: true, flags: []string{"--rewind-files", "u1"}, enabled: true},
		{name: "rewind-missing-backup", missingBackup: true, rewind: true, flags: []string{"--rewind-files", "u1"}, checkpoint: true, enabled: true},
		{name: "rewind-physical", rewind: true, flags: []string{"--rewind-files", "u1"}, checkpoint: true, enabled: true},
		{name: "rewind-with-prompt", rewind: true, flags: []string{"--rewind-files", "u1"}, prompt: "must not submit", enabled: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			sides := []string{resumeErrorHome(t, root, "source-"+row.name), resumeErrorHome(t, root, "go-"+row.name)}
			outputs := []string{}
			errors := []string{}
			codes := []int{}
			for index, home := range sides {
				transcript := filepath.Join(home, "valid.jsonl")
				original, _ := os.ReadFile(transcript)
				if row.rewind {
					transcript = filepath.Join(home, ".claude", "projects", projectpath.Sanitize(home), resumeErrorRequestedSID+".jsonl")
					if e := os.MkdirAll(filepath.Dir(transcript), 0700); e != nil {
						t.Fatal(e)
					}
					if e := os.WriteFile(transcript, original, 0600); e != nil {
						t.Fatal(e)
					}
				}
				if row.checkpoint {
					backupdir := filepath.Join(home, ".claude", "file-history", resumeErrorRequestedSID)
					if e := os.MkdirAll(backupdir, 0700); e != nil {
						t.Fatal(e)
					}
					if e := os.WriteFile(filepath.Join(backupdir, "before-physical"), []byte("BEFORE_REWIND_PHYSICAL\n"), 0600); e != nil {
						t.Fatal(e)
					}
					if row.missingBackup {
						if e := os.Remove(filepath.Join(backupdir, "before-physical")); e != nil {
							t.Fatal(e)
						}
					}
					snapshot := map[string]any{"type": "file-history-snapshot", "messageId": "u1", "isSnapshotUpdate": false, "snapshot": map[string]any{"messageId": "u1", "timestamp": "2026-10-01T00:00:00.000Z", "schemaVersion": 1, "trackedFileBackups": map[string]any{"edited.txt": map[string]any{"backupFileName": "before-physical", "version": 1, "backupTime": "2026-10-01T00:00:00.000Z"}}}}
					data, _ := json.Marshal(snapshot)
					original = append(original, append(data, '\n')...)
					if e := os.WriteFile(transcript, original, 0600); e != nil {
						t.Fatal(e)
					}
				}
				if e := os.WriteFile(filepath.Join(home, "edited.txt"), []byte("AFTER_REWIND_PHYSICAL\n"), 0600); e != nil {
					t.Fatal(e)
				}
				args := []string{"--bare", "--print", "--model", "fixture-model", "--thinking", "disabled", "--no-computer-use", "--tools", "", "--output-format", "json"}
				if row.noPrint {
					args = append(args[:1], args[2:]...)
				}
				if row.rewind {
					args = append(args, "--resume", resumeErrorRequestedSID)
				}
				args = append(args, row.flags...)
				if row.prompt != "" {
					args = append(args, row.prompt)
				}
				environment := append(resumeErrorEnv(home, root, "http://127.0.0.1:1"), "PATH=/usr/bin:/bin")
				if row.enabled {
					environment = append(environment, "CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING=1")
				}
				var command *exec.Cmd
				if index == 0 {
					input, _ := json.Marshal(map[string]any{"homeDir": home, "args": args})
					command = exec.CommandContext(ctx, bun, "--no-env-file", fixture)
					environment = append(environment, "CC_HAHA_RESUME_ERROR_FIXTURE=1", "CC_HAHA_RESUME_ERROR_INPUT="+string(input))
				} else {
					command = exec.CommandContext(ctx, binary, args...)
				}
				command.Dir = home
				command.Env = environment
				code, out, stderr := resumeErrorRun(t, command, false)
				codes = append(codes, code)
				outputs = append(outputs, strings.ReplaceAll(string(out), home, "<HOME>"))
				errors = append(errors, strings.ReplaceAll(string(stderr), home, "<HOME>"))
				after, _ := os.ReadFile(transcript)
				if !bytes.Equal(original, after) {
					t.Fatal("standalone initialization changed original transcript", home, string(after))
				}
				state, _ := os.ReadFile(filepath.Join(home, "edited.txt"))
				want := "AFTER_REWIND_PHYSICAL\n"
				if row.checkpoint && !row.missingBackup {
					want = "BEFORE_REWIND_PHYSICAL\n"
				}
				if string(state) != want {
					t.Fatal("actual file checkpoint operation failed", home, string(state), code, string(stderr))
				}
				if index == 0 {
					var receipt struct {
						Requests     []json.RawMessage
						Capabilities struct{ Blocked []string }
					}
					data, _ := os.ReadFile(filepath.Join(home, "source-resume-error-receipt.json"))
					if e := json.Unmarshal(data, &receipt); e != nil || len(receipt.Requests) != 0 || len(receipt.Capabilities.Blocked) != 0 {
						t.Fatal("uncontrolled Source or model request", e, string(data))
					}
				}
			}
			if codes[0] != codes[1] || outputs[0] != outputs[1] || errors[0] != errors[1] {
				t.Fatalf("actual public flag differs Source code%d stdout%q stderr%q; Go code%d stdout%q stderr%q", codes[0], outputs[0], errors[0], codes[1], outputs[1], errors[1])
			}
			if row.name == "rewind-physical" && outputs[0] != fmt.Sprintf("Files rewound to state at message u1\n") {
				t.Fatal(outputs)
			}
		})
	}
}
