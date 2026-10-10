import fs from 'node:fs'
import { fileURLToPath } from 'node:url'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../scripts/quality-gate/go-parity/client-options-capabilities'
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_SDK_MCP_STATUS_FIXTURE !== '1') throw new Error('Explicit actual SDK MCP status fixture required')
const home=assertDisposableFixtureHome(process.env.HOME,'SDK MCP status')
const printPath=fs.realpathSync(fileURLToPath(new URL('../../src/cli/print.ts',import.meta.url)))
await installClientFixtureCapabilities(home,{absentPaths:absentManagedSettingsFixturePaths()})
await import('../../preload')
Object.assign(globalThis,{MACRO:{VERSION:'fixture',BUILD_TIME:''}})
const state=await import('../../src/bootstrap/state')
state.setCwdState(process.cwd());state.setOriginalCwd(process.cwd());state.setProjectRoot(process.cwd());state.setAllowedSettingSources(['userSettings']);state.setIsInteractive(false)
const {enableConfigs}=await import('../../src/utils/config');enableConfigs()
const {Server}=await import('@modelcontextprotocol/sdk/server/index.js')
const {ListToolsRequestSchema}=await import('@modelcontextprotocol/sdk/types.js')
const {SdkControlServerTransport}=await import('../../src/services/mcp/SdkControlTransport')
const {setupSdkMcpClients}=await import('../../src/services/mcp/client')
const {isMcpServerDisabledForExecution,setMcpServerEnabled}=await import('../../src/services/mcp/config')
const {filterToolsByServer}=await import('../../src/services/mcp/utils')
const uniqBy=(await import('lodash-es/uniqBy.js')).default
const source=fs.readFileSync(printPath,'utf8')
const begin=source.indexOf('  function buildMcpServerStatuses():')
const end=source.indexOf('  // NOTE: Nested function required',begin)
if(begin<0||end<0)throw new Error('Original MCP status producer missing')
const body=new Bun.Transpiler({loader:'ts'}).transformSync(source.slice(begin,end)+'\nreturn buildMcpServerStatuses()\n')
const build=new Function('getAppState','sdkClients','dynamicMcpState','uniqBy','isMcpServerDisabledForExecution','filterToolsByServer','feature',body)
const project=(clients:any[],tools:any[])=>build(()=>({mcp:{clients:[],tools}}),clients,{clients:[],tools:[]},uniqBy,isMcpServerDisabledForExecution,filterToolsByServer,()=>false)
const before=project([],[])
const callbacks=new Map<string,(message:any)=>void>()
const transport=new SdkControlServerTransport((message:any)=>callbacks.get(String(message.id))?.(message))
const server=new Server({name:'actual private SDK filesystem reader',version:'1'},{capabilities:{tools:{}}})
server.setRequestHandler(ListToolsRequestSchema,async()=>({tools:[{name:'read_sdk',description:'Read the private physical SDK acceptance file',inputSchema:{type:'object',properties:{}}}]}))
await server.connect(transport)
if(process.env.CC_HAHA_SDK_MCP_DISABLED_P715==='1')setMcpServerEnabled('sdk-p684',false)
const sdkConfigs={'sdk-p684':{type:'sdk',name:'sdk-p684'}} as any
const configs=Object.fromEntries(Object.entries(sdkConfigs).filter(([name])=>!isMcpServerDisabledForExecution(name)))
const connected=await setupSdkMcpClients(configs,async(_name,message:any)=>{
 if(process.env.CC_HAHA_SDK_MCP_FAILURE_P715==='1' && message.method==='initialize')throw new Error('physical SDK initialize rejected')
 if(message.id===undefined){transport.onmessage?.(message);return {jsonrpc:'2.0',method:'notifications/progress',params:{progressToken:'unused',progress:0}}}
 const response=new Promise<any>(resolve=>callbacks.set(String(message.id),resolve));transport.onmessage?.(message);return response
})
if(!process.env.CC_HAHA_SDK_MCP_FAILURE_P715 && !process.env.CC_HAHA_SDK_MCP_DISABLED_P715 && connected.clients[0]?.type!=='connected')throw new Error('Original SDK handshake failed')
const after=project(connected.clients,connected.tools)
if(connected.clients[0]?.type==='connected')await connected.clients[0].cleanup();await server.close()
process.stdout.write(JSON.stringify({before,after})+'\n')
