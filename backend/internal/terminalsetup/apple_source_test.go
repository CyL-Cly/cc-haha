package terminalsetup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const appleSeed = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>Window Settings</key><dict>
<key>Basic</key><dict><key>useOptionAsMetaKey</key><false/><key>Bell</key><true/></dict>
<key>Red Sands</key><dict></dict></dict></dict></plist>
`

type appleCase struct {
	Fault   string `json:"fault"`
	Startup string `json:"startup"`
}
type appleInvocation struct {
	Name string   `json:"name"`
	Args []string `json:"args"`
}

func TestApplePrivatePreferenceCommandFixtureProcess(t *testing.T) {
	encoded := os.Getenv("CC_HAHA_APPLE_CASE")
	if encoded == "" {
		t.Skip("private preference fixture executable")
	}
	var profile appleCase
	if json.Unmarshal([]byte(encoded), &profile) != nil {
		os.Exit(81)
	}
	index := 0
	for index < len(os.Args) && os.Args[index] != "--" {
		index++
	}
	if index+2 > len(os.Args) {
		os.Exit(82)
	}
	name, args := os.Args[index+1], os.Args[index+2:]
	home := os.Getenv("HOME")
	path := filepath.Join(home, "Library", "Preferences", "com.apple.Terminal.plist")
	switch name {
	case "defaults":
		if len(args) < 3 || args[1] != "com.apple.Terminal" {
			os.Exit(83)
		}
		switch args[0] {
		case "export":
			if args[2] != path && args[2] != path+".bak" {
				os.Exit(84)
			}
			if profile.Fault == "export" || profile.Fault == "backup-export" && strings.HasSuffix(args[2], ".bak") {
				os.Exit(1)
			}
			_ = os.MkdirAll(filepath.Dir(path), 0700)
			if os.WriteFile(args[2], []byte(appleSeed), 0600) != nil {
				os.Exit(85)
			}
		case "read":
			if args[2] == "Default Window Settings" {
				if profile.Fault == "default-read" || profile.Fault == "restore-import" || profile.Fault == "backup-export" {
					os.Exit(1)
				}
				fmt.Print("  Basic \n")
			} else if args[2] == "Startup Window Settings" {
				fmt.Print(profile.Startup + "\n")
			} else {
				os.Exit(86)
			}
		case "import":
			if args[2] != path+".bak" {
				os.Exit(87)
			}
			if profile.Fault == "restore-import" {
				os.Exit(1)
			}
			data, err := os.ReadFile(args[2])
			if err != nil || os.WriteFile(path, data, 0600) != nil {
				os.Exit(88)
			}
		default:
			os.Exit(89)
		}
	case "killall":
		if len(args) != 1 || args[0] != "cfprefsd" {
			os.Exit(90)
		}
		if profile.Fault == "killall" {
			os.Exit(1)
		}
	case "PlistBuddy":
		if len(args) != 3 || args[0] != "-c" || args[2] != path {
			os.Exit(91)
		}
		if profile.Fault == "properties" || profile.Fault == "meta" && strings.Contains(args[1], "useOptionAsMetaKey") {
			os.Exit(1)
		}
		// Only this physical, owned plist reaches the real macOS parser.
		command := exec.Command("/usr/libexec/PlistBuddy", args...)
		command.Stdout, command.Stderr = os.Stdout, os.Stderr
		capturePublicGoCoverage(t, command)
		if err := command.Run(); err != nil {
			if exit, ok := err.(*exec.ExitError); ok {
				os.Exit(exit.ExitCode())
			}
			os.Exit(92)
		}
	default:
		os.Exit(93)
	}
	os.Exit(0)
}

func TestWholeOriginalAppleTerminalPrivatePlistBackupRecoveryAndProfileConsumers(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("physical macOS PlistBuddy parser")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range []appleCase{{"", "Basic"}, {"", "Red Sands"}, {"meta", "Red Sands"}, {"properties", "Basic"}, {"default-read", "Basic"}, {"restore-import", "Basic"}, {"export", "Basic"}, {"backup-export", "Basic"}, {"killall", "Basic"}} {
		t.Run(profile.Fault+"/"+profile.Startup, func(t *testing.T) {
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			privateBin := filepath.Join(home, "privatebin")
			if err = os.MkdirAll(privateBin, 0700); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(binary)
			if err != nil {
				t.Fatal(err)
			}
			privateBinary := filepath.Join(privateBin, "fixture.test")
			if err = os.WriteFile(privateBinary, data, 0700); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"defaults", "killall", "PlistBuddy"} {
				text := "#!/bin/sh\nexport GORACE=atexit_sleep_ms=0\nexec '" + strings.ReplaceAll(privateBinary, "'", "'\"'\"'") + "' -test.run=^TestApplePrivatePreferenceCommandFixtureProcess$ -test.count=1 -- " + name + " \"$@\"\n"
				if err = os.WriteFile(filepath.Join(privateBin, name), []byte(text), 0700); err != nil {
					t.Fatal(err)
				}
			}
			data = terminalSetupSource(t, home, map[string]any{"terminal": "Apple_Terminal", "platform": "darwin", "apple": profile, "global": map[string]any{"shiftEnterKeyBindingInstalled": false}})
			var source struct {
				Done       [][]string
				Error      *string
				Files      map[string]string
				Executions []appleInvocation
				Errors     []string
				Global     protocol.JSONObject
				Onboarding int
			}
			if err = json.Unmarshal(data, &source); err != nil {
				t.Fatalf("original Apple Source %v %.5000s", err, data)
			}
			if err = os.RemoveAll(filepath.Join(home, "Library", "Preferences")); err != nil {
				t.Fatal(err)
			}
			global := protocol.NewJSONObject(protocol.JSONField{Name: "shiftEnterKeyBindingInstalled", Value: json.RawMessage("false")}, protocol.JSONField{Name: "retainedSetupFixture", Value: json.RawMessage(`{"value":"unknown old field"}`)})
			var executions []appleInvocation
			logged := []string{}
			onboarding := 0
			encoded, _ := json.Marshal(profile)
			execute := func(ctx context.Context, name string, args []string) Execution {
				executions = append(executions, appleInvocation{name, args})
				command := exec.CommandContext(ctx, filepath.Join(privateBin, filepath.Base(name)), args...)
				command.Dir = home
				command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "CC_HAHA_APPLE_CASE=" + string(encoded)}
				var stdout, stderr bytes.Buffer
				command.Stdout, command.Stderr = &stdout, &stderr
				code := 0
				capturePublicGoCoverage(t, command)
				if err := command.Run(); err != nil {
					code = 1
					if exit, ok := err.(*exec.ExitError); ok {
						code = exit.ExitCode()
					}
				}
				return Execution{code, stdout.String(), stderr.String()}
			}
			output, actualError := Run(context.Background(), Options{Home: home, Terminal: "Apple_Terminal", Platform: "darwin", Files: osFiles{}, Execute: execute, Stat: os.Stat, SaveFields: func(fields protocol.JSONObject) error { global.Assign(fields); return nil }, GetFields: func() (protocol.JSONObject, error) { return global, nil }, Onboarding: func() error { onboarding++; return nil }, LogError: func(err error) { logged = append(logged, err.Error()) }})
			if source.Error != nil {
				if actualError == nil || actualError.Error() != *source.Error {
					t.Fatalf("original error%q Go%v", *source.Error, actualError)
				}
			} else if actualError != nil || len(source.Done) != 1 || output != source.Done[0][0] {
				t.Fatalf("original%+v Go%q err%v", source.Done, output, actualError)
			}
			for _, field := range []string{"retainedSetupFixture", "shiftEnterKeyBindingInstalled", "optionAsMetaKeyInstalled", "appleTerminalSetupInProgress", "appleTerminalBackupPath"} {
				if !bytes.Equal(global.Get(field), source.Global.Get(field)) {
					t.Errorf("global %s Go%s Source%s", field, global.Get(field), source.Global.Get(field))
				}
			}
			for name, expected := range source.Files {
				actual, err := os.ReadFile(filepath.Join(home, name))
				if err != nil || string(actual) != expected {
					t.Errorf("physical plist%q Go%.1000s Source%.1000s err%v", name, actual, expected, err)
				}
			}
			if !reflect.DeepEqual(executions, source.Executions) || !reflect.DeepEqual(logged, source.Errors) || onboarding != source.Onboarding {
				t.Errorf("actual process flow Go%+v Source%+v; logs%+v/%+v onboarding%d/%d", executions, source.Executions, logged, source.Errors, onboarding, source.Onboarding)
			}
		})
	}
}
