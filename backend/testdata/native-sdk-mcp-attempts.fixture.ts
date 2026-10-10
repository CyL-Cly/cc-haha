import fs from 'node:fs'
import {createHash} from 'node:crypto'
import {assertDisposableFixtureHome} from '../../scripts/quality-gate/go-parity/fixture-guard.ts'
import {installClientFixtureCapabilities,absentManagedSettingsFixturePaths} from '../../scripts/quality-gate/go-parity/client-options-capabilities.ts'
const home=assertDisposableFixtureHome(process.env.HOME,'SDK nameset lifecycle')
const peers:any={},pending=new Map<string,(m:any)=>void>(),counts:any={},exits:any={},methods:any[]=[]
for(const name of ['alpha','beta','retry']){
 const child=Bun.spawn([process.execPath,new URL('./native-sdk-mcp-attempt-peer.fixture.ts',import.meta.url).pathname,name],{cwd:home,env:process.env,stdin:'pipe',stdout:'pipe',stderr:'pipe'});peers[name]=child
 counts[name]={initialize:0,list:0,call:0,notifications:0}
 let readyResolve:any;const ready=new Promise(r=>readyResolve=r)
 ;(async()=>{let b='';for await(const c of child.stderr){b+=new TextDecoder().decode(c);if(b.includes('READY'))readyResolve();}exits[name]={stderr:b,code:await child.exited,pid:child.pid}})()
 ;(async()=>{let b='';for await(const c of child.stdout){b+=new TextDecoder().decode(c);while(b.includes('\n')){let i=b.indexOf('\n');const m=JSON.parse(b.slice(0,i));b=b.slice(i+1);const k=name+':'+(m.id===undefined?'notice':String(m.id));pending.get(k)?.(m);pending.delete(k)}}})()
 await ready
}
const printPath=fs.realpathSync(new URL('../../src/cli/print.ts',import.meta.url))
const caps=await installClientFixtureCapabilities(home,{absentPaths:absentManagedSettingsFixturePaths()})
await import('../../preload.ts')
const state=await import('../../src/bootstrap/state.ts');state.setCwdState(home);state.setOriginalCwd(home);state.setProjectRoot(home);state.setAllowedSettingSources(['userSettings']);state.setIsInteractive(false)
;(await import('../../src/utils/config.ts')).enableConfigs()
const {setupSdkMcpClients}=await import('../../src/services/mcp/client.ts')
const {getMcpPrefix}=await import('../../src/services/mcp/mcpStringUtils.ts')
const {ToolListChangedNotificationSchema}=await import('../../node_modules/@modelcontextprotocol/sdk/dist/esm/types.js')
const source=fs.readFileSync(printPath,'utf8'),start=source.indexOf('  async function updateSdkMcp() {'),end=source.indexOf('\n  void updateSdkMcp()',start),body=source.slice(start,end)
if(start<0||end<0)throw Error('actual updateSdkMcp missing')
let rejectRetry=true
async function send(name:string,m:any){
 methods.push({name,method:m.method,id:m.id})
 const c=counts[name];if(m.method==='initialize')c.initialize++;else if(m.method==='tools/list')c.list++;else if(m.method==='tools/call')c.call++;else c.notifications++
 if(name==='retry'&&m.method==='initialize'&&rejectRetry){rejectRetry=false;throw Error('explicit first peer initialize rejection')}
 const key=name+':'+(m.id===undefined?'notice':String(m.id)),result=new Promise<any>(resolve=>pending.set(key,resolve));peers[name].stdin.write(JSON.stringify(m)+'\n');peers[name].stdin.flush();return result
}
let app:any={mcp:{tools:[]}},updates=0
const factory=new Function('setupSdkMcpClients','structuredIO','isMcpServerDisabledForExecution','getMcpPrefix','uniq','setupVscodeSdkMcp','setAppState',`let sdkMcpConfigs={},sdkClients=[],sdkTools=[];${new Bun.Transpiler({loader:'ts'}).transformSync(body)};return {set(names){sdkMcpConfigs=Object.fromEntries(names.map(name=>[name,{type:'sdk',name}]))},update:updateSdkMcp,snapshot(){return {clients:sdkClients,tools:sdkTools}}}`)
const flow:any=factory(setupSdkMcpClients,{sendMcpMessage:send},()=>false,getMcpPrefix,(v:any[])=>[...new Set(v)],()=>{},(f:any)=>{app=f(app);updates++})
const stages:any[]=[],borrowed:any[]=[]
function record(label:string){stages.push({label,counts:JSON.parse(JSON.stringify(counts)),clients:flow.snapshot().clients.map((c:any)=>({name:c.name,type:c.type})),tools:flow.snapshot().tools.map((t:any)=>t.name),updates})}
async function call(c:any,text:string){return (await c.client.callTool({name:'physical',arguments:{text}})).content[0].text}
flow.set(['alpha']);await flow.update();record('initial');const oldAlpha=flow.snapshot().clients[0],originalTool=flow.snapshot().tools[0];borrowed.push(oldAlpha)
await flow.update();record('unchanged')
flow.set(['alpha','beta']);await flow.update();record('added');const second=flow.snapshot().clients;const addedAlpha=second.find((c:any)=>c.name==='alpha'),oldBeta=second.find((c:any)=>c.name==='beta');borrowed.push(addedAlpha)
const cachedToolIdentity=flow.snapshot().tools.find((t:any)=>t.name==='mcp__alpha__physical')===originalTool
let oldNotices=0,newNotices=0
oldAlpha.client.setNotificationHandler(ToolListChangedNotificationSchema,()=>oldNotices++)
addedAlpha.client.setNotificationHandler(ToolListChangedNotificationSchema,()=>newNotices++)
const inboundStart=source.indexOf('          const mcpRequest = message.request'),inboundEnd=source.indexOf("        } else if (message.request.subtype === 'rewind_files')",inboundStart)
if(inboundStart<0||inboundEnd<0)throw Error('Original inbound SDK notification consumer missing')
const dispatch=new Function('sdkClients','message','sendControlResponseSuccess',new Bun.Transpiler({loader:'ts'}).transformSync(source.slice(inboundStart,inboundEnd)))
const notice={type:'control_request',request_id:'current-sdk-notification',request:{subtype:'mcp_message',server_name:'alpha',message:{jsonrpc:'2.0',method:'notifications/tools/list_changed',params:{_meta:{actual:'LATEST_ONLY'}}}}}
const acks:any[]=[];dispatch(flow.snapshot().clients,notice,(message:any)=>acks.push(message.request_id));await Promise.resolve()
const noticeCounts={old:oldNotices,current:newNotices,acks}
if(oldNotices!==0||newNotices!==1||acks.length!==1)throw Error('Original latest SDK notification routing changed')
const oldAfterAdd=await call(oldAlpha,'OLD_ALPHA_AFTER_REBUILD')
flow.set(['alpha','beta','retry']);await flow.update();record('failed');const failedAlpha=flow.snapshot().clients.find((c:any)=>c.name==='alpha');borrowed.push(failedAlpha)
await flow.update();record('retried');const final=flow.snapshot().clients;const finalAlpha=final.find((c:any)=>c.name==='alpha');borrowed.push(finalAlpha)
const oldAfterRetry=await call(oldAlpha,'OLD_ALPHA_AFTER_RETRY')
flow.set(['beta','retry']);await flow.update();record('removed');let removedError='';try{await call(finalAlpha,'SHOULD_NOT_WRITE')}catch(e:any){removedError=e.message}
const orphanAfterRemoval=await call(oldAlpha,'OLD_ALPHA_SURVIVES_NAME_REMOVAL')
const betaAfterRemoval=await call(oldBeta,'OLD_BETA_SURVIVES_REBUILD')
const latest=flow.snapshot().clients;for(const c of latest)if(c.type==='connected')await c.cleanup()
const orphanAfterLatestCleanup=await call(oldAlpha,'OLD_ALPHA_SURVIVES_LATEST_CLEANUP')
const cachedToolCallAfterLatestCleanup=await originalTool.call({text:'CACHED_SOURCE_TOOL_SURVIVES_REBUILD'},{abortController:new AbortController(),setAppState:(f:any)=>app=f(app)},()=>{}, {message:{content:[]}})
for(const c of borrowed)if(c.type==='connected')await c.cleanup()
for(const child of Object.values(peers) as any[])child.stdin.end()
await Promise.all(Object.values(peers).map((c:any)=>c.exited));await Promise.resolve()
const result={noticeCounts,cachedToolIdentity,cachedToolCallAfterLatestCleanup,bodySHA:createHash('sha256').update(body).digest('hex'),stages,oldIdentityDifferent:oldAlpha.client!==addedAlpha.client,oldAfterAdd,oldAfterRetry,removedError,orphanAfterRemoval,betaAfterRemoval,orphanAfterLatestCleanup,methods,exits,caps,physical:{alpha:fs.readFileSync(home+'/alpha.txt','utf8'),beta:fs.readFileSync(home+'/beta.txt','utf8')}}
if(counts.alpha.initialize!==4||counts.alpha.list!==1||counts.beta.initialize!==4||counts.beta.list!==1||counts.retry.initialize!==3||counts.retry.list!==1||!result.oldIdentityDifferent||!cachedToolIdentity||!removedError)throw Error('Source lifecycle strict count/identity assertion '+JSON.stringify(result))
process.stdout.write(JSON.stringify(result)+'\n')
