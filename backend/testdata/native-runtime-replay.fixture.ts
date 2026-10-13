import { parse } from 'acorn'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME,'Actual normalized runtime replay')
if(process.env.CC_HAHA_RUNTIME_REPLAY_FIXTURE!=='1')throw new Error('Unowned runtime replay fixture')
const input=await Bun.stdin.json()
const source=new Bun.Transpiler({loader:'ts'}).transformSync(await Bun.file(new URL('../../src/server/ws/handler.ts',import.meta.url)).text())
const ast:any=parse(source,{ecmaVersion:'latest',sourceType:'module'})
const declaration=ast.body.find((node:any)=>node.type==='FunctionDeclaration'&&node.id.name==='handleSetRuntimeConfig')
if(!declaration)throw new Error('Original runtime consumer missing')
const rows=[]
for(const row of input.rows){
  const calls:string[]=[],overrides=new Map(),deferred=new Map()
  if(row.current)overrides.set('private-session',row.current)
  if(row.deferred)deferred.set('private-session',row.current)
  const dependencies={
    enqueueRuntimeTransition:async(_:string,work:()=>Promise<void>)=>work(),rejectStartedSideChatProviderChange:()=>false,
    // This corpus starts after real provider/model/effort normalization. The
    // original normalizer has independent Source and physical HTTP coverage.
    normalizeRuntimeOverrideInput:async()=>({ok:true,override:row.next}),
    rejectedRuntimeConfigs:new Map(),sendMessage:()=>calls.push('error'),getSideChat:()=>null,runtimeOverrides:overrides,
    conversationService:{hasSession:()=>true},persistSessionRuntimeConfig:async()=>calls.push('persist'),broadcastAppliedRuntimeConfig:()=>calls.push('ack'),
    deferredRuntimeRestarts:deferred,runtimeOverrideVersions:new Map(),shouldDeferRuntimeRestartForActiveTurn:()=>!!row.active,
    sessionStartupPromises:new Map(),sessionStartupRuntimeVersions:new Map(),restartSessionWithRuntimeConfig:async()=>calls.push('restart'),
  }
  const run=new Function(...Object.keys(dependencies),source.slice(declaration.start,declaration.end)+'\nreturn handleSetRuntimeConfig')(...Object.values(dependencies))
  await run({data:{sessionId:'private-session'}},{type:'set_runtime_config',...row.next})
  rows.push({calls,override:overrides.get('private-session'),deferred:deferred.has('private-session')})
}
process.stdout.write(JSON.stringify({rows}))
