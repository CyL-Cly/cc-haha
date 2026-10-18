package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestTerminalPluginBrowserActualSourceURLGuardOSArgumentsAndPhysicalChild(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	for _, profile := range []string{"darwin", "linux", "win32", "custom-darwin", "custom-win32", "failure"} {
		t.Run(profile, func(t *testing.T) {
			platform := profile
			if profile == "custom-darwin" || profile == "custom-win32" {
				platform = profile[len("custom-"):]
			}
			if profile == "failure" {
				platform = "darwin"
			}
			var home, bin, receipt string
			main := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", func(native *NativeBoundary) {
				home = native.Home
				bin, receipt = filepath.Join(home, "plugin-browser-bin"), filepath.Join(home, "plugin-browser-actual-argv")
				_ = os.MkdirAll(bin, 0700)
				script := "#!/bin/sh\nprintf '%s\\000' \"${0##*/}\" \"$@\" >> \"$PLUGIN_BROWSER_RECEIPT\"\n"
				if profile == "failure" {
					script += "exit 17\n"
				}
				for _, name := range []string{"open", "xdg-open", "rundll32", "custom-browser"} {
					if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0700); err != nil {
						t.Fatal(err)
					}
				}
				native.Environment["PATH"] = bin
				native.Environment["BROWSER"] = ""
				if profile == "custom-darwin" || profile == "custom-win32" {
					native.Environment["BROWSER"] = filepath.Join(bin, "custom-browser")
				}
				native.Environment["PLUGIN_BROWSER_RECEIPT"] = receipt
			}, func(o *MainPrintOptions) {
				o.Interactive = true
				o.Platform = platform
				if platform == "win32" {
					o.Platform = "windows"
				}
			})
			addresses := []string{"http://127.0.0.1:1/private plugin?q=a&b=x", "https://owned.invalid/repository", "not-an-url", "file:///owned/private", "javascript:must-not-run", "https:owned.invalid/source-spelling", " HTTP://owned.invalid/trimmed-validation "}
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-plugin-browser.fixture.ts")
			cmd := exec.Command(bun, "--no-env-file", fixture)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=" + bin, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_PLUGIN_BROWSER_SOURCE=1", "BUN_OPTIONS=--no-env-file"}
			cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"home": home, "platform": platform, "environment": main.options.Native.Environment, "addresses": addresses}))
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("original browser %v %.3000s", err, output)
			}
			var want []bool
			if err = json.Unmarshal(output, &want); err != nil {
				t.Fatal(err, string(output))
			}
			originalCalls, _ := os.ReadFile(receipt)
			_ = os.Remove(receipt)
			actual := []bool{}
			for _, address := range addresses {
				opened, err := main.OpenTerminalPluginLink(context.Background(), address)
				if err != nil {
					t.Fatal(err)
				}
				actual = append(actual, opened)
			}
			actualCalls, _ := os.ReadFile(receipt)
			if !reflect.DeepEqual(actual, want) || string(actualCalls) != string(originalCalls) {
				t.Fatal("actual URL/OS child consumer differs from Source", actual, want, string(actualCalls), string(originalCalls))
			}
			main.Close()
		})
	}
}
