package clibootstrap

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestActualSourceBundledComputerUseDeclarationPromptAndSkillGate(t *testing.T) {
	for _, platform := range []string{"darwin", "win32"} {
		t.Run(platform, func(t *testing.T) { testSourceBundledComputerUseDeclarationPromptAndSkillGate(t, platform) })
	}
}
func testSourceBundledComputerUseDeclarationPromptAndSkillGate(t *testing.T, platform string) {
	bun := os.Getenv("CC_HAHA_PINNED_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	f := newNativePluginFixture(t)
	_ = os.MkdirAll(f.n.Temp, 0700)
	configs := []string{`{"enabled":true,"future":{"keep":1}}`, `{"enabled":false}`, `{}`, `{"enabled":null}`, `{"enabled":true}`, `not JSON`, `null`, `{"enabled":"truthy Source gate"}`}
	envs := []string{"", "", "", "", "OFF", "", "", ""}
	args := []string{"", "literal $ARGUMENTS ${CLAUDE_SESSION_ID}", "\n task \n"}
	inputs := []map[string]string{}
	for i, raw := range configs {
		inputs = append(inputs, map[string]string{"raw": raw, "flag": envs[i]})
	}
	script := `import * as fs from 'node:fs';import {join} from 'node:path';globalThis.MACRO={VERSION:'fixture',BUILD_TIME:'',PACKAGE_URL:'',README_URL:'',ISSUES_URL:''};const root=Bun.argv[2];const input=await Bun.stdin.json();Object.defineProperty(process,'platform',{value:input.platform});const bundled=await import(root+'/src/skills/bundledSkills.ts');const cu=await import(root+'/src/skills/bundled/computerUse.ts');const gate=await import(root+'/src/utils/computerUse/skillGate.ts');cu.registerComputerUseSkill();const skill=bundled.getBundledSkills().find(s=>s.name==='computer-use');const {getPromptForCommand,isEnabled,...metadata}=skill;const enabled=[];for(const c of input.configs){delete process.env.CLAUDE_COMPUTER_USE_ENABLED;if(c.flag)process.env.CLAUDE_COMPUTER_USE_ENABLED=c.flag;fs.mkdirSync(join(process.env.CLAUDE_CONFIG_DIR,'cc-haha'),{recursive:true});fs.writeFileSync(join(process.env.CLAUDE_CONFIG_DIR,'cc-haha','computer-use-config.json'),c.raw);gate.invalidateComputerUseSkillGate();enabled.push(Boolean(isEnabled()))};const prompts=[];for(const args of input.args)prompts.push((await getPromptForCommand(args,{}))[0].text);console.log(JSON.stringify({metadata,prompts,enabled}))`
	root, _ := filepath.Abs("../../..")
	file := filepath.Join(f.home, "source.ts")
	_ = os.WriteFile(file, []byte(script), 0600)
	command := exec.Command(bun, file, root)
	command.Dir = f.home
	command.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "NODE_ENV=test", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=" + os.Getenv("PATH")}
	encoded, _ := json.Marshal(map[string]any{"configs": inputs, "args": args, "platform": platform})
	command.Stdin = strings.NewReader(string(encoded))
	output, e := command.CombinedOutput()
	if e != nil {
		t.Fatalf("Source=%s %v", output, e)
	}
	var expected any
	if e := json.Unmarshal(output, &expected); e != nil {
		t.Fatalf("Source=%s %v", output, e)
	}
	goPlatform := platform
	if platform == "win32" {
		goPlatform = "windows"
	}
	m := &MainPrint{options: MainPrintOptions{Native: f.n, Platform: goPlatform}}
	f.write(t, m.computerUseConfigPath(), configs[0])
	definition, enabled, e := m.nativeComputerUseSkill()
	if e != nil || !enabled {
		t.Fatal(enabled, e)
	}
	prompts := []string{}
	for _, args := range args {
		prompt, e := definition.Prompt(context.Background(), &args, skillcommands.Invocation{})
		if e != nil {
			t.Fatal(e)
		}
		prompts = append(prompts, prompt)
	}
	gates := []bool{}
	for i, raw := range configs {
		f.write(t, m.computerUseConfigPath(), raw)
		f.n.Environment["CLAUDE_COMPUTER_USE_ENABLED"] = envs[i]
		m.invalidateComputerUseSkillGate()
		gates = append(gates, m.computerUseSkillEnabled())
	}
	actual := map[string]any{"metadata": definition.Metadata(), "prompts": prompts, "enabled": gates}
	encoded, _ = json.Marshal(actual)
	var got any
	_ = json.Unmarshal(encoded, &got)
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("Source=%s\nGo=%s", output, encoded)
	}
}

func TestComputerUseSkillGateRetainsSourceTTLAndExplicitReloadInvalidation(t *testing.T) {
	f := newNativePluginFixture(t)
	m := &MainPrint{options: MainPrintOptions{Native: f.n, Platform: "darwin"}}
	now := float64(5000)
	f.n.NowMillis = func() float64 { return now }
	f.write(t, m.computerUseConfigPath(), `{"enabled":true}`)
	if !m.computerUseSkillEnabled() {
		t.Fatal("enabled gate")
	}
	f.write(t, m.computerUseConfigPath(), `{"enabled":false}`)
	now = 7999
	if !m.computerUseSkillEnabled() {
		t.Fatal("Source 3s cached gate changed early")
	}
	now = 8000
	if m.computerUseSkillEnabled() {
		t.Fatal("Source TTL did not reread actual configuration")
	}
	f.write(t, m.computerUseConfigPath(), `{"enabled":true}`)
	m.invalidateComputerUseSkillGate()
	if !m.computerUseSkillEnabled() {
		t.Fatal("explicit reload did not invalidate Source gate")
	}
	f.n.Environment["CLAUDE_COMPUTER_USE_ENABLED"] = " \uFEFFOFF\u00a0"
	m.invalidateComputerUseSkillGate()
	if m.computerUseSkillEnabled() {
		t.Fatal("JavaScript env whitespace kill switch was ignored")
	}
}
