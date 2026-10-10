package sdkpermissions

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestActualSourceNativeMCPRulesRetainParserFieldsAndRequireBindingProof(t *testing.T) {
	cases := [][]string{{"mcp__computer-use__js", "mcp__computer-use__js_reset"}, {" Read , mcp__server__tool ", "mcp__server__tool(a,b c)"}, {"mcp__server", "mcp__server__*"}, {"mcp__server__tool\t"}, {"\uFEFFmcp__server__tool\u00a0"}}
	bun := os.Getenv("CC_HAHA_PINNED_BUN")
	if bun == "" {
		t.Skip("actual Source pinned Bun required")
	}
	root, _ := filepath.Abs("../../..")
	home := t.TempDir()
	file := filepath.Join(home, "source.ts")
	script := `globalThis.MACRO={VERSION:'fixture',BUILD_TIME:'',PACKAGE_URL:'',README_URL:'',ISSUES_URL:''};const root=Bun.argv[2];const input=await Bun.stdin.json();const source=await import(root+'/src/utils/permissions/permissionSetup.ts');console.log(JSON.stringify(input.map(source.parseToolListFromCLI)))`
	_ = os.WriteFile(file, []byte(script), 0600)
	command := exec.Command(bun, file, root)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "NODE_ENV=test", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=" + os.Getenv("PATH")}
	encoded, _ := json.Marshal(cases)
	command.Stdin = bytes.NewReader(encoded)
	output, e := command.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %s %v", output, e)
	}
	var expected [][]string
	if e := json.Unmarshal(output, &expected); e != nil {
		t.Fatalf("Source %s %v", output, e)
	}
	for i, input := range cases {
		actual, e := nativeToolList(input, true)
		if e != nil || !reflect.DeepEqual(actual, expected[i]) {
			t.Fatalf("Source=%v Go=%v %v", expected[i], actual, e)
		}
		if _, e := nativeToolList(input); e == nil {
			t.Fatal("unbound MCP producer accepted")
		}
	}
	owner, settings, gate := setup(t, "")
	if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		state, e := Initialize(scope, owner, settings, gate, Input{Profile: PublicPrintProfile, NativeProcess: true, NativeMCPBindings: true, Platform: "darwin", Model: "real-source-model", NonInteractive: true, BaseToolsCLI: []string{"Read"}, Allowed: cases[0], Mode: "default"})
		if e != nil {
			return e
		}
		_, ctx, e := state.Read(scope)
		if e != nil {
			return e
		}
		var rules map[string][]string
		_ = json.Unmarshal(ctx.Get("alwaysAllowRules"), &rules)
		if !reflect.DeepEqual(rules["cliArg"], cases[0]) || ctx.String("mode") != "default" {
			t.Fatal("MCP permission source changed", ctx)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if _, e := nativeToolList([]string{"UnportedBuiltin"}, true); e == nil {
		t.Fatal("MCP proof enabled unported builtin")
	}
}
