package clibootstrap

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

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestTerminalIDEDiffWSLOriginalConverterPhysicalSuccessFailureAndEmptyPath(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	for _, profile := range []string{"success", "failure", "missing"} {
		t.Run(profile, func(t *testing.T) {
			home := t.TempDir()
			bin := filepath.Join(home, "bin")
			_ = os.MkdirAll(bin, 0700)
			receipt := filepath.Join(home, "actual-wslpath-arguments.txt")
			if profile != "missing" {
				script := "#!/bin/sh\nprintf '%s\\n' \"$1\" \"$2\" >> \"$WSL_RECEIPT\"\n"
				if profile == "success" {
					script += "printf '  C:\\\\owned\\\\actual.txt\\n'\n"
				} else {
					script += "exit 17\n"
				}
				if err := os.WriteFile(filepath.Join(bin, "wslpath"), []byte(script), 0700); err != nil {
					t.Fatal(err)
				}
			}
			paths := []string{"", "/mnt/c/owned/actual.txt", "/mnt/c/owned/space name.txt"}
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-ide-wsl.fixture.ts")
			cmd := exec.Command(bun, "--no-env-file", fixture)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=" + bin, "WSL_RECEIPT=" + receipt, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_IDE_WSL_SOURCE=1", "BUN_OPTIONS=--no-env-file"}
			cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"home": home, "paths": paths}))
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("original IDE converter %v %.3000s", err, output)
			}
			var want []string
			if err = json.Unmarshal(output, &want); err != nil {
				t.Fatal(err, string(output))
			}
			sourceCalls, _ := os.ReadFile(receipt)
			_ = os.Remove(receipt)
			native := &NativeBoundary{Home: home, CWD: home, roots: []string{home}, Environment: map[string]string{"PATH": bin, "HOME": home, "WSL_RECEIPT": receipt}}
			actual := []string{}
			for _, path := range paths {
				actual = append(actual, terminalIDEWindowsPath(context.Background(), native, path))
			}
			actualCalls, _ := os.ReadFile(receipt)
			if !reflect.DeepEqual(actual, want) || string(actualCalls) != string(sourceCalls) {
				t.Fatal("actual child process conversion differs from original converter", actual, want, string(actualCalls), string(sourceCalls))
			}
			if profile != "missing" && strings.Count(string(actualCalls), "-w\n") != 2 {
				t.Fatal("empty path invoked a physical converter", string(actualCalls))
			}
			if profile == "success" && protocol.TrimJavaScriptString(actual[1]) != actual[1] {
				t.Fatal("Source trim was not applied", actual)
			}
		})
	}
}
