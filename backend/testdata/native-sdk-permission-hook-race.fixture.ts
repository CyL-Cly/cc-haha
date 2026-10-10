import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../scripts/quality-gate/go-parity/client-options-capabilities'
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_SDK_PERMISSION_RACE_FIXTURE !== '1') throw new Error('Explicit original SDK permission race fixture required')
const home=assertDisposableFixtureHome(process.env.HOME,'SDK permission hook race')
const {realpathSync}=await import('node:fs')
const {fileURLToPath}=await import('node:url')
const credentialModuleSource=realpathSync(fileURLToPath(new URL('../../src/utils/secureStorage/index.ts',import.meta.url)))
const capabilities=await installClientFixtureCapabilities(home,{absentPaths:[...absentManagedSettingsFixturePaths(),'/home/claude/.claude/remote/.api_key','/home/claude/.claude/remote/.oauth_token']})
// Both the private loader path and the canonical readonly Source path must bind
// the same absent credential-store primitive. No Keychain access is admitted.
const {mock}=await import('bun:test')
mock.module(credentialModuleSource,()=>({getSecureStorage:()=>({name:'private-source-empty-credentials',read:()=>null,readAsync:async()=>null,update:()=>{throw new Error('Unexpected Source credential write')},delete:()=>{throw new Error('Unexpected Source credential delete')}})}))
await import('../../preload')
const state=await import('../../src/bootstrap/state')
state.setCwdState(home);state.setOriginalCwd(home);state.setProjectRoot(home);state.setAllowedSettingSources(['userSettings']);state.setIsInteractive(false)
const {enableConfigs}=await import('../../src/utils/config');enableConfigs()
const {StructuredIO}=await import('../../src/cli/structuredIO')
const {Stream}=await import('../../src/utils/stream')
const input=await Bun.stdin.json()
const rows:any[]=[]
for(const row of input.rows){
  state.clearRegisteredHooks()
  const raw=new Stream<string>(),io=new StructuredIO(raw)
  const reading=(async()=>{for await(const _ of io.structuredInput){}})()
  const toolName=row.physicalTool?'Read':'mcp__private__read'
  state.registerHookCallbacks({PermissionRequest:[{matcher:toolName,hooks:[io.createHookCallback('permission-hook')]}]})
  let update:()=>void=()=>{}
  const updated=new Promise<void>(resolve=>update=resolve)
  let appState:any={sessionHooks:new Map(),toolPermissionContext:{mode:'default',additionalWorkingDirectories:new Map(),alwaysAllowRules:{},alwaysDenyRules:{},alwaysAskRules:{}}}
  const controller=new AbortController()
  const call:any={abortController:controller,messages:[],options:{isNonInteractiveSession:true,tools:[],commands:[],mainLoopModel:'fixture-model',thinkingConfig:{type:'disabled'},mcpClients:[],mcpResources:{},debug:false,verbose:false,agentDefinitions:{activeAgents:[],allAgents:[]}},getAppState:()=>appState,setAppState:(f:any)=>{appState=f(appState);update()},setInProgressToolUseIDs:()=>{},setResponseLength:()=>{},nestedMemoryAttachmentTriggers:new Set()}
  let observedController=controller,physicalMarker=false
  let actualDecision:any
  const physicalPath=home+'/sdk-source-physical-read.txt'
  let operation:Promise<any>
  if(row.physicalTool){
    await Bun.write(physicalPath,'SDK_SOURCE_PHYSICAL_READ')
    const {FileReadTool}=await import('../../src/tools/FileReadTool/FileReadTool')
    const {createFileStateCacheWithSizeLimit}=await import('../../src/utils/fileStateCache')
    const {createAssistantMessage}=await import('../../src/utils/messages')
    const {StreamingToolExecutor}=await import('../../src/services/tools/StreamingToolExecutor')
    call.options.tools=[FileReadTool];call.readFileState=createFileStateCacheWithSizeLimit()
    const permission=io.createCanUseTool()
    const executor=new StreamingToolExecutor([FileReadTool],async(tool:any,value:any,current:any,assistant:any,id:any)=>{
      observedController=current.abortController
      actualDecision=await permission(tool,value,current,assistant,id,{behavior:'ask'})
      return actualDecision
    },call)
    const block:any={type:'tool_use',id:'permission-race-tool',name:'Read',input:{file_path:physicalPath}}
    executor.addTool(block,createAssistantMessage({content:[block]}))
    operation=(async()=>{for await(const result of executor.getRemainingResults()){if(JSON.stringify(result).includes('SDK_SOURCE_PHYSICAL_READ'))physicalMarker=true}return actualDecision})()
  }else{
    const tool:any={name:toolName,userFacingName:()=> 'private physical reader'}
    operation=io.createCanUseTool()(tool,{path:'original'},call,undefined as any,'permission-race-tool',{behavior:'ask'})
  }
  const requests:any[]=[]
  while(requests.length<2)requests.push((await io.outbound.next()).value)
  const sdk=requests.find(frame=>frame.request.subtype==='can_use_tool')
  const hook=requests.find(frame=>frame.request.subtype==='hook_callback')
  if(!sdk||!hook)throw new Error('Original race did not start both actual producers')
  const deliver=(frame:any,response:any)=>raw.enqueue(JSON.stringify({type:'control_response',response:{subtype:'success',request_id:frame.request_id,response}})+'\n')
  const hookOutput={hookSpecificOutput:{hookEventName:'PermissionRequest',decision:{behavior:'allow',updatedInput:row.physicalTool?{file_path:physicalPath}:{path:'hook-physical'},...(row.lateUpdates?{updatedPermissions:[{type:'setMode',mode:'acceptEdits',destination:'session'}]}:{})}}}
  if(row.winner==='cancel')controller.abort()
  else if(row.winner==='hook')deliver(hook,hookOutput)
  else if(row.winner==='passthrough')deliver(hook,{})
  else deliver(sdk,{behavior:row.deny?'deny':'allow',...(row.deny?{message:'SDK deny'}:{updatedInput:row.physicalTool?{file_path:physicalPath}:{path:'sdk-physical'}})})
  if(row.winner==='passthrough')deliver(sdk,{behavior:'allow',updatedInput:row.physicalTool?{file_path:physicalPath}:{path:'sdk-physical'}})
  const decision=await operation
  const pendingAtWinner=io.getPendingPermissionRequests().length
  let cancel:any,cancels:any[]=[]
  if(row.winner==='cancel'){for(let i=0;i<2;i++){const frame:any=(await io.outbound.next()).value;cancels.push({...frame,request_id:frame.request_id===sdk.request_id?'<can_use_tool>':'<hook_callback>'})}}
  if(row.winner==='hook')cancel=(await io.outbound.next()).value
  else if(row.winner!=='passthrough'&&row.winner!=='cancel'){
    deliver(hook,hookOutput)
    if(row.lateUpdates)await updated
  }
  raw.done();await reading
  const packet=(frame:any)=>({...frame,request_id:'<'+frame.request.subtype+'>'})
  rows.push({name:row.name,requests:requests.map(packet),decision,pendingAtWinner,cancel:cancel?{...cancel,request_id:'<can_use_tool>'}:undefined,permissionMode:appState.toolPermissionContext.mode,...(cancels.length?{cancels}:{}),parentAborted:controller.signal.aborted,toolSignalAborted:observedController.signal.aborted,...(row.physicalTool?{physicalMarker}:{})})
}
state.clearRegisteredHooks()
// Execute the original Agent producer up to its real cache-safe context seam.
// Its narrowed tools are the source of the secondary PermissionRequest prompt;
// this observation stops before any model request rather than returning a model.
const {runAgent}=await import('../../src/tools/AgentTool/runAgent')
const {FileReadTool}=await import('../../src/tools/FileReadTool/FileReadTool')
const {FileWriteTool}=await import('../../src/tools/FileWriteTool/FileWriteTool')
const {createFileStateCacheWithSizeLimit}=await import('../../src/utils/fileStateCache')
const {getDefaultAppState}=await import('../../src/state/AppStateStore')
const {asSystemPrompt}=await import('../../src/utils/systemPromptType')
const definition:any={agentType:'sdk-child-read',whenToUse:'Actual Source child catalog',tools:['Read'],rawSystemPrompt:'actual child',getSystemPrompt:()=> 'actual child'}
const available=[FileReadTool,FileWriteTool]
let app=getDefaultAppState()
const parent:any={options:{commands:[],debug:false,mainLoopModel:'fixture-model',tools:available,verbose:false,thinkingConfig:{type:'disabled'},mcpClients:[],mcpResources:{},isNonInteractiveSession:true,agentDefinitions:{activeAgents:[definition],allAgents:[definition]}},abortController:new AbortController(),readFileState:createFileStateCacheWithSizeLimit(),getAppState:()=>app,setAppState:(f:any)=>{app=f(app)},setResponseLength:()=>{},messages:[]}
let childToolNames:string[]=[]
const captured=new Error('Actual child ToolUseContext captured before model')
const generator=runAgent({agentDefinition:definition,promptMessages:[],toolUseContext:parent,isAsync:false,querySource:'agent:custom',canUseTool:ioNeverRequests(),override:{userContext:{},systemContext:{},systemPrompt:asSystemPrompt([]),agentId:'actual-child-catalog'},availableTools:available,onCacheSafeParams:(params:any)=>{childToolNames=params.toolUseContext.options.tools.map((tool:any)=>tool.name);throw captured}})
try{await generator.next();throw new Error('Source child context never captured')}catch(error){if(error!==captured)throw error}
function ioNeverRequests(){return async()=>{throw new Error('Unexpected permission before child context capture')}}
process.stdout.write(JSON.stringify({rows,childToolNames,capabilities})+'\n')
