package sdkpermissions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestActualSourcePowerShellCLIListParserAndNativeKnownToolAdmission(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-powershell-policy.fixture.ts")
	rows := [][]string{{"PowerShell"}, {"PowerShell,Read"}, {"PowerShell(Get-Process:*)"}, {" PowerShell(Set-Content private, literal),Read "}, {"\uFEFFPowerShell\u00a0"}, {`PowerShell(Get-Content '中文😀')`}}
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CLAUDE_CODE_TMPDIR=" + home, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_POWERSHELL_POLICY_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"home": home, "toolLists": rows})
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var source struct{ ToolLists [][]string }
	if err = json.Unmarshal(out, &source); err != nil {
		t.Fatal(err, string(out))
	}
	if len(source.ToolLists) != len(rows) {
		t.Fatal("incomplete original CLI tool-list receipt")
	}
	for i, row := range rows {
		got, err := nativeToolList(row)
		if err != nil || !reflect.DeepEqual(got, source.ToolLists[i]) {
			t.Fatalf("PowerShell tool admission row %d Go=%v err=%v Source=%v", i, got, err, source.ToolLists[i])
		}
	}
	owner, settings, gate := setup(t, "")
	err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		state, err := Initialize(scope, owner, settings, gate, Input{Profile: PublicPrintProfile, NativeProcess: true, Platform: "windows", Model: "source-known-model", NonInteractive: true, BaseToolsCLI: []string{"PowerShell"}, Allowed: []string{"PowerShell(Set-Content:*)"}, Mode: "default"})
		if err != nil {
			return err
		}
		_, context, err := state.Read(scope)
		if err != nil {
			return err
		}
		var rules map[string][]string
		if err = json.Unmarshal(context.Get("alwaysAllowRules"), &rules); err != nil {
			return err
		}
		if !reflect.DeepEqual(rules["cliArg"], []string{"PowerShell(Set-Content:*)"}) {
			return fmt.Errorf("actual native startup lost PowerShell CLI rule %v", rules)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := nativeToolList([]string{"UnimplementedFutureTool"}); err == nil {
		t.Fatal("known PowerShell admission broadened unknown tools")
	}
}
