import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { installClientFixtureCapabilities, absentManagedSettingsFixturePaths } from '../../scripts/quality-gate/go-parity/client-options-capabilities'
assertDisposableFixtureHome(process.env.HOME, 'Actual RemoteIO native WebSocket producer')
if(process.env.CC_HAHA_REMOTEIO_FIXTURE!=='1')throw new Error('Unowned RemoteIO WebSocket fixture')
const input=await Bun.stdin.json()
const nativeWebSocket=globalThis.WebSocket
const origin=new URL(input.url.replace(/^ws/,'http')).origin
const capabilities=await installClientFixtureCapabilities(process.env.HOME!,{absentPaths:absentManagedSettingsFixturePaths(),loopbackFetchOrigin:origin})
const endpoint=new URL(input.url)
if(endpoint.hostname!=='127.0.0.1'||!endpoint.port||endpoint.username||endpoint.password)throw new Error('RemoteIO WS capability must be exact private loopback endpoint')
globalThis.WebSocket=class extends nativeWebSocket{
  constructor(url: string|URL,options?: any){
    if(String(url)!==input.url||options?.proxy && options.proxy !== input.proxyUrl)throw new Error('Unapproved RemoteIO native WebSocket')
    super(url,options)
  }
} as typeof WebSocket
Object.assign(process.env,input.env)
;(globalThis as any).MACRO={VERSION:'source-fixture',BUILD_TIME:'',PACKAGE_URL:'https://fixture.invalid/package'}
await import('../../preload')
const {default:axios}=await import('axios')
axios.defaults.adapter=async config=>{
  const url=new URL(config.url!);if(url.origin!==origin||url.username||url.password)throw new Error('Unapproved hybrid POST')
  const controller=new AbortController();const timer=config.timeout?setTimeout(()=>controller.abort(),config.timeout):undefined
  const agentProxy = config.httpAgent?.proxy ?? config.httpsAgent?.proxy
  const proxy = agentProxy ? String(agentProxy) : undefined
  if (proxy && proxy.replace(/\/$/, '') !== input.proxyUrl) throw new Error('Unapproved hybrid proxy native primitive')
  try{const response=await fetch(url,{method:config.method,headers:config.headers.toJSON() as Record<string,string>,body:config.data,signal:controller.signal,...(proxy ? {proxy} : {})});return{data:await response.text(),status:response.status,statusText:response.statusText,headers:Object.fromEntries(response.headers),config}}finally{if(timer)clearTimeout(timer)}
}
const {getTransportForUrl}=await import('../../src/cli/transports/transportUtils')
// Environment updates must consume RemoteIO's actual refreshHeaders closure,
// rather than the transport-only fixture's intentionally fixed headers.
const remote: any = input.runtimeNetwork ? new (await import('../../src/cli/remoteIO')).RemoteIO(input.url) : null
const transport:any=remote ? remote.transport : getTransportForUrl(new URL(input.url),{Authorization:'Bearer '+input.env.CLAUDE_CODE_SESSION_ACCESS_TOKEN},'source-session')
let onConnect!:()=>void,onInput!:(value:string)=>void,onClose!:(code?:number)=>void
const connected=new Promise<void>(resolve=>onConnect=resolve)
const incoming=new Promise<string>(resolve=>onInput=resolve)
const closed=new Promise<number|undefined>(resolve=>onClose=resolve)
transport.setOnConnect(onConnect);transport.setOnData(onInput);transport.setOnClose(onClose)
try{
  if (!remote) await transport.connect()
  await connected
  if (input.runtimeNetwork) {
    let ready!: () => void
    const readiness = new Promise<void>(resolve => ready = resolve)
    transport.setOnData((data: string) => { if (data.includes('network-update-ready')) ready(); else onInput(data) })
    await transport.write(input.messages[0])
    await readiness
    const { StructuredIO } = await import('../../src/cli/StructuredIO')
    const controls: any = new StructuredIO((async function* () {})())
    await controls.processLine(JSON.stringify({ type: 'update_environment_variables', variables: input.updates }))
    await transport.write(input.messages[1])
    const received = await incoming, code = await closed
    if (capabilities.blocked.length) throw new Error('Outside network update access: ' + capabilities.blocked.join(', '))
    process.stdout.write(JSON.stringify({ incoming: received, code, capabilities }))
    process.exit(0)
  }
  if (input.closeQueued) {
    const first = transport.write(input.messages[0])
    await fetch(origin + '/first-post-entered')
    const second = transport.write(input.messages[1])
    transport.close()
    await fetch(origin + '/socket-closed')
    await fetch(origin + '/release-post')
    await Promise.all([first, second])
    if (capabilities.blocked.length) throw new Error('Outside hybrid close access: ' + capabilities.blocked.join(', '))
    process.stdout.write(JSON.stringify({ capabilities }))
    process.exit(0)
  }
  for(const row of input.messages)await transport.write(row)
  if(input.env.CLAUDE_CODE_POST_FOR_SESSION_INGRESS_V2==='1')await transport.flush()
  const received=await incoming,code=await closed
  if(capabilities.blocked.length)throw new Error('Outside WS access: '+capabilities.blocked.join(', '))
  process.stdout.write(JSON.stringify({incoming:received,code,capabilities}))
}finally{transport.close()}
