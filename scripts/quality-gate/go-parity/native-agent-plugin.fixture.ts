import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir,'Plugin Agent')
if(process.env.CC_HAHA_NATIVE_AGENT_PLUGIN_FIXTURE!=='1')throw new Error('Missing plugin Agent fixture')
const capabilities=await installClientFixtureCapabilities(input.homeDir,{absentPaths:absentManagedSettingsFixturePaths()})
if(input.optionsProfile){
 mkdirSync(join(input.homeDir,'.claude'),{recursive:true})
 writeFileSync(join(input.homeDir,'.claude','settings.json'),input.settingsSource)
 writeFileSync(join(input.homeDir,'.claude','.credentials.json'),input.credentialsSource)
 // The default capability fixture supplies an empty secure store. This
 // profile binds the original plaintext implementation to the private files
 // instead; plugin option merging and Agent loading remain original.
 const {mock}=await import('bun:test')
 const {plainTextStorage}=await import('../../../src/utils/secureStorage/plainTextStorage')
 mock.module('../../../src/utils/secureStorage/index.ts',()=>({getSecureStorage:()=>plainTextStorage}))
}
await import('../../../preload')
const state=await import('../../../src/bootstrap/state');state.setCwdState(input.homeDir);state.setOriginalCwd(input.homeDir);state.setProjectRoot(input.homeDir);state.setIsInteractive(false)
const config=await import('../../../src/utils/config');config.enableConfigs()
const {loadAgentFromFile}=await import('../../../src/utils/plugins/loadPluginAgents')
const rows=[],prompts=[]
for(let i=0;i<input.cases.length;i++){
 const row=input.cases[i];process.env.CLAUDE_CODE_DISABLE_AUTO_MEMORY=row.memoryEnabled?'0':'1'
 const file=join(input.homeDir,'plugin','agents','fixture-'+i+'.md');mkdirSync(join(input.homeDir,'plugin','agents'),{recursive:true});writeFileSync(file,row.content)
 const definition=await loadAgentFromFile(file,'sample',['nested'],input.pluginID??'sample@fixture',join(input.homeDir,'plugin'),{name:'sample',...(input.optionsProfile?{userConfig:input.schema}:{})},new Set())
 if(!definition){rows.push(null);continue}
 const {getSystemPrompt,...fields}=definition;rows.push(JSON.stringify(fields));if(input.optionsProfile)prompts.push(getSystemPrompt())
}
process.stdout.write(JSON.stringify({rows,capabilities,...(input.optionsProfile?{prompts}:{})}))
