import fs from 'node:fs'
import { fileURLToPath } from 'node:url'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../scripts/quality-gate/go-parity/client-options-capabilities'
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_SDK_MCP_CONTROLS_FIXTURE !== '1') throw new Error('Explicit pinned SDK MCP controls fixture required')
const home=assertDisposableFixtureHome(process.env.HOME,'SDK MCP controls')
const printPath=fs.realpathSync(fileURLToPath(new URL('../../src/cli/print.ts',import.meta.url)))
await installClientFixtureCapabilities(home,{absentPaths:absentManagedSettingsFixturePaths(),...(process.env.CC_HAHA_SDK_MCP_CONTROLS_LOOPBACK_ORIGIN?{loopbackFetchOrigin:process.env.CC_HAHA_SDK_MCP_CONTROLS_LOOPBACK_ORIGIN}:{})})
await import('../../preload')
Object.assign(globalThis,{MACRO:{VERSION:'fixture',BUILD_TIME:''}})
const state=await import('../../src/bootstrap/state')
state.setCwdState(home);state.setOriginalCwd(home);state.setProjectRoot(home);state.setAllowedSettingSources(['userSettings']);state.setIsInteractive(false)
;(await import('../../src/utils/config')).enableConfigs()
const {Server}=await import('@modelcontextprotocol/sdk/server/index.js')
const {ListToolsRequestSchema,CallToolRequestSchema}=await import('@modelcontextprotocol/sdk/types.js')
const {SdkControlServerTransport}=await import('../../src/services/mcp/SdkControlTransport')
const {setupSdkMcpClients,reconnectMcpServerImpl,connectToServer,clearServerCache,areMcpConfigsEqual}=await import('../../src/services/mcp/client')
const {isMcpServerDisabledForExecution,setMcpServerEnabled,filterMcpServersByPolicy,getMcpConfigByName}=await import('../../src/services/mcp/config')
const {getMcpPrefix}=await import('../../src/services/mcp/mcpStringUtils')
const {filterToolsByServer,commandBelongsToServer}=await import('../../src/services/mcp/utils')
const {fetchToolsForClient}=await import('../../src/services/mcp/client')
const {toError}=await import('../../src/utils/errors')
const uniqBy=(await import('lodash-es/uniqBy.js')).default
const reject=(await import('lodash-es/reject.js')).default
const omit=(await import('lodash-es/omit.js')).default
const peers=new Map<string,any>(),counts:any={}
const physicalFile=home+'/sdk-control-physical.txt'
if (!fs.existsSync(physicalFile)) fs.writeFileSync(physicalFile,'SDK_CONTROL_PHYSICAL_ONLY\n',{mode:0o600})
async function makePeer(name:string,output:(message:any)=>void){
 const server=new Server({name:'physical SDK control peer',version:'1'},{capabilities:{tools:{}}})
 const transport=new SdkControlServerTransport(output)
 server.setRequestHandler(ListToolsRequestSchema,async()=>({tools:[{name:'read_sdk',description:'Read the private physical SDK control file',inputSchema:{type:'object',properties:{}}}]}))
 server.setRequestHandler(CallToolRequestSchema,async()=>({content:[{type:'text',text:fs.readFileSync(physicalFile,'utf8')}]}))
 await server.connect(transport)
 return {server,transport}
}
const notificationAck={jsonrpc:'2.0',method:'notifications/progress',params:{progressToken:'unused',progress:0}}
if (process.env.CC_HAHA_SDK_MCP_CONTROLS_PEER==='1') {
 const peer=await makePeer('sdk-p684',message=>process.stdout.write(JSON.stringify(message)+'\n'))
 let buffer=''
 for await (const chunk of Bun.stdin.stream()) {
  buffer+=new TextDecoder().decode(chunk)
  while(buffer.includes('\n')){
   const i=buffer.indexOf('\n'),message=JSON.parse(buffer.slice(0,i));buffer=buffer.slice(i+1)
   peer.transport.onmessage?.(message)
   if(message.id===undefined)process.stdout.write(JSON.stringify(notificationAck)+'\n')
  }
 }
 await peer.server.close()
} else {
 const input=JSON.parse(await Bun.stdin.text())
 const source=fs.readFileSync(printPath,'utf8')
 const start=source.indexOf('function toScopedConfig('),end=source.length
 const helperText=source.slice(start,end)
 if(start<0)throw Error('Original set-server implementation missing')
 const helpers=new Function('filterMcpServersByPolicy','connectToServer','clearServerCache','fetchToolsForClient','getMcpPrefix','areMcpConfigsEqual','toError','logError',new Bun.Transpiler({loader:'ts'}).transformSync(helperText.replaceAll('export async function','async function'))+';return {handleMcpSetServers,reconcileMcpServers}')
 const {handleMcpSetServers}=helpers(filterMcpServersByPolicy,connectToServer,clearServerCache,fetchToolsForClient,getMcpPrefix,areMcpConfigsEqual,toError,()=>{})
 const uStart=source.indexOf('  async function updateSdkMcp() {'),uEnd=source.indexOf('\n  void updateSdkMcp()',uStart)
 const updateBody=source.slice(uStart,uEnd)
 const aStart=source.indexOf('  let mcpChangesPromise:'),aEnd=source.indexOf('  // Build McpServerStatus',aStart)
 const applyBody=source.slice(aStart,aEnd)
 const cStart=source.indexOf("        } else if (message.request.subtype === 'mcp_set_servers')"),cReload=source.indexOf("        } else if (message.request.subtype === 'reload_plugins')",cStart)
 const cReconnect=source.indexOf("        } else if (message.request.subtype === 'mcp_reconnect')",cReload),cEnd=source.indexOf("        } else if (message.request.subtype === 'channel_enable')",cReconnect)
 const controlBody='if'+source.slice(cStart+'        } else if'.length,cReload)+source.slice(cReconnect,cEnd)+'\n}'
 const statusStart=source.indexOf('  function buildMcpServerStatuses():'),statusEnd=source.indexOf('  // NOTE: Nested function required',statusStart)
 for(const boundary of [uStart,uEnd,aStart,aEnd,cStart,cReload,cReconnect,cEnd,statusStart,statusEnd])if(boundary<0)throw Error('Original controls source boundary missing')
 const events:any[]=[]
 let flow:any,refresh:Promise<any>=Promise.resolve()
 async function send(name:string,message:any){
  counts[name]??={initialize:0,list:0,call:0}
  const c=counts[name];if(message.method==='initialize')c.initialize++;if(message.method==='tools/list')c.list++;if(message.method==='tools/call')c.call++
  let peer=peers.get(name)
  if(!peer){const pending=new Map<string,(m:any)=>void>();peer={...(await makePeer(name,m=>pending.get(String(m.id))?.(m))),pending};peers.set(name,peer)}
  if(message.id===undefined){peer.transport.onmessage?.(message);return notificationAck}
  const result=new Promise(resolve=>peer.pending.set(String(message.id),resolve));peer.transport.onmessage?.(message);return result
 }
 const factory=new Function('handleMcpSetServers','setupSdkMcpClients','structuredIO','isMcpServerDisabledForExecution','setMcpServerEnabled','getMcpConfigByName','getMcpPrefix','reconnectMcpServerImpl','clearServerCache','reject','omit','uniqBy','filterToolsByServer','commandBelongsToServer','onACK','observeRefresh',new Bun.Transpiler({loader:'ts'}).transformSync(`
 let sdkMcpConfigs={},sdkClients=[],sdkTools=[],tools=[],mcpClients=[],dynamicMcpState={configs:{},clients:[],tools:[]};let app={mcp:{clients:[],tools:[],commands:[],resources:{}}};
 const setAppState=f=>app=f(app),getAppState=()=>app,uniq=v=>[...new Set(v)],setupVscodeSdkMcp=()=>{},feature=()=>false,elicitationRegistered=new Set(),registerElicitationHandlers=()=>{},reregisterChannelHandlerAfterReconnect=()=>{};
 ${updateBody.replace('async function updateSdkMcp()', 'async function originalUpdateSdkMcp()')}
 const updateSdkMcp=()=>observeRefresh(originalUpdateSdkMcp());
 ${applyBody}
 ${source.slice(statusStart,statusEnd)}
 const sendControlResponseSuccess=(message,response)=>onACK({subtype:'success',request_id:message.request_id,...(response===undefined?{}:{response})},snapshot());
 const sendControlResponseError=(message,error)=>onACK({subtype:'error',request_id:message.request_id,error},snapshot());
 function snapshot(){return {configs:sdkMcpConfigs,clients:sdkClients.map(c=>({name:c.name,type:c.type,config:c.config})),status:buildMcpServerStatuses(),tools:sdkTools.map(t=>t.name)}}
 return {snapshot,update:updateSdkMcp,async control(message){${controlBody}}};`))
 flow=factory(handleMcpSetServers,setupSdkMcpClients,{sendMcpMessage:send},isMcpServerDisabledForExecution,setMcpServerEnabled,getMcpConfigByName,getMcpPrefix,reconnectMcpServerImpl,clearServerCache,reject,omit,uniqBy,filterToolsByServer,commandBelongsToServer,(ack:any,snapshot:any)=>events.push({ack,snapshot:JSON.parse(JSON.stringify(snapshot))}),(p:Promise<any>)=>{refresh=p;return p})
 const observations:any[]=[]
 for(const row of input.rows){
  if(row.persist!==undefined)setMcpServerEnabled(row.request.serverName,row.persist)
  if(row.askRefresh)await flow.update()
  else {await flow.control({type:'control_request',request_id:row.id,request:row.request});if(row.waitRefresh)await refresh}
  observations.push({event:row.askRefresh?null:events.at(-1),after:JSON.parse(JSON.stringify(flow.snapshot())),counts:JSON.parse(JSON.stringify(counts)),enabled:isMcpServerDisabledForExecution('sdk-p684')===false})
 }
 for(const peer of peers.values())await peer.server.close()
 process.stdout.write(JSON.stringify({observations,sourceBodies:{update:updateBody,apply:applyBody,control:controlBody}})+'\n')
}
