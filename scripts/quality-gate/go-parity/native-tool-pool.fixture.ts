import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
import { mock } from 'bun:test'
import { feature } from 'bun:bundle'

const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
const home=assertDisposableFixtureHome(input.homeDir,'Native final tool pool')
if(process.env.CC_HAHA_NATIVE_TOOL_POOL_FIXTURE!=='1'||process.env.NODE_ENV!=='production')throw new Error('Unowned final tool pool fixture')
const capabilities=await installClientFixtureCapabilities(home,{absentPaths:[...absentManagedSettingsFixturePaths(),'/home/claude/.claude/remote/.api_key','/home/claude/.claude/remote/.oauth_token']})
mock.module('../../../src/utils/secureStorage/keychainPrefetch',()=>({getLegacyApiKeyPrefetchResult:()=>({stdout:''}),getLegacyApiKeyPrefetchPromise:()=>undefined,startKeychainPrefetch:()=>{},clearLegacyApiKeyPrefetch:()=>{}}))
;(globalThis as any).MACRO={BUILD_TIME:''}
await import('../../../preload')
const state=await import('../../../src/bootstrap/state')
state.setCwdState(home);state.setOriginalCwd(home);state.setProjectRoot(home)
state.setAllowedSettingSources(['userSettings','projectSettings','localSettings','policySettings'])
process.env.USER_TYPE='ant';process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH=home+'/managed'
const {getManagedFilePath}=await import('../../../src/utils/settings/managedPath')
if(getManagedFilePath()!==home+'/managed')throw new Error('Unowned managed path')
process.env.USER_TYPE='external'
const {enableConfigs}=await import('../../../src/utils/config');enableConfigs()
const {getTools,assembleToolPool,getAllBaseTools}=await import('../../../src/tools')
const {mergeAndFilterTools}=await import('../../../src/utils/toolPool')
const {buildTool,getEmptyToolPermissionContext}=await import('../../../src/Tool')
const {getMainLoopModel}=await import('../../../src/utils/model/model')
const profiles=[]
for(const row of input.profiles){
  for(const name of ['CLAUDE_CODE_SIMPLE','ENABLE_TOOL_SEARCH','CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS','CLAUDE_CODE_ENABLE_TASKS','CC_HAHA_AGENT_TEAMS_ENABLED','CLAUDE_CODE_DISABLE_WORKFLOWS','CC_HAHA_DESKTOP_SERVER_URL','CC_HAHA_SESSION_COLLABORATION_TOKEN','CC_HAHA_SESSION_ID','CC_HAHA_IMAGE_PROVIDER_KIND','CC_HAHA_IMAGE_PROVIDER_ID','CC_HAHA_IMAGE_BASE_URL','CC_HAHA_IMAGE_API_KEY','CC_HAHA_IMAGE_MODEL'])delete process.env[name]
  Object.assign(process.env,row.env)
  state.setMainLoopModelOverride(row.model??undefined)
  state.setIsInteractive(!row.nonInteractive)
  const permission={...getEmptyToolPermissionContext(),alwaysDenyRules:{cliArg:row.deny??[]}}
  const initial=getTools(permission)
  const mcp=(row.mcp??[]).map((tool:any)=>buildTool({...tool,prompt:async()=>tool.prompt??'',inputJSONSchema:{type:'object',properties:{}}}))
  const assembled=assembleToolPool(permission,mcp)
  // These are the actual original functions and the actual print.ts
  // buildAllTools inputs; no copied sorting/filtering implementation.
  const final=mergeAndFilterTools(initial,assembled,permission.mode)
  profiles.push({model:getMainLoopModel(),allBase:getAllBaseTools().map(t=>t.name),base:initial.map(t=>t.name),assembled:assembled.map(t=>t.name),final:final.map(t=>t.name)})
}
process.stdout.write(JSON.stringify({profiles,capabilities,features:{TRANSCRIPT_CLASSIFIER:feature('TRANSCRIPT_CLASSIFIER')?true:false,AGENT_TRIGGERS:feature('AGENT_TRIGGERS')?true:false,KAIROS:feature('KAIROS')?true:false,KAIROS_BRIEF:feature('KAIROS_BRIEF')?true:false,KAIROS_CHANNELS:feature('KAIROS_CHANNELS')?true:false,FORK_SUBAGENT:feature('FORK_SUBAGENT')?true:false,PROACTIVE:feature('PROACTIVE')?true:false,WEB_BROWSER_TOOL:feature('WEB_BROWSER_TOOL')?true:false,REPL_TOOL:feature('REPL_TOOL')?true:false,REMOTE_TRIGGER:feature('REMOTE_TRIGGER')?true:false,MONITOR_TOOL:feature('MONITOR_TOOL')?true:false}}))
