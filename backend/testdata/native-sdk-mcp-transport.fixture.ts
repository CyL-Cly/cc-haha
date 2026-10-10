import fs from 'node:fs'
import {join} from 'node:path'
import {fileURLToPath} from 'node:url'
import {assertDisposableFixtureHome} from '../../scripts/quality-gate/go-parity/fixture-guard'
import {absentManagedSettingsFixturePaths,installClientFixtureCapabilities} from '../../scripts/quality-gate/go-parity/client-options-capabilities'
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_SDK_MCP_TRANSPORT_FIXTURE !== '1') throw new Error('Explicit pinned SDK MCP Source fixture required')
const home=assertDisposableFixtureHome(process.env.HOME,'SDK MCP transport')
const printPath=fs.realpathSync(fileURLToPath(new URL('../../src/cli/print.ts',import.meta.url)))
const caps=await installClientFixtureCapabilities(home,{absentPaths:absentManagedSettingsFixturePaths()})
await import('../../preload')
Object.assign(globalThis,{MACRO:{VERSION:'fixture',BUILD_TIME:''}})
const state=await import('../../src/bootstrap/state')
state.setCwdState(home);state.setOriginalCwd(home);state.setProjectRoot(home);state.setAllowedSettingSources(['userSettings']);state.setIsInteractive(false)
const config=await import('../../src/utils/config');config.enableConfigs()
const {Server}=await import('@modelcontextprotocol/sdk/server/index.js')
const {ListToolsRequestSchema,CallToolRequestSchema,ToolListChangedNotificationSchema}=await import('@modelcontextprotocol/sdk/types.js')
const {SdkControlServerTransport}=await import('../../src/services/mcp/SdkControlTransport')
const {setupSdkMcpClients}=await import('../../src/services/mcp/client')
const file=join(home,'sdk-physical.txt')
const responseCallbacks=new Map<string,(value:any)=>void>()
const serverTransport=new SdkControlServerTransport((message:any)=>{
 if(process.env.CC_HAHA_SDK_MCP_PEER==='1')process.stdout.write(JSON.stringify(message)+'\n')
 else responseCallbacks.get(String(message.id))?.(message)
})
const server=new Server({name:'actual-sdk-peer',version:'1'},{capabilities:{tools:{}}})
server.setRequestHandler(ListToolsRequestSchema,async()=>({tools:[{name:'touch',description:'Write the private physical SDK file',inputSchema:{type:'object',properties:{text:{type:'string'}},required:['text']}}]}))
server.setRequestHandler(CallToolRequestSchema,async(request:any)=>{
 fs.writeFileSync(file,request.params.arguments.text,{mode:0o600})
 return {content:[{type:'text',text:fs.readFileSync(file,'utf8')}]}
})
await server.connect(serverTransport)
// A valid unrelated notification is the private Query peer's acknowledgement
// for MCP notifications; it neither fabricates a tool response nor resolves an ID.
const notificationAck={jsonrpc:'2.0',method:'notifications/progress',params:{progressToken:'unused',progress:0}}
if(process.env.CC_HAHA_SDK_MCP_PEER==='1'){
 let buffer=''
 for await(const chunk of Bun.stdin.stream()){
  buffer+=new TextDecoder().decode(chunk)
  while(buffer.includes('\n')){
   const i=buffer.indexOf('\n');const message=JSON.parse(buffer.slice(0,i));buffer=buffer.slice(i+1)
   serverTransport.onmessage?.(message)
   if(message.id===undefined)process.stdout.write(JSON.stringify(notificationAck)+'\n')
  }
 }
 await server.close()
}else{
 const messages:any[]=[]
 const result=await setupSdkMcpClients({peer:{type:'sdk',name:'peer'}},async(name,message:any)=>{
  messages.push({name,message})
  if(message.id===undefined){serverTransport.onmessage?.(message);return notificationAck}
  const response=new Promise<any>(resolve=>responseCallbacks.set(String(message.id),resolve))
  serverTransport.onmessage?.(message)
  return response
 })
 if(result.clients.length!==1||result.clients[0]?.type!=='connected')throw new Error('Actual SDK Source handshake failed')
 const client:any=result.clients[0]
 const call=await client.client.callTool({name:'touch',arguments:{text:'ACTUAL_SDK_PHYSICAL'}})
 const tools=result.tools.map((tool:any)=>tool.name)
 const notifications:any[]=[]
 client.client.setNotificationHandler(ToolListChangedNotificationSchema,(message:any)=>{notifications.push({method:message.method,params:message.params})})
 const source=fs.readFileSync(printPath,'utf8')
 const start=source.indexOf('          const mcpRequest = message.request')
 const end=source.indexOf("        } else if (message.request.subtype === 'rewind_files')",start)
 if(start<0||end<0)throw new Error('Original inbound SDK notification consumer missing')
 const body=new Bun.Transpiler({loader:'ts'}).transformSync(source.slice(start,end))
 const dispatch=new Function('sdkClients','message','sendControlResponseSuccess',body)
 const deliveries:any[]=[]
 for(const name of ['missing','failed','peer']){
  const event={type:'control_request',request_id:'notice-'+name,request:{subtype:'mcp_message',server_name:name,message:{jsonrpc:'2.0',method:'notifications/tools/list_changed',params:{_meta:{fixture:'ACTUAL_SDK_NOTICE'}}}}}
  const acknowledgements:any[]=[]
  dispatch([...result.clients,{name:'failed',type:'failed'}],event,(message:any)=>acknowledgements.push({type:'control_response',response:{subtype:'success',request_id:message.request_id}}))
  // Protocol dispatch uses Promise.resolve(handler). No timer or extra request.
  await Promise.resolve()
  deliveries.push({name,message:event.request.message,acknowledgements,received:notifications.length})
 }
 await client.cleanup();await server.close()
 process.stdout.write(JSON.stringify({messages,tools,call,deliveries,notifications,file:fs.readFileSync(file,'utf8'),capabilities:caps})+'\n')
}
