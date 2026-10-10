import fs from 'node:fs'
import path from 'node:path'
import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import { installClientFixtureCapabilities, absentManagedSettingsFixturePaths } from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'

const input = JSON.parse(process.env.CC_HAHA_SOURCE_PLAN_RESTART_INPUT ?? 'null')
const home = assertDisposableFixtureHome(input.home, 'Actual Source plan runtime server')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_SOURCE_PLAN_RESTART_FIXTURE !== '1') throw new Error('Missing actual Source plan restart guard')
if (input.work !== path.join(home, 'work')) throw new Error('Unowned Source work directory')
const physicalSpawn = Bun.spawn.bind(Bun)
const physicalServe = Bun.serve.bind(Bun)
const physicalWebSocket = globalThis.WebSocket
const physicalFetch = globalThis.fetch.bind(globalThis)
const physicalProcesses = require('node:child_process')
const physicalSpawnSync = physicalProcesses.spawnSync
const physicalExecFile = physicalProcesses.execFile
const absent = [...absentManagedSettingsFixturePaths(), '/bin/zsh', '/usr/bin/zsh', '/bin/bash', '/usr/bin/bash', '/Library/Application Support/ClaudeCode/managed-mcp.json']
for (let directory = path.dirname(home); ; directory = path.dirname(directory)) {
  for (const name of ['.git', '.claude/agents', '.claude/skills', '.claude/commands', '.claude/workflows', '.agents/skills', '.mcp.json', 'CLAUDE.md', 'CLAUDE.local.md', 'AGENTS.md', '.claude/CLAUDE.md', '.claude/AGENTS.md', '.claude/rules']) absent.push(path.join(directory, name))
  if (directory === path.dirname(directory)) break
}
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absent })
const origins = new Set([new URL(input.planningURL).origin, new URL(input.executionURL).origin])
for (const origin of origins) if (new URL(origin).hostname !== '127.0.0.1') throw new Error('Unowned physical model peer')
let serverOrigin = ''
globalThis.fetch = ((value: any, options?: RequestInit) => {
  const url = new URL(value instanceof Request ? value.url : String(value))
  if (!origins.has(url.origin) && url.origin !== serverOrigin || url.username || url.password) throw new Error('Unowned Source server fetch')
  return physicalFetch(value, { ...options, redirect: 'error' })
}) as typeof fetch
Bun.serve = ((options: any) => {
  if (options.hostname !== '127.0.0.1' || options.port !== 0) throw new Error('Unowned Source server listener')
  return physicalServe(options)
}) as typeof Bun.serve
const processes: any[] = []
const processTokens: string[] = []
let sdkIdentity = ''
let observedRestart: (()=>void) | undefined
let clientEOF: (()=>void) | undefined
const clientClosed = new Promise<void>(resolve=>{clientEOF=resolve})
const readonlyGit = (args: string[], options: any) => path.resolve(options?.cwd ?? process.cwd()).startsWith(home + '/') && args.some(arg => ['rev-parse', 'status', 'log', 'diff', 'ls-files'].includes(arg)) && !args.some(arg => ['clean', 'checkout', 'restore', 'reset'].includes(arg))
physicalProcesses.spawnSync = (command: string, args: string[], options: any) => {
  if (command === 'git' && readonlyGit(args, options)) return physicalSpawnSync(command, args, options)
  throw new Error('Unowned Source server spawnSync')
}
physicalProcesses.execFile = (command: string, args: string[], ...rest: any[]) => {
  if (command === 'git' && readonlyGit(args, rest.find(x => x && typeof x === 'object'))) return physicalExecFile(command, args, ...rest)
  throw new Error('Unowned Source server execFile')
}
Bun.spawn = ((args: string[], options: any) => {
  if (!Array.isArray(args)) { options = args; args = options.cmd }
  const cwd = path.resolve(options?.cwd ?? process.cwd())
  if (cwd !== input.work) throw new Error('Unowned Source subprocess CWD')
  if (args[0] === 'bun' && args[1] === input.cliPath && options.env.HOME === home && options.env.CLAUDE_CONFIG_DIR === path.join(home, '.claude')) {
    if((input.scenario==='queued'||input.scenario==='disconnect-queued')&&processes.length===1) {socket.send(JSON.stringify({type:'user_message',content:'PLAN_RUNTIME_PRIVATE_QUEUED: Read the existing execution marker.'}));if(input.scenario==='disconnect-queued')socket.close()}
    const process = physicalSpawn(args, { ...options, env: { ...options.env, NODE_ENV: 'production', ...(input.scenario==='close-restart'&&processes.length===1?{CC_HAHA_SOURCE_PLAN_STARTUP_HELD:'1'}:{}) } })
    processes.push(process)
    if(input.scenario==='close-restart'&&processes.length===2) queueMicrotask(()=>settle({sessionId}))
    const sdkIndex=args.indexOf('--sdk-url');const sdkURL=new URL(args[sdkIndex+1]);sdkIdentity=sdkURL.pathname.split('/').at(-1)!;processTokens.push(sdkURL.searchParams.get('token')!)
    return process
  }
  if (['git', '/usr/bin/git'].includes(args[0]) && readonlyGit(args.slice(1), options)) return physicalSpawn(args, options)
  throw new Error('Unowned actual Source subprocess entry')
}) as typeof Bun.spawn
mock.module('../../../../src/utils/settings/mdm/rawRead', () => ({ startMdmRawRead: () => {}, getMdmRawReadPromise: () => Promise.resolve({ plistStdouts: [], hklmStdout: null, hkcuStdout: null }), fireRawRead: async () => ({ plistStdouts: [], hklmStdout: null, hkcuStdout: null }) }))
mock.module('../../../../src/utils/secureStorage/keychainPrefetch', () => ({ getLegacyApiKeyPrefetchResult: () => ({ stdout: '' }), getLegacyApiKeyPrefetchPromise: () => undefined, startKeychainPrefetch: () => {}, clearLegacyApiKeyPrefetch: () => {}, ensureKeychainPrefetchCompleted: async () => {} }))
;(globalThis as any).MACRO = { VERSION: 'source-plan-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
process.env.USER_TYPE = 'ant'
process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = path.join(home, 'managed')
const { getManagedFilePath } = await import('../../../../src/utils/settings/managedPath')
if (getManagedFilePath() !== path.join(home, 'managed')) throw new Error('Unowned Source managed root')
process.env.USER_TYPE = 'external'
process.env.CLAUDE_CLI_PATH = input.cliPath
console.log = (...values) => {
 console.error(...values)
 if (values.some(value=>String(value).includes(`[WS] Restarted CLI for ${sdkIdentity} with runtime override`))) observedRestart?.()
}
await import('../../../../preload')
const { startServer } = await import('../../../../src/server/index')
const { conversationService } = await import('../../../../src/server/services/conversationService')
const server = startServer(0, '127.0.0.1')
serverOrigin = `http://127.0.0.1:${server.port}`
const created = await fetch(serverOrigin + '/api/sessions', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ workDir: input.work, permissionMode: 'default', customTitle: 'Private plan restart fixture' }) })
if (created.status !== 201) throw new Error('Source public session creation failed: ' + await created.text())
const { sessionId } = await created.json() as any
const frames: any[] = []
let socket: WebSocket
let settle: (value: any) => void, reject: (error: any) => void
const finished = new Promise((resolve, fail) => { settle = resolve; reject = fail })
const timeout = setTimeout(() => reject(new Error('Source actual plan server watchdog')), 90_000)
let applied = false, continued = false, nextSent = false, approvalSent=false, duplicateSent=false
let originalApproval:any
let manualComplete:()=>void
const manualFinished=new Promise<void>(resolve=>{manualComplete=resolve})
const restartFinished=new Promise<void>(resolve=>{observedRestart=()=>{
 if(input.scenario==='disconnect-queued') {
  const callback=(message:any)=>{if(message.type==='result'&&message.result==='PLAN_RUNTIME_PRIVATE_EXECUTION_OK'){conversationService.removeOutputCallback(sessionId,callback);manualComplete()}}
  conversationService.onOutput(sessionId,callback)
 }
 resolve()
}})
let secondary: WebSocket | undefined
let identityBeforeClose: boolean[]=[]
const scenario=input.scenario ?? 'normal'
const approve=(frame:any,target:WebSocket)=>target.send(JSON.stringify({ type:'permission_response',requestId:frame.requestId,allowed:true,runtimeOverride:input.override??{providerId:'execution',modelId:'fixture-execution'},permissionUpdates:[{type:'setMode',destination:'session',mode:'acceptEdits'}]}))
socket = new physicalWebSocket('ws' + serverOrigin.slice(4) + '/ws/' + sessionId)
const consume = (event:any, active:WebSocket, primary:boolean) => {
 const frame=JSON.parse(String(event.data));frames.push(frame)
 if (frame.type==='connected' && primary) active.send(JSON.stringify({type:'user_message',content:'PLAN_RUNTIME_PRIVATE_FIRST: Enter plan mode, read input.txt, write the plan, and ask for approval.'}))
 if (frame.type==='permission_request'&&!approvalSent) {
 approvalSent=true
  if (frame.toolName!=='ExitPlanMode') return reject(new Error('Unexpected Source permission: '+frame.toolName))
  if (scenario==='fallback') {
   secondary=new physicalWebSocket('ws'+serverOrigin.slice(4)+'/ws/'+sessionId)
   secondary.onmessage=event=>{
    const next=JSON.parse(String(event.data))
    if(next.type==='connected') {approve(frame,active);active.close()}
    consume(event,secondary!,false)
   }
  } else {
   originalApproval=frame;approve(frame,active)
   if(scenario==='disconnect') active.close()
  }
 }
 if(frame.type==='permission_resolved'&&scenario==='duplicate'&&!duplicateSent&&originalApproval){duplicateSent=true;approve(originalApproval,active)}
 if(frame.type==='runtime_config_applied'&&frame.providerId===(input.override?.providerId??'execution')) {
  applied=true
  if(scenario==='abort'||scenario==='close') void (async()=>{
   const ready=await fetch(input.executionURL+'/fixture-held-ready');if(!ready.ok) throw new Error('Physical model readiness unavailable');await ready.text()
   if(scenario==='abort') active.send(JSON.stringify({type:'stop_generation'}));else settle({sessionId})
  })().catch(reject)
 }
 if(frame.type==='user_message_replay'&&frame.content==='The plan is approved. Continue with the implementation.') continued=true
 if(frame.type==='content_delta'&&frame.text==='PLAN_RUNTIME_EXECUTION_HELD') {
  if(scenario==='abort') active.send(JSON.stringify({type:'stop_generation'}))
  if(scenario==='close') settle({sessionId})
 }
 if(frame.type==='message_complete'&&scenario==='abort'&&applied) settle({sessionId})
 else if(frame.type==='message_complete'&&applied&&continued&&!nextSent) {
  nextSent=true;active.send(JSON.stringify({type:'user_message',content:'PLAN_RUNTIME_PRIVATE_NEXT: Read the existing execution marker and report it.'}))
 } else if(frame.type==='message_complete'&&nextSent) settle({sessionId})
 if(frame.type==='error') reject(new Error('Actual Source plan server error: '+frame.code+':'+frame.message))
}
socket.onmessage=event=>consume(event,socket,true)
socket.onclose=()=>clientEOF?.()
if(scenario==='disconnect'||scenario==='disconnect-queued') void (async()=>{
 await clientClosed
 await restartFinished
 if(scenario==='disconnect-queued') await manualFinished
 // A physical public HTTP round trip follows the original restart's return;
 // no timeout or fixed delay stands in for active-client/readiness proof.
 const response=await fetch(serverOrigin+'/api/sessions/'+sessionId)
 if(!response.ok) throw new Error('Actual restarted Source session unavailable')
 await response.text()
 settle({sessionId})
})().catch(reject)

socket.onerror = event => reject(new Error('Actual Source client socket error'))
let failure: any
try { await finished } catch (error) { failure = error }
clearTimeout(timeout)
socket.close();secondary?.close()
identityBeforeClose=processTokens.map(token=>conversationService.authorizeSdkConnection(sessionId,token))
conversationService.stopAllSessions()
const exited=await Promise.allSettled(processes.map(process => process.exited))
server.stop(true)
const output = { sessionId, frames, processReceipts: processes.map((child,index) => {
 let absent=false
 try {process.kill(child.pid,0)} catch(error:any) {if(error.code==='ESRCH') absent=true;else throw error}
 return {pid:child.pid,exitCode:child.exitCode,signalCode:child.signalCode,exited:exited[index],absent}
}), identityBeforeClose,identityAfterClose:processTokens.map(token=>conversationService.authorizeSdkConnection(sessionId,token)),capabilities, failure: failure instanceof Error ? failure.message : failure ?? null }
fs.writeFileSync(path.join(home, 'original-plan-server-receipt.json'), JSON.stringify(output))
process.stdout.write(JSON.stringify(output))
process.exit(failure ? 1 : 0)
