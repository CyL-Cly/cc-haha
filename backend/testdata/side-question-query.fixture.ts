import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
const home = assertDisposableFixtureHome(process.env.HOME, 'side question real query Source')
if (process.env.CC_HAHA_SIDE_QUESTION_FIXTURE !== '1') throw new Error('Missing fixture marker')
await import('../../preload')
;(globalThis as any).MACRO = { BUILD_TIME: '' }
const bootstrap = await import('../../src/bootstrap/state')
bootstrap.setCwdState(home); bootstrap.setOriginalCwd(home); bootstrap.setProjectRoot(home)
const { z } = await import('zod')
const { query } = await import('../../src/query')
const { runSideQuestion } = await import('../../src/utils/sideQuestion')
const { createUserMessage } = await import('../../src/utils/messages')
const { getDefaultAppState } = await import('../../src/state/AppStateStore')
const { asSystemPrompt } = await import('../../src/utils/systemPromptType')
const { createFileStateCacheWithSizeLimit } = await import('../../src/utils/fileStateCache')
const { enableConfigs } = await import('../../src/utils/config')
enableConfigs()
const rows = []
for (const order of ['main-first', 'side-first']) {
 const deferred = () => { let resolve!: () => void; const promise = new Promise<void>(r => { resolve = r }); return { promise, resolve } }
 const mainEntered=deferred(), sideEntered=deferred(), releaseMain=deferred(), releaseSide=deferred()
 let executions=0, calls=0
 const tool = { name:'FixtureWrite', inputSchema:z.object({value:z.string()}), prompt:async()=>'Write a file',maxResultSizeChars:1000,isConcurrencySafe:()=>false,isReadOnly:()=>false,isEnabled:()=>true,userFacingName:()=> 'fixture',description:async()=>'Write a file',call:async()=>{executions++;return{data:'done'}},mapToolResultToToolResultBlockParam:(data:string,id:string)=>({type:'tool_result',tool_use_id:id,content:data}) }
 const wire=(text:string)=>[
 {type:'message_start',message:{id:'msg',type:'message',role:'assistant',model:'fixture-model',content:[],stop_reason:null,stop_sequence:null,usage:{input_tokens:7,output_tokens:0}}},
 {type:'content_block_start',index:0,content_block:{type:'text',text:''}},
 {type:'content_block_delta',index:0,delta:{type:'text_delta',text}},
 {type:'content_block_stop',index:0},{type:'message_delta',delta:{stop_reason:'end_turn',stop_sequence:null},usage:{output_tokens:5}},{type:'message_stop'}].map(data=>`event: ${data.type}\ndata: ${JSON.stringify(data)}\n\n`).join('')
 const server=Bun.serve({hostname:'127.0.0.1',port:0,async fetch(request){const data=await request.json();calls++;const side=JSON.stringify(data).includes('This is a side question from the user');(side?sideEntered:mainEntered).resolve();await(side?releaseSide:releaseMain).promise;return new Response(wire(side?'side answer':'main answer'),{headers:{'content-type':'text/event-stream'}})}})
 process.env.ANTHROPIC_BASE_URL=`http://127.0.0.1:${server.port}`
 const state=getDefaultAppState(), mainController=new AbortController(), sideController=new AbortController()
 const parentMessages=[createUserMessage({content:'held main'})]
 const context={options:{commands:[],debug:false,mainLoopModel:'fixture-model',tools:[tool],verbose:false,thinkingConfig:{type:'disabled'},mcpClients:[],mcpResources:{},isNonInteractiveSession:true,agentDefinitions:{activeAgents:[],allAgents:[]}},abortController:mainController,readFileState:createFileStateCacheWithSizeLimit(100),getAppState:()=>state,setAppState:()=>{},setInProgressToolUseIDs:()=>{},setResponseLength:()=>{},updateFileHistoryState:()=>{},updateAttributionState:()=>{},messages:parentMessages} as any
 const systemPrompt=asSystemPrompt(['cached system prefix']), before=JSON.stringify(parentMessages)
 const cache={systemPrompt,userContext:{},systemContext:{},toolUseContext:context,forkContextMessages:parentMessages}
 let main!:Promise<void>,side!:ReturnType<typeof runSideQuestion>
 const startMain=()=>{main=(async()=>{for await(const _ of query({messages:parentMessages,systemPrompt,userContext:{},systemContext:{},canUseTool:async()=>({behavior:'deny',message:'fixture'}),toolUseContext:context,querySource:'sdk',maxTurns:1})){} })();return mainEntered.promise}
 const startSide=()=>{side=runSideQuestion({question:'side question',cacheSafeParams:cache,signal:sideController.signal});return sideEntered.promise}
 try {
  if(order==='main-first'){await startMain();await startSide()}else{await startSide();await startMain()}
  releaseMain.resolve()
  await main
  let sideSettled=false;void side.then(()=>{sideSettled=true},()=>{sideSettled=true})
  await Promise.resolve()
  const mainCompletedBeforeSide=!sideSettled
  releaseSide.resolve();const result=await side
  rows.push({order,mainCompletedBeforeSide,response:result.response,parentUnchanged:before===JSON.stringify(parentMessages),mainAborted:mainController.signal.aborted,calls,executions})
 }finally{releaseMain.resolve();releaseSide.resolve();server.stop(true)}
}
process.stdout.write(JSON.stringify(rows))
process.exit(0)
