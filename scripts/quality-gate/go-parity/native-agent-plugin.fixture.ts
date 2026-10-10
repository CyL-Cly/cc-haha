import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir,'Plugin Agent')
if(process.env.CC_HAHA_NATIVE_AGENT_PLUGIN_FIXTURE!=='1')throw new Error('Missing plugin Agent fixture')
const capabilities=await installClientFixtureCapabilities(input.homeDir,{absentPaths:absentManagedSettingsFixturePaths()})
await import('../../../preload')
const state=await import('../../../src/bootstrap/state');state.setCwdState(input.homeDir);state.setOriginalCwd(input.homeDir);state.setProjectRoot(input.homeDir);state.setIsInteractive(false)
const config=await import('../../../src/utils/config');config.enableConfigs()
const {loadAgentFromFile}=await import('../../../src/utils/plugins/loadPluginAgents')
const rows=[]
for(let i=0;i<input.cases.length;i++){
 const row=input.cases[i];process.env.CLAUDE_CODE_DISABLE_AUTO_MEMORY=row.memoryEnabled?'0':'1'
 const file=join(input.homeDir,'plugin','agents','fixture-'+i+'.md');mkdirSync(join(input.homeDir,'plugin','agents'),{recursive:true});writeFileSync(file,row.content)
 const definition=await loadAgentFromFile(file,'sample',['nested'],'sample@fixture',join(input.homeDir,'plugin'),{name:'sample'},new Set())
 if(!definition){rows.push(null);continue}
 const {getSystemPrompt,...fields}=definition;rows.push(JSON.stringify(fields))
}
process.stdout.write(JSON.stringify({rows,capabilities}))
