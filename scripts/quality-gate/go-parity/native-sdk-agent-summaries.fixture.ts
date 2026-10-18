import fs from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { mock, spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_SDK_AGENT_SUMMARY_FIXTURE !== '1') throw new Error('Explicit pinned Source metadata fixture required')
const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(process.env.HOME, 'SDK initialize metadata')
const printPath = fs.realpathSync(fileURLToPath(new URL('../../../src/cli/print.ts', import.meta.url)))
const mainPath = fs.realpathSync(fileURLToPath(new URL('../../../src/main.tsx', import.meta.url)))
const absent=[...absentManagedSettingsFixturePaths(), '/Library/Application Support/ClaudeCode/.claude/skills', '/Library/Application Support/ClaudeCode/.claude/agents', '/Library/Application Support/ClaudeCode/.claude/output-styles', join(process.execPath, '..', 'rg'), '/usr/bin/rg', '/bin/rg', '/home/claude/.claude/remote/.oauth_token','/home/claude/.claude/remote/.api_key']
for(let directory=dirname(home);;directory=dirname(directory)){absent.push(join(directory,'.git'));if(directory===dirname(directory))break}
let activeSummaryPeer: ((request: Request) => Promise<Response>) | undefined
const summaryPeer = Bun.serve({ hostname: '127.0.0.1', port: 0, fetch(request) {
  if (!activeSummaryPeer) return new Response('No active private summary profile', { status: 404 })
  return activeSummaryPeer(request)
} })
process.env.ANTHROPIC_BASE_URL=summaryPeer.url.origin
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absent, loopbackFetchOrigin: summaryPeer.url.origin })
process.once('exit', () => summaryPeer.stop(true))
// Only the external credential-store primitive is replaced. Its bytes are the
// disposable physical Source credential fixture, parsed by the real auth code.
const secure = join(home, '.claude', '.credentials.json')
if (input.cachedSubscriber) {
  fs.mkdirSync(dirname(secure),{recursive:true})
  fs.writeFileSync(secure,JSON.stringify({claudeAiOauth:{accessToken:'fake-source-suggestion-subscriber',scopes:['user:inference','user:profile'],expiresAt:9999999999999,subscriptionType:'max'}}))
  delete process.env.ANTHROPIC_API_KEY
}
mock.module(new URL('../../../src/utils/secureStorage/index.ts', import.meta.url).pathname, () => ({ getSecureStorage: () => ({ name: 'private-physical-credential-store', read: () => fs.existsSync(secure) ? JSON.parse(fs.readFileSync(secure, 'utf8')) : null, readAsync: async () => fs.existsSync(secure) ? JSON.parse(fs.readFileSync(secure, 'utf8')) : null }) }))
// Execute original prefetch with a narrowly bound external security primitive.
const processes = await import('node:child_process')
const fakeExecFile = (command: string, args: string[], options: unknown, callback: Function) => {
  if (command !== 'security' || args[0] !== 'find-generic-password') throw new Error('Unbound Source subprocess')
  callback(Object.assign(new Error('private Keychain entry absent'), { code:44 }), '')
  return { on() {}, kill() {} } as any
}
spyOn(processes, 'execFile').mockImplementation(fakeExecFile as any)
const prefetch = await import('../../../src/utils/secureStorage/keychainPrefetch')
prefetch.startKeychainPrefetch()
await prefetch.ensureKeychainPrefetchCompleted()
Object.assign(globalThis, { MACRO: { VERSION:'fixture', BUILD_TIME:'' } })
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
state.setCwdState(process.cwd()); state.setOriginalCwd(process.cwd()); state.setProjectRoot(process.cwd()); state.setAllowedSettingSources(['userSettings']); state.setIsInteractive(false)
const config = await import('../../../src/utils/config'); config.enableConfigs()
const model = await import('../../../src/utils/model/model')
const { getModelOptions } = await import('../../../src/utils/model/modelOptions')
const auth = await import('../../../src/utils/auth')
const effort = await import('../../../src/utils/effort')
const thinking = await import('../../../src/utils/thinking')
const fast = await import('../../../src/utils/fastMode')
const { getAPIProvider } = await import('../../../src/utils/model/providers')
state.setInitialMainLoopModel(input.initial ?? null)
state.setMainLoopModelOverride(input.override)
// Execute the original modelInfos declaration body, supplying its actual
// imported producers. No expected catalog or capability outputs are injected.
const source = fs.readFileSync(printPath, 'utf8')
const begin = source.indexOf('  const modelOptions = getModelOptions()')
const end = source.indexOf('  let activeUserSpecifiedModel', begin)
if (begin < 0 || end < 0) throw new Error('Original initialize modelInfos declaration missing')
const body = new Bun.Transpiler({ loader:'ts' }).transformSync(source.slice(begin,end)+'\nreturn modelInfos\n')
const build = new Function('getModelOptions','feature','getDefaultMainLoopModel','parseUserSpecifiedModel','modelSupportsEffort','modelSupportsAdaptiveThinking','isFastModeSupportedByModel','getSupportedEffortLevelsForModel',body)
const models = build(getModelOptions, (name:string) => name === 'TRANSCRIPT_CLASSIFIER', model.getDefaultMainLoopModel, model.parseUserSpecifiedModel, effort.modelSupportsEffort, thinking.modelSupportsAdaptiveThinking, fast.isFastModeSupportedByModel, effort.getSupportedEffortLevelsForModel)
const { initBuiltinPlugins } = await import('../../../src/plugins/bundled/index')
const { initBundledSkills } = await import('../../../src/skills/bundled/index')
initBuiltinPlugins(); initBundledSkills()
const commandsModule = await import('../../../src/commands')
const headless = await import('../../../src/commands/headless')
const commands = headless.filterCommandsForHeadlessMode(await commandsModule.getCommands(process.cwd())).filter(cmd => cmd.userInvocable !== false).map(cmd => ({name:commandsModule.getCommandName(cmd),description:commandsModule.formatDescriptionWithSource(cmd),argumentHint:cmd.argumentHint || ''}))
const agentsModule = await import('../../../src/tools/AgentTool/loadAgentsDir')
const definitions = await agentsModule.getAgentDefinitionsWithOverrides(process.cwd())
const agents = definitions.activeAgents.map(agent => ({name:agent.agentType,description:agent.whenToUse,...(agent.model !== undefined && agent.model !== 'inherit' ? {model:agent.model}: {})}))
const styles = await import('../../../src/constants/outputStyles')
const outputStyles = Object.keys(await styles.getAllOutputStyles(process.cwd()))
const settings = await import('../../../src/utils/settings/settings')
const outputStyle = settings.getSettings_DEPRECATED()?.outputStyle || styles.DEFAULT_OUTPUT_STYLE_NAME
const accountInfo = auth.getAccountInformation()
const account = {email:accountInfo?.email,organization:accountInfo?.organization,subscriptionType:accountInfo?.subscription,tokenSource:accountInfo?.tokenSource,apiKeySource:accountInfo?.apiKeySource,apiProvider:getAPIProvider()}
// Execute the unchanged original initialize handler with its real producers.
const initializeBegin = source.indexOf('async function handleInitializeRequest(')
const initializeEnd = source.indexOf('async function handleRewindFiles(', initializeBegin)
if (initializeBegin < 0 || initializeEnd < 0) throw new Error('Original SDK initialize consumer missing')
const initializeBody = new Bun.Transpiler({loader:'ts'}).transformSync(source.slice(initializeBegin, initializeEnd) + '\nreturn handleInitializeRequest\n')
const cwd = await import('../../../src/utils/cwd')
const dependencies = {
  AwsAuthStatusManager: (await import('../../../src/utils/awsAuthStatusManager')).AwsAuthStatusManager,
  randomUUID: (await import('node:crypto')).randomUUID,
  getSessionId: state.getSessionId,
  parseAgentsFromJson: agentsModule.parseAgentsFromJson,
  getMainThreadAgentType: state.getMainThreadAgentType,
  setMainThreadAgentType: state.setMainThreadAgentType,
  isBuiltInAgent: agentsModule.isBuiltInAgent,
  parseUserSpecifiedModel: model.parseUserSpecifiedModel,
  setMainLoopModelOverride: state.setMainLoopModelOverride,
  getSettings_DEPRECATED: settings.getSettings_DEPRECATED,
  DEFAULT_OUTPUT_STYLE_NAME: styles.DEFAULT_OUTPUT_STYLE_NAME,
  getAllOutputStyles: styles.getAllOutputStyles,
  getCwd: cwd.getCwd,
  getAccountInformation: auth.getAccountInformation,
  registerHookCallbacks: state.registerHookCallbacks,
  setInitJsonSchema: state.setInitJsonSchema,
  getCommandName: commandsModule.getCommandName,
  formatDescriptionWithSource: commandsModule.formatDescriptionWithSource,
  getAPIProvider,
  isFastModeEnabled: fast.isFastModeEnabled,
  isFastModeAvailable: fast.isFastModeAvailable,
  getFastModeState: fast.getFastModeState,
}
const handle = new Function(...Object.keys(dependencies), initializeBody)(...Object.values(dependencies))
const {StructuredIO} = await import('../../../src/cli/structuredIO')
const io = new StructuredIO((async function*(){})())
const output: any[] = []
const options: any = input.options ?? {}
const allCommands = headless.filterCommandsForHeadlessMode(await commandsModule.getCommands(process.cwd()))
const allAgents = definitions.activeAgents.slice()
if (input.authStatus !== undefined) {
  if (input.sessionId) state.switchSession(input.sessionId)
  await handle({subtype:'initialize'}, 'actual-init', false, {enqueue:(value:any)=>output.push(value)}, allCommands, models, io, input.authStatus, options, allAgents, ()=>({fastMode:false}))
  await handle({subtype:'initialize'}, 'repeated-init', true, {enqueue:(value:any)=>output.push(value)}, allCommands, models, io, input.authStatus, options, allAgents, ()=>({fastMode:false}))
  process.stdout.write(JSON.stringify({output,capabilities})+'\n')
  process.exit(0)
}
if (input.controls) {
  const modelStart=source.indexOf('          const requestedModel = message.request.model')
  const modelEnd=source.indexOf("        } else if (message.request.subtype === 'set_max_thinking_tokens')",modelStart)
  const thinkingStart=source.indexOf('          if (message.request.max_thinking_tokens === null)',modelEnd)
  const thinkingEnd=source.indexOf("        } else if (message.request.subtype === 'mcp_status')",thinkingStart)
  if([modelStart,modelEnd,thinkingStart,thinkingEnd].some(index=>index<0))throw new Error('Original SDK inference consumers missing')
  const runModel=new Function('message','getDefaultMainLoopModel','setMainLoopModelOverride','notifySessionMetadataChanged','injectModelSwitchBreadcrumbs','sendControlResponseSuccess',new Bun.Transpiler({loader:'ts'}).transformSync('let activeUserSpecifiedModel\n'+source.slice(modelStart,modelEnd)))
  const runThinking=new Function('message','options','sendControlResponseSuccess',new Bun.Transpiler({loader:'ts'}).transformSync(source.slice(thinkingStart,thinkingEnd)))
  const messages=await import('../../../src/utils/messages')
  const main=fs.readFileSync(mainPath,'utf8')
  const initialStart=main.indexOf('    let thinkingEnabled = shouldEnableThinkingByDefault();')
  const initialEnd=main.indexOf('    logForDiagnosticsNoPII(',initialStart)
  if(initialStart<0||initialEnd<0)throw new Error('Original thinking initialization missing')
  const initialBody=new Bun.Transpiler({loader:'ts'}).transformSync(main.slice(initialStart,initialEnd)+'\nreturn thinkingConfig\n')
  const initialThinking=new Function('options','shouldEnableThinkingByDefault','process',initialBody)({},thinking.shouldEnableThinkingByDefault,process)
  const options:any={thinkingConfig:initialThinking}
  const frames:any[]=[]
  const breadcrumbs:any[]=[]
  const send=(message:any)=>frames.push({type:'control_response',response:{subtype:'success',request_id:message.request_id}})
  const snapshots:any[]=[]
  for(const request of input.controls){
    const message={request_id:'control-'+snapshots.length,request}
    if(request.subtype==='set_model')runModel(message,model.getDefaultMainLoopModel,state.setMainLoopModelOverride,()=>{},(requested:string,resolved:string)=>breadcrumbs.push(...messages.createModelSwitchBreadcrumbs(requested,resolved)),send)
    else runThinking(message,options,send)
    snapshots.push({model:model.getMainLoopModel(),thinkingConfig:options.thinkingConfig})
  }
  process.stdout.write(JSON.stringify({frames,snapshots,breadcrumbs,capabilities})+'\n')
  process.exit(0)
}

// Actual AgentSummary service, original 30-second timer, real transcript loader,
// real fork/query/deny callback and SDK task event producer. No query/timer mocks.
const {getDefaultAppState}=await import('../../../src/state/AppStateStore')
const {asSystemPrompt}=await import('../../../src/utils/systemPromptType')
const {createFileStateCacheWithSizeLimit}=await import('../../../src/utils/fileStateCache')
const {createUserMessage,createAssistantMessage}=await import('../../../src/utils/messages')
const {FileWriteTool}=await import('../../../src/tools/FileWriteTool/FileWriteTool')
const storage=await import('../../../src/utils/sessionStorage')
const {startAgentSummarization}=await import('../../../src/services/AgentSummary/agentSummary')
const {drainSdkEvents}=await import('../../../src/utils/sdkEventQueue')
const {getFeatureValue_CACHED_MAY_BE_STALE}=await import('../../../src/services/analytics/growthbook')
const flagsStart=source.indexOf('          // Enable prompt suggestions in AppState when SDK consumer opts in.')
const flagsEnd=source.indexOf('\n          initialized = true',flagsStart)
if(flagsStart<0||flagsEnd<0)throw new Error('Original initialize flag producer missing')
const flags=new Function('message','setAppState','getFeatureValue_CACHED_MAY_BE_STALE','setSdkAgentProgressSummariesEnabled',new Bun.Transpiler({loader:'ts'}).transformSync(source.slice(flagsStart,flagsEnd)))
const wire=(events:any[])=>events.map(data=>`event: ${data.type}\ndata: ${JSON.stringify(data)}\n\n`).join('')
const textWire=(text:string)=>wire([
 {type:'message_start',message:{id:'summary-text',type:'message',role:'assistant',model:'fixture-model',content:[],stop_reason:null,stop_sequence:null,usage:{input_tokens:2,output_tokens:0}}},
 {type:'content_block_start',index:0,content_block:{type:'text',text:''}},
 {type:'content_block_delta',index:0,delta:{type:'text_delta',text}},
 {type:'content_block_stop',index:0},{type:'message_delta',delta:{stop_reason:'end_turn',stop_sequence:null},usage:{output_tokens:3}},{type:'message_stop'}
])
const hasPrevious=(request:any)=>request.messages.some((row:any)=>Array.isArray(row.content)&&row.content.some((block:any)=>block.type==='text'&&block.text.includes('Previous: "Reading summary-target.ts" — say something NEW.')))
const calls=new Map<string,any[]>()
const profiles:any[]=[]
const forbidden=join(home,'summary-must-not-write.txt')
activeSummaryPeer=async(request: Request)=>{
 if(request.method==='HEAD')return new Response('')
 const data=await request.json()
 const name=profiles.find(p=>JSON.stringify(data.system).includes('SDK_SUMMARY_PREFIX_'+p.name))?.name
 if(!name)throw new Error('Unbound summary model consumer')
 const rows=calls.get(name)!;rows.push(data)
 if(name==='p1'&&rows.length===1)return new Response(JSON.stringify({type:'error',error:{type:'api_error',message:'private summary transient'}}),{status:500,headers:{'content-type':'application/json'}})
 const round=rows.length-(name==='p1'?1:0)
 if(round===1)return new Response(wire([
  {type:'message_start',message:{id:'summary-tool',type:'message',role:'assistant',model:'fixture-model',content:[],stop_reason:null,stop_sequence:null,usage:{input_tokens:2,output_tokens:0}}},
  {type:'content_block_start',index:0,content_block:{type:'tool_use',id:'summary-denied-write',name:'Write',input:{}}},
  {type:'content_block_delta',index:0,delta:{type:'input_json_delta',partial_json:JSON.stringify({file_path:forbidden,content:'MUST_NOT_WRITE'})}},
  {type:'content_block_stop',index:0},{type:'message_delta',delta:{stop_reason:'tool_use',stop_sequence:null},usage:{output_tokens:3}},{type:'message_stop'}
 ]),{headers:{'content-type':'text/event-stream'}})
 if(rows.length===3&&name==='p0'&&!hasPrevious(data))throw new Error('Original second summary lost previous text')
 return new Response(textWire(name==='p0'&&rows.length===3?'  Fixing summary-target.ts  ':'  Reading summary-target.ts  '),{headers:{'content-type':'text/event-stream'}})
}
const server=summaryPeer
process.env.ANTHROPIC_BASE_URL=`http://127.0.0.1:${server.port}`
let app:any=getDefaultAppState()
const controls:any[]=[]
const stops:Array<()=>void>=[]
const completions:Promise<void>[]=[]
const initialSnapshots=new Map<string,string>()
const parentCaches:any[]=[]
try{
 for(const [index,flag] of (input.flags??[true,'false',1,[],{},false,null,0,'']).entries()){
  const name='p'+index,id=('a'+index.toString(16).padStart(16,'0')) as any
  const parseIO=new StructuredIO((async function*(){yield JSON.stringify({type:'control_request',request_id:name,request:{subtype:'initialize',agentProgressSummaries:flag}})+'\n'})())
  const parsed=await parseIO.structuredInput.next()
  if(parsed.done||parsed.value.type!=='control_request')throw new Error('Source StructuredIO rejected '+name)
  state.setSdkAgentProgressSummariesEnabled(false)
  const ack:any[]=[],opts:any={}
  await handle(parsed.value.request,name,false,{enqueue:(row:any)=>ack.push(row)},allCommands,models,parseIO,false,opts,definitions.activeAgents.slice(),()=>app)
  flags(parsed.value,(update:any)=>{app=update(app)},getFeatureValue_CACHED_MAY_BE_STALE,state.setSdkAgentProgressSummariesEnabled)
  const enabled=state.getSdkAgentProgressSummariesEnabled()
  controls.push({name,flag,enabled,ack:ack.map(row=>row.response.subtype)})
  await parseIO.structuredInput.next()
  if(!enabled)continue
  profiles.push({name,id});calls.set(name,[])
  const actual=[createUserMessage({content:'DISK_FIRST_'+name}),createAssistantMessage({content:'DISK_ANSWER_'+name}),createUserMessage({content:'DISK_LATEST_'+name}),createAssistantMessage({content:[{type:'tool_use',id:'incomplete-'+name,name:'Write',input:{file_path:forbidden,content:'UNFINISHED'}}]})]
  await storage.recordSidechainTranscript(actual,id)
  await storage.flushSessionStorage()
  const transcript=await storage.getAgentTranscript(id)
  if(!transcript||transcript.messages.length!==4)throw new Error('Actual Source transcript was not physically available')
  const initial=[createUserMessage({content:'INITIAL_PINNED_MUST_NOT_APPEAR_'+name})]
  initialSnapshots.set(name,JSON.stringify(initial))
  app={...app,tasks:{...app.tasks,[id]:{id,type:'local_agent',status:'running',agentId:id,startTime:Date.now(),description:'Actual summary '+name,toolUseId:'tool-'+name,ownerAgentId:'owner-'+name,progress:{tokenCount:19,toolUseCount:2}}}}
  const cache=createFileStateCacheWithSizeLimit(10);parentCaches.push(cache)
  let resolve!:()=>void
  completions.push(new Promise<void>(done=>{resolve=done}))
  let publications=0,lifecycle:{stop:()=>void}|undefined
  const setAppState=(update:any)=>{app=update(app);if(app.tasks[id]?.progress?.summary){publications++;if(publications===(index===0?2:1)){lifecycle?.stop();resolve()}}}
  const context={options:{commands:[],debug:false,mainLoopModel:'fixture-model',tools:[FileWriteTool],verbose:false,thinkingConfig:{type:'disabled'},mcpClients:[],mcpResources:{},isNonInteractiveSession:true,agentDefinitions:{activeAgents:[],allAgents:[]}},abortController:new AbortController(),readFileState:cache,getAppState:()=>app,setAppState,setInProgressToolUseIDs:()=>{},setResponseLength:()=>{},updateFileHistoryState:()=>{},updateAttributionState:()=>{},messages:initial} as any
  lifecycle=startAgentSummarization(id,id,{systemPrompt:asSystemPrompt(['SDK_SUMMARY_PREFIX_'+name]),userContext:{},systemContext:{},toolUseContext:context,forkContextMessages:initial},setAppState)
  stops.push(lifecycle.stop)
  profiles[profiles.length-1].context=context
  profiles[profiles.length-1].initial=initial
 }
 // Each row above resets only the flag oracle. Restore the actual process
 // opt-in through the original print consumer before pending timers fire.
 flags({request:{agentProgressSummaries:true}},(update:any)=>{app=update(app)},getFeatureValue_CACHED_MAY_BE_STALE,state.setSdkAgentProgressSummariesEnabled)
 // All truthy actual producers share the real 30-second wall-clock interval;
 // model responses are isolated by their original inherited system prefix.
 await Promise.all(completions)
 for(const stop of stops)stop()
 const events=drainSdkEvents()
 const rows=profiles.map(({name,id,context,initial})=>{
  const requests=calls.get(name)!,event=events.filter(e=>e.subtype==='task_progress'&&e.task_id===id).at(-1)
  return{name,summary:app.tasks[id].progress.summary,event,requests:requests.length,toolDenied:requests.some(row=>JSON.stringify(row.messages).includes('No tools needed for summary')),toolsAdvertised:requests.every(row=>row.tools.some((tool:any)=>tool.name==='Write')),freshDisk:requests.every(row=>JSON.stringify(row.messages).includes('DISK_LATEST_'+name)&&!JSON.stringify(row.messages).includes('INITIAL_PINNED_MUST_NOT_APPEAR')&&!JSON.stringify(row.messages).includes('UNFINISHED')),parentUnchanged:JSON.stringify(initial)===initialSnapshots.get(name),parentAborted:context.abortController.signal.aborted,previousConsumed:name!=='p0'||hasPrevious(requests[2]),cacheMarkers:requests.map(row=>row.messages.flatMap((m:any,i:number)=>JSON.stringify(m).includes('cache_control')?[i]:[])),messageCounts:requests.map(row=>row.messages.length),thinking:requests.map(row=>row.thinking),maxTokens:requests.map(row=>row.max_tokens)}
 })
 if(capabilities.blocked.length)throw new Error('Summary Source escaped fixture capabilities '+JSON.stringify(capabilities.blocked))
 process.stdout.write(JSON.stringify({controls,rows,fileExists:fs.existsSync(forbidden),capabilities})+'\n')
}finally{for(const stop of stops)stop();server.stop(true);await storage.flushSessionStorage()}
process.exit(0)
