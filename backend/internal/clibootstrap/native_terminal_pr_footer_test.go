package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type prFooterSourceFetch struct {
	IsGit                         bool `json:"isGit"`
	Branch, DefaultBranch, Stdout string
	Code                          int
}

func terminalPRSource(t *testing.T, input any) []struct {
	Value json.RawMessage
	Calls [][]json.RawMessage
} {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual pinned Source Bun required")
	}
	home := t.TempDir()
	value := mainObj(rawMain(input))
	value.Set("home", quoteMain(home))
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-pr-footer.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_PR_SOURCE=1"}
	cmd.Stdin = bytes.NewReader(rawMain(value))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual original PR footer %v %.5000s", err, output)
	}
	var parsed struct {
		FetchResults []struct {
			Value json.RawMessage
			Calls [][]json.RawMessage
		}
	}
	if err = json.Unmarshal(output, &parsed); err != nil {
		t.Fatal(err, string(output))
	}
	return parsed.FetchResults
}

func TestTerminalPRStatusActualOriginalGHParserAndSkipBoundaries(t *testing.T) {
	rows := []prFooterSourceFetch{}
	for _, text := range []string{
		`{"number":12,"url":"https://example.test/pr/12","reviewDecision":"APPROVED","isDraft":false,"headRefName":"feature","state":"OPEN"}`,
		`{"number":12,"url":"u","reviewDecision":"CHANGES_REQUESTED","isDraft":false,"headRefName":"feature","state":"OPEN"}`,
		`{"number":12,"url":"u","reviewDecision":"REVIEW_REQUIRED","isDraft":false,"headRefName":"feature","state":"OPEN"}`,
		`{"number":12,"url":"u","reviewDecision":"APPROVED","isDraft":true,"headRefName":"feature","state":"OPEN"}`,
		`{"number":12,"url":"u","isDraft":"false","headRefName":"feature","state":"OPEN"}`,
		`{"number":12,"url":"u","headRefName":"main","state":"OPEN"}`,
		`{"number":12,"url":"u","headRefName":"master","state":"OPEN"}`,
		`{"number":12,"url":"u","headRefName":"trunk","state":"OPEN"}`,
		`{"number":12,"url":"u","headRefName":"feature","state":"MERGED"}`,
		`{"number":12,"url":"u","headRefName":"feature","state":"CLOSED"}`,
		`{"number":0,"url":"","headRefName":"feature","state":"open"}`,
		`{"number":null,"url":null,"headRefName":"feature"}`,
		`{}`, `[]`, `"text"`, `123`, `false`, ` null `, `{bad`, "\ufeff   ",
	} {
		rows = append(rows, prFooterSourceFetch{IsGit: true, Branch: "feature", DefaultBranch: "trunk", Stdout: text})
	}
	rows = append(rows, prFooterSourceFetch{IsGit: false, Branch: "feature", DefaultBranch: "trunk", Stdout: rows[0].Stdout}, prFooterSourceFetch{IsGit: true, Branch: "trunk", DefaultBranch: "trunk", Stdout: rows[0].Stdout}, prFooterSourceFetch{IsGit: true, Branch: "feature", DefaultBranch: "trunk", Stdout: rows[0].Stdout, Code: 1})
	// Preserve the original lower-case input names, independently of Go's
	// parser, so this driver is exercising the extracted Source functions.
	input := []any{}
	for _, row := range rows {
		input = append(input, map[string]any{"isGit": row.IsGit, "branch": row.Branch, "defaultBranch": row.DefaultBranch, "stdout": row.Stdout, "code": row.Code})
	}
	wants := terminalPRSource(t, map[string]any{"fetch": input})
	if len(wants) != len(rows) {
		t.Fatal("Source case inventory", len(wants))
	}
	for i, row := range rows {
		actual := json.RawMessage("null")
		if row.IsGit && row.Branch != row.DefaultBranch && row.Code == 0 && protocol.TrimJavaScriptString(row.Stdout) != "" {
			actual = terminalPRStatusFromGH([]byte(row.Stdout), row.DefaultBranch)
		}
		var got, want any
		_ = json.Unmarshal(actual, &got)
		_ = json.Unmarshal(wants[i].Value, &want)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("case %d actual=%s Source=%s", i, actual, wants[i].Value)
		}
		if row.IsGit && row.Branch != row.DefaultBranch {
			if len(wants[i].Calls) != 1 || string(wants[i].Calls[0][0]) != `"gh"` || string(wants[i].Calls[0][1]) != `["pr","view","--json","number,url,reviewDecision,isDraft,headRefName,state"]` || string(wants[i].Calls[0][2]) != `{"timeout":5000,"preserveOutputOnError":false}` {
				t.Fatal("actual Source argv/options changed", wants[i].Calls)
			}
		} else if len(wants[i].Calls) != 0 {
			t.Fatal("Source skipped repo launched gh")
		}
	}
}

func newTerminalPRProcess(t *testing.T, branch, defaultBranch string) (nativePluginFixture, *MainPrint, string) {
	t.Helper()
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
	if branch != "" {
		f.write(t, filepath.Join(f.cwd, ".git", "HEAD"), "ref: refs/heads/"+branch+"\n")
		f.write(t, filepath.Join(f.cwd, ".git", "refs", "remotes", "origin", "HEAD"), "ref: refs/remotes/origin/"+defaultBranch+"\n")
	}
	bin := filepath.Join(f.home, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	gh := filepath.Join(bin, "gh")
	script := "#!/bin/sh\nprintf '%s\\n' \"$PWD\" \"$HOME\" \"$@\" > \"$PR_NATIVE_RECEIPT\"\nif [ -f \"$PR_NATIVE_HOLD\" ]; then exec /bin/sleep 30; fi\n/bin/cat \"$PR_NATIVE_DATA\"\nexit \"$(/bin/cat \"$PR_NATIVE_CODE\")\"\n"
	if err := os.WriteFile(gh, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"PATH": bin + ":/usr/bin:/bin", "PR_NATIVE_RECEIPT": filepath.Join(f.home, "gh-receipt"), "PR_NATIVE_DATA": filepath.Join(f.home, "gh-data"), "PR_NATIVE_CODE": filepath.Join(f.home, "gh-code"), "PR_NATIVE_HOLD": filepath.Join(f.home, "gh-hold"), "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "DISABLE_AUTOUPDATER": "1", "ANTHROPIC_API_KEY": "private-pr-footer-no-quota"} {
		f.n.Environment[key] = value
	}
	f.write(t, f.n.Environment["PR_NATIVE_DATA"], `{"number":812,"url":"https://example.test/pr/812","reviewDecision":"APPROVED","isDraft":false,"headRefName":"feature","state":"OPEN"}`)
	f.write(t, f.n.Environment["PR_NATIVE_CODE"], "0")
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", PermissionMode: "default", MaxTurns: 100400, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Interactive: true, Version: "fixture", Platform: runtime.GOOS, Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	if err = m.StartSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	return f, m, f.n.Environment["PR_NATIVE_RECEIPT"]
}

func TestTerminalPRStatusSameNativeGitOwnerPhysicalGHAndCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix subprocess fixture; Windows native process is separate")
	}
	for _, row := range []struct {
		branch, def string
		skip        bool
	}{{"feature", "trunk", false}, {"trunk", "trunk", true}, {"", "trunk", true}} {
		t.Run(row.branch, func(t *testing.T) {
			f, m, receipt := newTerminalPRProcess(t, row.branch, row.def)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			value, err := m.FetchTerminalPRStatus(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if row.skip {
				if string(value) != "null" {
					t.Fatal("actual Git skip", string(value))
				}
				if _, err = os.Stat(receipt); !os.IsNotExist(err) {
					t.Fatal("skipped Git spawned gh", err)
				}
				return
			}
			if mainObj(value).String("reviewState") != "approved" {
				t.Fatal("physical gh consumer", string(value))
			}
			data, err := os.ReadFile(receipt)
			want := strings.Join([]string{f.n.CWD, f.home, "pr", "view", "--json", "number,url,reviewDecision,isDraft,headRefName,state", ""}, "\n")
			if err != nil || string(data) != want {
				t.Fatal("actual isolated argv/cwd/home", err, string(data))
			}
			f.write(t, f.n.Environment["PR_NATIVE_CODE"], "1")
			if value, err = m.FetchTerminalPRStatus(ctx); err != nil || string(value) != "null" {
				t.Fatal("nonzero output should be discarded", string(value), err)
			}
			f.write(t, f.n.Environment["PR_NATIVE_HOLD"], "hold")
			started := time.Now()
			if value, err = m.FetchTerminalPRStatus(ctx); err != nil || string(value) != "null" {
				t.Fatal("actual five-second gh timeout", string(value), err)
			}
			if elapsed := time.Since(started); elapsed < 4500*time.Millisecond || elapsed > 8*time.Second {
				t.Fatal("original gh timeout duration", elapsed)
			}
			active, stop := context.WithCancel(ctx)
			finished := make(chan error, 1)
			_ = os.Remove(receipt)
			go func() { _, err := m.FetchTerminalPRStatus(active); finished <- err }()
			// The process receipt is physical evidence that cancellation reaches
			// an actual child, not a pre-admission context shortcut.
			deadline := time.Now().Add(3 * time.Second)
			for {
				if _, err = os.Stat(receipt); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("held gh never launched")
				}
				time.Sleep(10 * time.Millisecond)
			}
			stop()
			select {
			case err := <-finished:
				if err != context.Canceled {
					t.Fatal("physical gh cancellation", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("held gh did not join")
			}
		})
	}
}
