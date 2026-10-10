import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
import { mock, spyOn } from 'bun:test'
import * as crypto from 'node:crypto'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.homeDir, 'Native Tool Search')
if (process.env.CC_HAHA_NATIVE_TOOL_SEARCH_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Unowned tool search fixture')
const capabilities = await installClientFixtureCapabilities(home, {absentPaths:[...absentManagedSettingsFixturePaths(),'/home/claude/.claude/remote/.api_key','/home/claude/.claude/remote/.oauth_token']})
mock.module('../../../src/utils/secureStorage/keychainPrefetch',()=>({getLegacyApiKeyPrefetchResult:()=>({stdout:''}),getLegacyApiKeyPrefetchPromise:()=>undefined,startKeychainPrefetch:()=>{},clearLegacyApiKeyPrefetch:()=>{}}))
;(globalThis as any).MACRO = {BUILD_TIME:''}
const {ToolSearchTool,clearToolSearchDescriptionCache} = await import('../../../src/tools/ToolSearchTool/ToolSearchTool')
const {buildTool,getEmptyToolPermissionContext} = await import('../../../src/Tool')
const {getPrompt,isDeferredTool} = await import('../../../src/tools/ToolSearchTool/prompt')
const {getToolSearchMode,isToolSearchEnabledOptimistic,modelSupportsToolReference,extractDiscoveredToolNames,getDeferredToolsDelta} = await import('../../../src/utils/toolSearch')
const {toolToAPISchema} = await import('../../../src/utils/api')
const makeTools = (rows:any[],trace:string[]) => rows.map(row=>buildTool({...row,prompt:async()=>{trace.push(row.name);return row.prompt??''},inputJSONSchema:row.input_schema??{type:'object',properties:{}}}))
const autoPlans=[]
if (input.autoPlans) {
  process.env.USER_TYPE='ant';process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH=home+'/managed'
  const {getManagedFilePath}=await import('../../../src/utils/settings/managedPath')
  if (getManagedFilePath()!==home+'/managed')throw new Error('Unowned managed path')
  process.env.USER_TYPE='external'
  const state=await import('../../../src/bootstrap/state');state.setCwdState(home);state.setOriginalCwd(home);state.setProjectRoot(home);state.setAllowedSettingSources(['userSettings','projectSettings','localSettings','policySettings'])
  const {enableConfigs}=await import('../../../src/utils/config');enableConfigs()
  const analyze=await import('../../../src/utils/analyzeContext')
  let current:any
  const countTrace:any[]=[]
  const promptTrace:string[]=[]
  spyOn(analyze,'countToolDefinitionTokens').mockImplementation(async (tools:any,permission:any,agents:any,model:any)=>{
    countTrace.push({names:tools.map((t:any)=>t.name),model})
    if(current.countError)throw new Error('physical API failure fixture')
    return current.count
  })
  const {isToolSearchEnabled}=await import('../../../src/utils/toolSearch')
  const {getContextWindowForModel}=await import('../../../src/utils/context')
  const {getMergedBetas}=await import('../../../src/utils/betas')
  for(const row of input.autoPlans){
    current=row;process.env.ENABLE_TOOL_SEARCH=row.mode
    const tools=makeTools(row.tools,promptTrace)
    const enabled=await isToolSearchEnabled(row.model,tools,async()=>getEmptyToolPermissionContext(),[])
    autoPlans.push({enabled,window:getContextWindowForModel(row.model,getMergedBetas(row.model)),countTrace:[...countTrace],promptTrace:[...promptTrace]})
  }
}
const normalizations=[]
const parsers=(input.parsers??[]).map((value:string)=>{
  const parsed=ToolSearchTool.inputSchema.safeParse(JSON.parse(value))
  return parsed.success?{success:true,data:parsed.data}:{success:false,issues:parsed.error.issues}
})
if (input.normalizations) {
  process.env.USER_TYPE='ant';process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH=home+'/managed'
  const {getManagedFilePath}=await import('../../../src/utils/settings/managedPath')
  if (getManagedFilePath()!==home+'/managed')throw new Error('Unowned managed path')
  process.env.USER_TYPE='external'
  const state=await import('../../../src/bootstrap/state');state.setCwdState(home);state.setOriginalCwd(home);state.setProjectRoot(home);state.setAllowedSettingSources(['userSettings','projectSettings','localSettings','policySettings'])
  const {enableConfigs}=await import('../../../src/utils/config');enableConfigs()
  const {normalizeMessagesForAPI,stripToolReferenceBlocksFromUserMessage,stripCallerFieldFromAssistantMessage,ensureToolResultPairing}=await import('../../../src/utils/messages')
  for (const row of input.normalizations) {
    process.env.ENABLE_TOOL_SEARCH=row.enabled?'true':'false'
    const tools=makeTools(row.tools,[])
    let output=normalizeMessagesForAPI(JSON.parse(row.history),tools,row.model)
    if(!row.modelEnabled) output=output.map(message=>message.type==='user'?stripToolReferenceBlocksFromUserMessage(message):message.type==='assistant'?stripCallerFieldFromAssistantMessage(message):message)
    normalizations.push(ensureToolResultPairing(output))
  }
}
const observations = []
clearToolSearchDescriptionCache()
for (const row of input.rows??[]) {
  const trace:string[] = []
  const tools = makeTools(row.tools,trace)
  const context:any = {options:{tools},getAppState:()=>({mcp:{clients:(row.pending??[]).map((name:string)=>({type:'pending',name}))}})}
  const parsed=ToolSearchTool.inputSchema.safeParse(JSON.parse(row.input))
  if (!parsed.success) {observations.push({issues:parsed.error.issues});continue}
  try {
    const result=await ToolSearchTool.call(parsed.data,context)
    observations.push({parsed:parsed.data,data:result.data,mapped:ToolSearchTool.mapToolResultToToolResultBlockParam(result.data,'tool-id'),trace})
  } catch (error:any) {observations.push({parsed:parsed.data,error:error.message,trace})}
}
const gates=[]
for (const row of input.gates??[]) {
  for(const name of ['ENABLE_TOOL_SEARCH','CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS','ANTHROPIC_BASE_URL','USER_TYPE','CLAUDE_CODE_USE_BEDROCK','CLAUDE_CODE_USE_VERTEX','CLAUDE_CODE_USE_FOUNDRY','CLAUDE_CODE_USE_AZURE_OPENAI']) delete process.env[name]
  Object.assign(process.env,row.env)
  gates.push({mode:getToolSearchMode(),optimistic:isToolSearchEnabledOptimistic(),modelSupports:modelSupportsToolReference(row.model),prompt:getPrompt()})
}
const poolRows=[]
for (const row of input.poolRows??[]) {
  const tools=makeTools(row.tools,[])
  poolRows.push({deferred:tools.filter(isDeferredTool).map(tool=>tool.name),discovered:[...extractDiscoveredToolNames(row.history)],delta:getDeferredToolsDelta(tools,row.history)})
}
const projections=[]
if(input.projections){
  const {normalizeAttachmentForAPI}=await import('../../../src/utils/messages')
  let allocations=0
  spyOn(crypto,'randomUUID').mockImplementation(()=>`tool-search-delta-${++allocations}` as any)
  const RealDate=Date
  globalThis.Date=class extends RealDate{constructor(...args:any[]){if(args.length)super(...args as [any]);else super(1700000000000)}static now(){return 1700000000000}} as any
  for(const value of input.projections)projections.push({messages:normalizeAttachmentForAPI(JSON.parse(value)),allocations})
}
delete process.env.USER_TYPE
delete process.env.ANTHROPIC_BASE_URL
delete process.env.CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS
const schema=await toolToAPISchema(ToolSearchTool,{tools:[ToolSearchTool],agents:[],model:'deepseek-flash',getToolPermissionContext:async()=>getEmptyToolPermissionContext()})
process.stdout.write(JSON.stringify({schema,parsers,observations,gates,poolRows,normalizations,autoPlans,projections,capabilities}))
