import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir,'Agent runtime context')
if(process.env.CC_HAHA_NATIVE_AGENT_RUNTIME_FIXTURE!=='1')throw new Error('Missing Agent runtime fixture')
const capabilities=await installClientFixtureCapabilities(input.homeDir,{absentPaths:absentManagedSettingsFixturePaths()})
await import('../../../preload')
const bootstrap=await import('../../../src/bootstrap/state');bootstrap.setCwdState(input.homeDir);bootstrap.setOriginalCwd(input.homeDir);bootstrap.setProjectRoot(input.homeDir);bootstrap.setIsInteractive(false)
const config=await import('../../../src/utils/config');config.enableConfigs()
const {getDefaultAppState}=await import('../../../src/state/AppStateStore')
const {createFileStateCacheWithSizeLimit}=await import('../../../src/utils/fileStateCache')
const {asSystemPrompt}=await import('../../../src/utils/systemPromptType')
const {runAgent}=await import('../../../src/tools/AgentTool/runAgent')
const rows=[]
for(const row of input.cases){
 const definition={agentType:'runtime-fixture',whenToUse:'Runtime fixture',rawSystemPrompt:'Runtime body',getSystemPrompt:()=> 'Runtime body',...row.definition}
 let parent={...getDefaultAppState(),effortValue:row.parentEffort}
 const parentContext={options:{commands:[],debug:false,mainLoopModel:'sonnet',tools:[],verbose:false,thinkingConfig:{type:'disabled'},mcpClients:[],mcpResources:{},isNonInteractiveSession:false,agentDefinitions:{activeAgents:[definition],allAgents:[definition]}},abortController:new AbortController(),readFileState:createFileStateCacheWithSizeLimit(),getAppState:()=>parent,setAppState:()=>{},setResponseLength:()=>{},messages:[]}
 let captured:any
 const stop=new Error('Actual context captured before model request')
 const generator=runAgent({agentDefinition:definition,promptMessages:[],toolUseContext:parentContext as any,canUseTool:(async()=>({behavior:'allow'})) as any,isAsync:false,querySource:'agent:custom',override:{userContext:{fixture:'explicit-context'},systemContext:{gitStatus:'SOURCE GIT STATUS',other:'preserve'},systemPrompt:asSystemPrompt([]),agentId:'actual-context-fixture' as any},availableTools:[],onCacheSafeParams:params=>{captured=params;throw stop}})
 try{await generator.next();throw new Error('Agent did not reach actual context')}catch(error){if(error!==stop)throw error}
 const states=[]
 for(const mode of row.modes){parent={...parent,toolPermissionContext:{...parent.toolPermissionContext,mode}};const state=captured.toolUseContext.getAppState();states.push({mode:state.toolPermissionContext.mode,effort:state.effortValue,rulesPreserved:state.toolPermissionContext.alwaysDenyRules===parent.toolPermissionContext.alwaysDenyRules,parentMode:parent.toolPermissionContext.mode})}
 rows.push({states,systemContext:captured.systemContext,userContext:captured.userContext,thinking:captured.toolUseContext.options.thinkingConfig})
}
process.stdout.write(JSON.stringify({rows,capabilities}))
