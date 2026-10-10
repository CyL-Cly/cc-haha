package skillwatcher

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

// Both sides watch real temporary files. Only the external hook/cache/signal
// consumers are replaced to observe the original detector's orchestration.
func TestActualSourceFileDetectorBatchBlockDynamicAndCleanup(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	root := t.TempDir()
	config := filepath.Join(root, ".claude")
	_ = os.MkdirAll(config, 0700)
	script := `import {mock} from 'bun:test';import * as fs from 'node:fs';import {join} from 'node:path';const repo=Bun.argv[2],home=process.env.HOME,file=join(process.env.CLAUDE_CONFIG_DIR,'cc-haha/computer-use-config.json');fs.mkdirSync(join(process.env.CLAUDE_CONFIG_DIR,'cc-haha'),{recursive:true});fs.writeFileSync(file,'{"enabled":false}');const trace=[];let blocked=false,dynamic;mock.module(repo+'/src/utils/computerUse/preauthorizedConfig.ts',()=>({getComputerUseConfigPath:()=>file}));mock.module(repo+'/src/bootstrap/state.ts',()=>({getAdditionalDirectoriesForClaudeMd:()=>[]}));mock.module(repo+'/src/commands.ts',()=>({clearCommandMemoizationCaches:()=>trace.push('memo'),clearCommandsCache:()=>trace.push('commands')}));mock.module(repo+'/src/services/analytics/index.ts',()=>({logEvent:()=>{}}));mock.module(repo+'/src/skills/loadSkillsDir.ts',()=>({clearSkillCaches:()=>trace.push('skills'),getSkillsPath:(source,kind)=>source==='userSettings'?join(process.env.CLAUDE_CONFIG_DIR,kind):join(home,'.claude-project',kind),onDynamicSkillsLoaded:cb=>{dynamic=cb}}));mock.module(repo+'/src/skills/skillRoots.ts',()=>({AGENT_SKILLS_DIR:'.agents',getUserAgentSkillsDir:()=>join(home,'.agents/skills'),isAgentSkillsDirectoryEnabled:()=>false}));mock.module(repo+'/src/utils/attachments.ts',()=>({resetSentSkillNames:()=>trace.push('sent')}));mock.module(repo+'/src/utils/cleanupRegistry.ts',()=>({registerCleanup:()=>()=>{}}));mock.module(repo+'/src/utils/debug.ts',()=>({logForDebugging:()=>{}}));mock.module(repo+'/src/utils/fsOperations.ts',()=>({getFsImplementation:()=>fs.promises}));mock.module(repo+'/src/utils/hooks.ts',()=>({executeConfigChangeHooks:async(source,path)=>{trace.push('hook:'+source+':'+(path===file?'config':'other'));return [{blocked}]},hasBlockingResult:results=>results.some(r=>r.blocked)}));const detector=await import(repo+'/src/utils/skills/skillChangeDetector.ts');await detector.resetForTesting({stabilityThreshold:50,pollInterval:10,reloadDebounce:30,chokidarInterval:10});detector.subscribe(()=>trace.push('notify'));await detector.initialize();const sleep=ms=>new Promise(r=>setTimeout(r,ms));const wait=async n=>{const end=Date.now()+3000;while(trace.length<n){if(Date.now()>end)throw Error('timeout '+JSON.stringify(trace));await sleep(5)}};await sleep(150);for(let i=0;i<4;i++){fs.writeFileSync(file,JSON.stringify({enabled:true,write:i}));await sleep(12)}await wait(5);await sleep(80);blocked=true;fs.writeFileSync(file,'{"enabled":false}');await wait(6);await sleep(80);blocked=false;fs.writeFileSync(file,'{"enabled":true}');await wait(11);await sleep(80);dynamic();await wait(13);fs.unlinkSync(file);await wait(18);await sleep(80);fs.writeFileSync(file,'{"enabled":false}');await wait(23);await sleep(80);await detector.dispose();fs.writeFileSync(file,'{"enabled":true}');await sleep(150);console.log(JSON.stringify(trace));`
	file := filepath.Join(root, "source.ts")
	_ = os.WriteFile(file, []byte(script), 0600)
	repo, _ := filepath.Abs("../../..")
	command := exec.Command(bun, file, repo)
	command.Dir = root
	command.Env = []string{"HOME=" + root, "USERPROFILE=" + root, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + root, "NODE_ENV=test", "DISABLE_TELEMETRY=1", "PATH=" + os.Getenv("PATH")}
	output, e := command.CombinedOutput()
	if e != nil {
		t.Fatalf("actual Source: %v %s", e, output)
	}
	var expected []string
	if e := json.Unmarshal(output, &expected); e != nil {
		t.Fatalf("Source output %s: %v", output, e)
	}
	configFile := filepath.Join(root, "go-config.json")
	_ = os.WriteFile(configFile, []byte(`{"enabled":false}`), 0600)
	var mu sync.Mutex
	trace := []string{}
	blocked := false
	w, e := Start(Options{Paths: []string{configFile}, Timing: &fixtureTiming, Reload: func(_ context.Context, _ string, dynamic bool) error {
		mu.Lock()
		defer mu.Unlock()
		if dynamic {
			trace = append(trace, "memo", "notify")
			return nil
		}
		trace = append(trace, "hook:skills:config")
		if !blocked {
			trace = append(trace, "skills", "commands", "sent", "notify")
		}
		return nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	defer w.Close()
	count := func() int { mu.Lock(); defer mu.Unlock(); return len(trace) }
	for i := 0; i < 4; i++ {
		_ = os.WriteFile(configFile, []byte(`{"enabled":true,"write":`+string(rune('0'+i))+`}`), 0600)
		time.Sleep(12 * time.Millisecond)
	}
	waitFor(t, func() bool { return count() == 5 })
	time.Sleep(80 * time.Millisecond)
	mu.Lock()
	blocked = true
	mu.Unlock()
	_ = os.WriteFile(configFile, []byte(`{"enabled":false}`), 0600)
	waitFor(t, func() bool { return count() == 6 })
	time.Sleep(80 * time.Millisecond)
	mu.Lock()
	blocked = false
	mu.Unlock()
	_ = os.WriteFile(configFile, []byte(`{"enabled":true}`), 0600)
	waitFor(t, func() bool { return count() == 11 })
	time.Sleep(80 * time.Millisecond)
	w.NotifyDynamic()
	waitFor(t, func() bool { return count() == 13 })
	_ = os.Remove(configFile)
	waitFor(t, func() bool { return count() == 18 })
	time.Sleep(80 * time.Millisecond)
	_ = os.WriteFile(configFile, []byte(`{"enabled":false}`), 0600)
	waitFor(t, func() bool { return count() == 23 })
	time.Sleep(80 * time.Millisecond)
	w.Close()
	_ = os.WriteFile(configFile, []byte(`{"enabled":true}`), 0600)
	time.Sleep(150 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(trace, expected) {
		t.Fatalf("Source=%s\nGo=%v", output, trace)
	}
}
