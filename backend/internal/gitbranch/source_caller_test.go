package gitbranch

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// The archived Source fixture has a legitimate 7/4 native interleaving (537).
// This new caller awaits only two corresponding first missing-ref callbacks.
// Source helpers, native watchFile, input profile and strict golden stay intact.
func TestActualSourceControlledMissingRefCallerKeepsStrictContract(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual pinned Source Bun required")
	}
	raw, err := os.ReadFile("testdata/source_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Original                                 originalProfile
		Values, AsyncReads, ClockCalls, CWDReads json.RawMessage
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	var expected struct {
		Original                                 originalProfile
		Values, AsyncReads, ClockCalls, CWDReads json.RawMessage
	}
	var sourceCases []struct{ Original json.RawMessage }
	if err = json.Unmarshal(raw, &sourceCases); err != nil {
		t.Fatal(err)
	}
	var sourceProfile json.RawMessage
	for index, c := range cases {
		if c.Original.Name == "watch-head-dirty" {
			expected = c
			sourceProfile = sourceCases[index].Original
		}
	}
	if expected.Original.Name == "" {
		t.Fatal("original profile absent")
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-gitbranch-contract.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		home, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if err = os.MkdirAll(filepath.Join(home, expected.Original.CWD), 0700); err != nil {
			t.Fatal(err)
		}
		for path, raw := range expected.Original.Files {
			var data string
			if json.Unmarshal(raw, &data) != nil {
				t.Fatal("expected original string fixture")
			}
			target := filepath.Join(home, path)
			if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(target, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
		cmd.Dir = home
		cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_GIT_BRANCH_FIXTURE=1", "NODE_ENV=production", "USER_TYPE=external", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1"}
		input, _ := json.Marshal(map[string]any{"home": home, "profile": sourceProfile})
		cmd.Stdin = bytes.NewReader(input)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		stdout, err := cmd.Output()
		cancel()
		if err != nil {
			t.Fatal(err, stderr.String())
		}
		var source map[string]json.RawMessage
		if err = json.Unmarshal(stdout, &source); err != nil {
			t.Fatal(err, string(stdout))
		}
		for key, want := range map[string]json.RawMessage{"values": expected.Values, "asyncReads": expected.AsyncReads, "clockCalls": expected.ClockCalls, "cwdReads": expected.CWDReads} {
			gotCompact, wantCompact := new(bytes.Buffer), new(bytes.Buffer)
			if err = json.Compact(gotCompact, source[key]); err != nil {
				t.Fatal(err)
			}
			if err = json.Compact(wantCompact, want); err != nil {
				t.Fatal(err)
			}
			if gotCompact.String() != wantCompact.String() {
				t.Fatalf("actual Source strict %s mismatch\nwant %s\ngot %s", key, wantCompact, gotCompact)
			}
		}
		var caps struct{ Blocked, SecureReads []json.RawMessage }
		if err = json.Unmarshal(source["caps"], &caps); err != nil {
			t.Fatal(err)
		}
		if len(caps.Blocked) != 0 || len(caps.SecureReads) != 0 {
			t.Fatal("Source fixture escaped native/private boundary", string(source["caps"]))
		}
		var watchTrace []struct {
			Path, Phase    string
			InitialMissing bool
		}
		if err = json.Unmarshal(source["watchTrace"], &watchTrace); err != nil {
			t.Fatal("native watch callback evidence missing", err)
		}
		var callbacks []string
		for _, event := range watchTrace {
			if event.Phase != "native-callback" {
				continue
			}
			if event.Path != "work/.git/HEAD" && !event.InitialMissing {
				t.Fatal("missing-ref observer did not retain actual zero-link native stat pair", event)
			}
			callbacks = append(callbacks, event.Path)
		}
		wantCallbacks, _ := json.Marshal([]string{"work/.git/refs/heads/first", "work/.git/HEAD", "work/.git/refs/heads/second"})
		gotCallbacks, _ := json.Marshal(callbacks)
		if !bytes.Equal(gotCallbacks, wantCallbacks) {
			t.Fatal("original callback order differed", string(source["watchTrace"]))
		}
	}
	// The Go side is already exact-checked in TestActualSourceOriginalContracts;
	// execute its original case here too, using the identical new caller control.
	t.Run("same-Go-original-contract", func(t *testing.T) { TestActualSourceOriginalContracts(t) })
}
