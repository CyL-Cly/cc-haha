import * as fs from 'node:fs'
import * as fsp from 'node:fs/promises'
import * as crypto from 'node:crypto'
import { dirname, isAbsolute, join, relative, sep } from 'node:path'
import { spyOn } from 'bun:test'
import { feature } from 'bun:bundle'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
if (!['submit-now-at-stop','user-interrupt-at-stop','finish-after-stop','server-boundary-late-usage','empty-first','eof-first','usage-fields','synthetic-no-response','synthetic-cancel','synthetic-reject','preflight-blocked'].includes(input.probeMode)) throw new Error('Declared original SDK/transport schedule required')
const home = assertDisposableFixtureHome(input.home, 'Actual headless session controller')
const keys = ['home', 'origin', 'temporary', 'sid', 'continue', 'forkSession', 'checkpointing', 'disabled', 'resumeInterrupted', 'resumeAt', 'model', 'sdk', 'stat', 'runtime', 'holdCopies', 'probeMode']
if (process.env.CC_HAHA_SESSION_CONTROLLER_FIXTURE !== '1' || process.env.NODE_ENV !== 'production' || process.env.TZ !== 'UTC' || !feature('TRANSCRIPT_CLASSIFIER') || Object.keys(input).some(key => !keys.includes(key)) || !Array.isArray(input.sdk) || !input.stat || fs.realpathSync(home) !== home)
  throw new Error('Headless controller requires original canonical offline inputs')
const origin = new URL(input.origin)
if (origin.origin !== input.origin || origin.protocol !== 'https:' || origin.hostname !== '127.0.0.1' || !origin.port || input.origin !== process.env.ANTHROPIC_BASE_URL || process.env.ANTHROPIC_API_KEY !== 'offline-fixture-key')
  throw new Error('Headless controller requires exact original loopback')
function guard(path: string) {
  const part = relative(home, path)
  if (part === '..' || part.startsWith('..' + sep) || isAbsolute(part))
    throw new Error('Headless controller ordinary private files required')
  for (let probe = path;; probe = dirname(probe)) {
    try {
      const info = fs.lstatSync(probe)
      if (info.isSymbolicLink() || probe === path && !info.isFile())
        throw new Error('Headless controller ordinary private files required')
      const part = relative(home, fs.realpathSync(probe))
      if (part === '..' || part.startsWith('..' + sep) || isAbsolute(part))
        throw new Error('Headless controller ordinary private files required')
      break
    }
    catch (e) {
      if ((e as NodeJS.ErrnoException).code !== 'ENOENT' || probe === dirname(probe))
        throw e
    }
  }
}
for (const path of Object.keys(input.stat))
  guard(path)
guard(join(home, 'sdk-full-ask-result.json'))
const exactAbsentPaths: string[] = []
if (input.sdk.length)
  for (let directory = dirname(home);; directory = dirname(directory)) {
    exactAbsentPaths.push(join(directory, '.git'))
    if (dirname(directory) === directory)
      break
  }
const capabilities = await installClientFixtureCapabilities(home, { loopbackFetchOrigin: input.origin, absentPaths: exactAbsentPaths })
let releaseCopy: () => void = () => { }, copyStarted = false
const copyBarrier = input.holdCopies ? new Promise<void>(resolve => {
  releaseCopy = resolve
}) : Promise.resolve()
const originalLink = fsp.link
if (input.holdCopies)
  spyOn(fsp, 'link').mockImplementation(async (oldPath, newPath) => {
    if (String(oldPath) !== join(home, '.claude', 'file-history', input.sid, 'backup') || String(newPath) !== join(home, '.claude', 'file-history', input.temporary, 'backup'))
      throw new Error('Only original private Link barrier is allowed')
    copyStarted = true
    await copyBarrier
    return originalLink(oldPath, newPath)
  })
process.env.USER_TYPE = 'ant'
process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = join(home, 'policy')
process.env.CLAUDE_CODE_SIMPLE = '1'
process.env.CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING = String(input.checkpointing)
process.env.CLAUDE_CODE_RESUME_INTERRUPTED_TURN = input.resumeInterrupted
;(globalThis as any).MACRO = { BUILD_TIME: '', VERSION: 'fixture-version', PACKAGE_URL: '', README_URL: '', ISSUES_URL: '' }
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(home)
bootstrap.setOriginalCwd(home)
bootstrap.setProjectRoot(home)
bootstrap.setIsInteractive(false)
bootstrap.setSessionPersistenceDisabled(input.disabled)
bootstrap.switchSession(input.temporary)
bootstrap.setSdkBetas([])
bootstrap.setPromptCache1hEligible(false)
bootstrap.setPromptCache1hAllowlist([])
const settings = await import('../../../src/utils/settings/settings')
settings.getInitialSettings()
process.env.USER_TYPE = 'external'
const config = await import('../../../src/utils/config')
config.enableConfigs()
const permissions = await import('../../../src/utils/permissions/permissionSetup')
const permissionResult = await permissions.initializeToolPermissionContext({ allowedToolsCli: [], disallowedToolsCli: [], baseToolsCli: [''], permissionMode: 'default', allowDangerouslySkipPermissions: false, addDirs: [] })
const { getDefaultAppState } = await import('../../../src/state/AppStateStore'), { createStore } = await import('../../../src/state/store')
const store = createStore({ ...getDefaultAppState(), toolPermissionContext: permissionResult.toolPermissionContext })
const storage = await import('../../../src/utils/sessionStorage'), recovery = await import('../../../src/utils/conversationRecovery'), engine = await import('../../../src/QueryEngine'), queue = await import('../../../src/utils/messageQueueManager')
const snapshotRecords: unknown[] = [], copyPromises: Promise<unknown>[] = [], background: {
  returnedBeforeCopy?: boolean
} = {}
const fileHistory = await import('../../../src/utils/fileHistory'), oldCopy = fileHistory.copyFileHistoryForResume
spyOn(fileHistory, 'copyFileHistoryForResume').mockImplementation((...args) => {
  const pending = oldCopy(...args)
  copyPromises.push(pending)
  return pending
})
const oldSnapshot = storage.recordFileHistorySnapshot
spyOn(storage, 'recordFileHistorySnapshot').mockImplementation((messageId, snapshot, isSnapshotUpdate) => {
  snapshotRecords.push(encode({ type: 'file-history-snapshot', messageId, snapshot, isSnapshotUpdate }))
  return oldSnapshot(messageId, snapshot, isSnapshotUpdate)
})
const { registerCleanup } = await import('../../../src/utils/cleanupRegistry')
registerCleanup(async () => {
  releaseCopy()
  await Promise.all(copyPromises)
})
const rawErrors: unknown[] = [], errors: string[] = [], phases: unknown[] = [], allocations: unknown[] = [], asks: unknown[] = []
const logging = await import('../../../src/utils/log')
logging.attachErrorLogSink({ logError: e => {
    errors.push(e.message)
    rawErrors.push({ name: e.name, message: e.message, stack: e.stack })
  }, logMCPError: (name, e) => rawErrors.push({ name, error: String(e) }), logMCPDebug: (name, message) => rawErrors.push({ name, message }), getErrorsPath: () => join(home, 'errors'), getMCPLogsPath: name => join(home, 'mcp-' + name) })
let scope = 'startup', counter = 0
spyOn(crypto, 'randomUUID').mockImplementation(() => {
  const value = '00000000-0000-4000-' + (scope === 'turn' ? '0002' : '0001') + '-' + (++counter).toString().padStart(12, '0')
  allocations.push({ scope, kind: 'uuid', value })
  return value as ReturnType<typeof crypto.randomUUID>
})
const OriginalDate = Date
globalThis.Date = class extends OriginalDate {
  constructor(...args: unknown[]) {
    super(...(args.length ? args : [1700000000000]) as [
      number
    ])
    if (!args.length)
      allocations.push({ scope, kind: 'time', value: this.toISOString() })
  }
  static now() {
    return 1700000000000
  }
} as DateConstructor
function encode(value: unknown) {
  if (value === undefined)
    return null
  return JSON.parse(JSON.stringify(value, (_k, v) => v instanceof Map ? [...v] : v instanceof Set ? [...v] : v))
}
let activationHistory: unknown[] = []
const oldLoad = recovery.loadConversationForResume
spyOn(recovery, 'loadConversationForResume').mockImplementation(async (...args) => {
  scope = 'resume'
  phases.push({ phase: 'resume:start', args })
  const result = await oldLoad(...args)
  if (input.holdCopies)
    background.returnedBeforeCopy = snapshotRecords.length === 0
  phases.push({ phase: 'resume:return', result: encode(result) })
  return result
})
const restoreState = await import('../../../src/utils/sessionRestore')
const originalRestoreState = restoreState.restoreSessionStateFromLog
spyOn(restoreState, 'restoreSessionStateFromLog').mockImplementation((...args) => {
  phases.push({ phase: 'state:restore' })
  return originalRestoreState(...args)
})
const originalRestoreMetadata = storage.restoreSessionMetadata
spyOn(storage, 'restoreSessionMetadata').mockImplementation((...args) => {
  phases.push({ phase: 'metadata:restore' })
  return originalRestoreMetadata(...args)
})
const oldSwitch = bootstrap.switchSession
spyOn(bootstrap, 'switchSession').mockImplementation((...args) => {
  phases.push({ phase: 'switch', args })
  return oldSwitch(...args)
})
const oldReset = storage.resetSessionFilePointer
spyOn(storage, 'resetSessionFilePointer').mockImplementation(async () => {
  phases.push({ phase: 'reset' })
  return oldReset()
})
for (const name of ['enqueue', 'dequeue'] as const) {
  const original = queue[name]
  spyOn(queue, name).mockImplementation(((...args: unknown[]) => {
    const result = (original as any)(...args)
    phases.push({ phase: name, result: encode(result) })
    if (name === 'enqueue')
      enqueued.push(encode(queue.getCommandQueue().at(-1)))
    return result
  }) as any)
}
let activeMessages: unknown[] = []
let askCount=0
const bufferEvents:unknown[]=[]
const liveAssistantReferences:unknown[]=[]
const appendEvents:unknown[]=[]
const outputEvents:unknown[]=[]
const stateSamples:unknown[]=[]
const actualPending:Promise<unknown>[]=[]
let resolveBlockStop:()=>void=()=>{},resolveFirstResult:()=>void=()=>{}
const observedBlockStop=new Promise<void>(resolve=>{resolveBlockStop=resolve})
const firstResult=new Promise<void>(resolve=>{resolveFirstResult=resolve})
const refIDs=new WeakMap<object,number>();let refCounter=0
function ref(value:any){if(!value||typeof value!=='object')return null;let id=refIDs.get(value);if(!id){id=++refCounter;refIDs.set(value,id)}return id}
function liveRefs(){return liveAssistantReferences.map((value:any)=>({envelopeRef:ref(value),messageRef:ref(value.message),source:JSON.stringify(value)}))}
function sample(phase:string){stateSamples.push({phase,ask:askCount,messages:encode(activeMessages),cache:encode(cache),liveRefs:liveRefs(),transcripts:transcripts()})}
const caPath=join(home,'original-ca.pem')
const controlTLS={ca:fs.readFileSync(caPath,'utf8')}
function control(path:string){const pending=fetch(input.origin+path,{tls:controlTLS} as any).then(response=>{if(!response.ok)throw new Error('private control failed '+path);return response.text()});actualPending.push(pending);return pending}
let firstUserNotified=false, firstAssistantNotified=false
const realOpen=fsp.open
spyOn(fsp,'open').mockImplementation(async(...args:any[])=>{const handle=await (realOpen as any)(...args);const path=String(args[0]);if(path.startsWith(join(home,'.claude','projects')+sep)){const append=handle.appendFile.bind(handle);handle.appendFile=async(...writeArgs:any[])=>{const content=String(writeArgs[0]);await append(...writeArgs);appendEvents.push({ask:askCount,path:relative(home,path),content,liveRefs:liveRefs()});phases.push({phase:'Project:append',content});if(!firstUserNotified&&content.includes('"uuid":"first-sdk-id"')){firstUserNotified=true;sample('first-user-natural-append');void control('/__fixture/first-user-persisted')}if(!firstAssistantNotified&&content.includes('"id":"probe-first"')){firstAssistantNotified=true;sample('first-assistant-natural-append');if(input.probeMode==='server-boundary-late-usage')void control('/__fixture/release-final')}return undefined}}return handle})
const {StreamAssistantCommitBuffer}=await import('../../../src/services/api/streamAssistantCommitBuffer')
const actualAdd=StreamAssistantCommitBuffer.prototype.add
spyOn(StreamAssistantCommitBuffer.prototype,'add').mockImplementation(function(value:any,kind:string){liveAssistantReferences.push(value);const result=actualAdd.call(this,value,kind);bufferEvents.push({operation:'add',ask:askCount,kind,value:JSON.stringify(value),envelopeRef:ref(value),messageRef:ref(value.message),result:result.map((m:any)=>JSON.stringify(m)),crossed:this.hasCrossedSideEffectBoundary()});return result})
for(const name of ['flush','flushWithoutToolUse'] as const){const actual=StreamAssistantCommitBuffer.prototype[name];spyOn(StreamAssistantCommitBuffer.prototype,name).mockImplementation(function(){const result=actual.call(this);bufferEvents.push({operation:name,ask:askCount,result:result.map((m:any)=>JSON.stringify(m)),refs:result.map((m:any)=>({envelopeRef:ref(m),messageRef:ref(m.message)}))});return result})}
let cache: unknown[] = []
const queryHelpers = await import('../../../src/utils/queryHelpers'), oldExtract = queryHelpers.extractReadFilesFromMessages
spyOn(queryHelpers, 'extractReadFilesFromMessages').mockImplementation((...args) => {
  const result = oldExtract(...args)
  activationHistory = encode(args[0])
  activeMessages = args[0]
  cache = [...result.entries()].map(([path, value]) => ({ path, value }))
  releaseCopy()
  return result
})
const oldAsk = engine.ask
spyOn(engine, 'ask').mockImplementation(async function* (args) {
  scope = 'turn'
  askCount++
  activeMessages = args.mutableMessages
  activationHistory = encode(args.mutableMessages)
  cache = [...args.getReadFileCache().entries()].map(([path, value]) => ({ path, value }))
  asks.push({ before: encode(args.mutableMessages), cache: encode(cache) })
  phases.push({ phase: 'ask:start' })
  const setCache=args.setReadFileCache
  args={...args,setReadFileCache(value:any){setCache(value);cache=[...value.entries()].map(([path,value])=>({path,value}));phases.push({phase:'cache:finally',ask:askCount,cache:encode(cache)});sample('cache-finally')}}
  args.abortController.signal.addEventListener('abort',()=>{const reason=args.abortController.signal.reason;phases.push({phase:'abort',ask:askCount,reason:reason instanceof Error?{name:reason.name,message:reason.message}:reason});sample('abort-before-model-buffer-flush')})
  try {yield* oldAsk(args)}finally {phases.push({phase:'ask:finally',ask:askCount,messages:encode(args.mutableMessages)});sample('ask-finally')}
  phases.push({ phase: 'ask:end', messages: encode(args.mutableMessages) })
})
const uuidChecks: unknown[] = [], enqueued: unknown[] = [], replays: unknown[] = []
const oldExists = storage.doesMessageExistInSession
spyOn(storage, 'doesMessageExistInSession').mockImplementation(async (sid, uuid) => {
  const exists = await oldExists(sid, uuid)
  uuidChecks.push({ uuid, exists })
  return exists
})
const { Stream } = await import('../../../src/utils/stream')
const oldOutput = Stream.prototype.enqueue
spyOn(Stream.prototype, 'enqueue').mockImplementation(function (value) {
  outputEvents.push(encode(value))
  if(value?.type==='stream_event'&&value.event?.type==='content_block_stop'&&askCount===1&&value.event.index===0){sample('SDK-block-stop-before-assistant-commit');resolveBlockStop()}
  if(value?.type==='stream_event'&&value.event?.type==='message_delta'){sample('SDK-message-delta-live-usage')}
  if(value?.type==='assistant'){sample('SDK-assistant-accepted')}
  phases.push({phase:'FIFO:enqueue',ask:askCount,type:value?.type,source:JSON.stringify(value)})
  if(value?.type==='result'&&askCount===1){sample('first-SDK-result');resolveFirstResult()}
  if (value?.type === 'user' && value.isReplay)
    replays.push(encode(value))
  return oldOutput.call(this, value)
})
const lifecycle = await import('../../../src/utils/commandLifecycle'), oldLifecycle = lifecycle.notifyCommandLifecycle
spyOn(lifecycle, 'notifyCommandLifecycle').mockImplementation((uuid, event) => {
  phases.push({ phase: 'lifecycle:' + event, uuid })
  return oldLifecycle(uuid, event)
})
const records: unknown[] = [], oldRecord = storage.recordTranscript
spyOn(storage, 'recordTranscript').mockImplementation(async (...args) => {
  records.push({ask:askCount,messages:encode(args[0]),refs:args[0].filter((m:any)=>m.type==='assistant').map((m:any)=>({envelopeRef:ref(m),messageRef:ref(m.message)}))})
  phases.push({ phase: 'record', messages: encode(args[0]) })
  const receipt = oldRecord(...args)
  phases.push({phase:'Record:returned-promise',ask:askCount})
  receipt.then(()=>phases.push({phase:'Record:fulfilled',ask:askCount}),error=>phases.push({phase:'Record:rejected',ask:askCount,error:String(error)}))
  return receipt
})
const oldFlush = storage.flushSessionStorage
spyOn(storage, 'flushSessionStorage').mockImplementation(async () => {
  phases.push({ phase: 'flush' })
  const result=await oldFlush()
  phases.push({phase:'flush:fulfilled',ask:askCount})
  return result
})
const originalNormalize=queryHelpers.normalizeMessage
spyOn(queryHelpers,'normalizeMessage').mockImplementation(function* (message:any){phases.push({phase:'normalize:start',ask:askCount,type:message.type});try{for(const value of originalNormalize(message)){phases.push({phase:'normalize:yield',ask:askCount,source:JSON.stringify(value)});yield value}}finally{phases.push({phase:'normalize:finally',ask:askCount})}})
const costs=await import('../../../src/cost-tracker')
const originalCost=costs.addToTotalSessionCost
spyOn(costs,'addToTotalSessionCost').mockImplementation((...args:any[])=>{const result=(originalCost as any)(...args);phases.push({phase:'cost:added',ask:askCount,args:encode(args),result});return result})
const requests: string[] = [], fetchOriginal = fetch
;(globalThis as any).fetch = (async (url: unknown, init?: RequestInit) => {
  if (init?.method === 'POST') {
    requests.push(String(init.body))
    phases.push({ phase: 'HTTP', messages: encode(activeMessages) })
  }
  return fetchOriginal(url as any, init)
})
function transcripts(directory = join(home, '.claude', 'projects'), result: Record<string, string> = {}) {
  try {
    for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
      const path = join(directory, entry.name)
      if (entry.isDirectory())
        transcripts(path, result)
      else if (entry.isFile()) {
        guard(path)
        result[relative(home, path)] = fs.readFileSync(path, 'utf8')
      }
      else
        throw new Error('unexpected fixture symlink')
    }
  }
  catch (e) {
    if ((e as NodeJS.ErrnoException).code !== 'ENOENT')
      throw e
  }
  return result
}
process.on('exit', code => {
  const state = store.getState(), out = encode({ activationHistory, snapshotRecords, background, appState: { FileHistory: state.fileHistory, Todos: state.todos }, cache, uuidChecks, enqueued, replays, boundaryOrder: phases.slice(0, phases.some((value: any) => value.phase === 'HTTP') ? phases.findIndex((value: any) => value.phase === 'HTTP') + 1 : phases.length).filter((value: any) => ['resume:start', 'resume:return', 'switch', 'reset', 'state:restore', 'metadata:restore', 'enqueue', 'lifecycle:started', 'lifecycle:completed', 'ask:start', 'record', 'HTTP'].includes(value.phase) || value.phase === 'dequeue' && value.result).map((value: any) => value.phase), historySource: activeMessages.map(value => JSON.stringify(value)), requests, transcripts: transcripts(), copies: transcripts(join(home, '.claude', 'file-history')), errors })
  // SDK acknowledgements, allocation stream and all post-request host output are
  // retained raw alongside the finite migrated boundary; none feeds Go input.
  fs.writeFileSync(join(home, 'sdk-full-ask-result.json'), JSON.stringify({ observation: out, sourceOnly: { code, phases, asks, records, allocations, capabilities, exactAbsentPaths, rawErrors, permissionResult, copyStarted, bufferEvents, appendEvents, outputEvents, stateSamples, liveRefs:liveRefs(), asksCompleted:askCount } }))
})
const { runHeadless } = await import('../../../src/cli/print')
await runHeadless((async function* () {
  phases.push({phase:'original-SDK:first',message:input.sdk[0]})
  yield JSON.stringify(input.sdk[0])+'\n'
  if(!['empty-first','preflight-blocked'].includes(input.probeMode))await observedBlockStop
  sample('original-SDK:at-block-stop')
  if(input.probeMode==='submit-now-at-stop'){phases.push({phase:'original-SDK:second-now',message:input.sdk[1]});yield JSON.stringify(input.sdk[1])+'\n'}else if(input.probeMode==='user-interrupt-at-stop'){phases.push({phase:'original-SDK:control-interrupt',message:input.sdk[1]});yield JSON.stringify(input.sdk[1])+'\n';await firstResult;phases.push({phase:'original-SDK:second-after-result',message:input.sdk[2]});yield JSON.stringify(input.sdk[2])+'\n'}else{if(['finish-after-stop','usage-fields','eof-first','synthetic-no-response','synthetic-cancel','synthetic-reject'].includes(input.probeMode))await control('/__fixture/release-final');await firstResult;phases.push({phase:'original-SDK:second-after-result',message:input.sdk[1]});yield JSON.stringify(input.sdk[1])+'\n'}
  phases.push({phase:'original-SDK:EOF'})
})(), () => store.getState(), store.setState, [], [], {}, [], { resume: input.continue ? undefined : input.sid, continue: input.continue, resumeSessionAt: input.resumeAt || undefined, verbose: true, outputFormat: 'stream-json', jsonSchema: undefined, permissionPromptToolName: undefined, allowedTools: [], thinkingConfig: { type: 'disabled' }, maxTurns: 1, maxBudgetUsd: undefined, taskBudget: undefined, systemPrompt: 'original custom system', appendSystemPrompt: undefined, userSpecifiedModel: input.model, fallbackModel: undefined, teleport: undefined, sdkUrl: undefined, replayUserMessages: true, includePartialMessages: true, forkSession: input.forkSession, rewindFiles: undefined, enableAuthStatus: false, agent: undefined, workload: undefined })
