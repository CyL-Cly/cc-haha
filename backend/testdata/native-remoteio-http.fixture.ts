import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { installClientFixtureCapabilities, absentManagedSettingsFixturePaths } from '../../scripts/quality-gate/go-parity/client-options-capabilities'
assertDisposableFixtureHome(process.env.HOME, 'Actual RemoteIO HTTP producer')
if (process.env.CC_HAHA_REMOTEIO_FIXTURE !== '1') throw new Error('Unowned RemoteIO fixture')
const input = await Bun.stdin.json()
const origin = new URL(input.url).origin
const capabilities = await installClientFixtureCapabilities(process.env.HOME!, { absentPaths: absentManagedSettingsFixturePaths(), loopbackFetchOrigin: origin })
;(globalThis as any).MACRO = { VERSION: 'source-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../preload')
const { default: axios } = await import('axios')
// The approved transport primitive uses real loopback HTTP. Source Axios
// request construction, interceptors, CCR retry/state/batching and SSE reader
// remain unchanged; node's unrestricted sockets are still denied.
axios.defaults.adapter = async config => {
  const url = new URL(axios.getUri(config))
  if (url.origin !== origin || url.username || url.password) throw new Error('Unapproved CCR HTTP origin')
  const controller = new AbortController()
  const timer = config.timeout ? setTimeout(() => controller.abort(), config.timeout) : undefined
  const abort = () => controller.abort()
  config.signal?.addEventListener('abort', abort)
  try {
    const response = await fetch(url, { method: config.method, headers: config.headers.toJSON() as Record<string, string>, body: config.data, signal: controller.signal })
    return { data: await response.text(), status: response.status, statusText: response.statusText, headers: Object.fromEntries(response.headers), config }
  } finally {
    if (timer) clearTimeout(timer)
    config.signal?.removeEventListener('abort', abort)
  }
}
Object.assign(process.env, input.env)
if (input.sseNetwork) {
  const { getTransportForUrl } = await import('../../src/cli/transports/transportUtils')
  const { StructuredIO } = await import('../../src/cli/StructuredIO')
  const controls: any = new StructuredIO((async function* () {})())
  const refreshHeaders = () => ({ Authorization: 'Bearer ' + process.env.CLAUDE_CODE_SESSION_ACCESS_TOKEN, 'x-environment-runner-version': process.env.CLAUDE_CODE_ENVIRONMENT_RUNNER_VERSION! })
  const transport = getTransportForUrl(new URL(input.url), refreshHeaders(), 'network-source-session', refreshHeaders)
  const incoming: string[] = []
  let updated!: () => void
  const updateDone = new Promise<void>(resolve => updated = resolve)
  transport.setOnData(data => {
    incoming.push(data)
    if (JSON.parse(data).type === 'update_environment_variables') {
      void (async () => { await controls.processLine(data); await fetch(origin + '/release-network'); updated() })()
    }
  })
  let close!: (code?: number) => void
  const closed = new Promise<number | undefined>(resolve => close = resolve)
  transport.setOnClose(close)
  const connection = transport.connect()
  await updateDone
  const code = await closed
  await connection
  transport.close()
  if (capabilities.blocked.length) throw new Error('Original SSE network capability escape: ' + capabilities.blocked.join(', '))
  process.stdout.write(JSON.stringify({ code: code ?? null, incoming, capabilities }))
  process.exit(0)
}
if (input.sseTerminal) {
  const { getTransportForUrl } = await import('../../src/cli/transports/transportUtils')
  const transport = getTransportForUrl(new URL(input.url), {}, 'terminal-source-session')
  const incoming: string[] = []
  transport.setOnData(data => incoming.push(data))
  let close!: (code?: number) => void
  const closed = new Promise<number | undefined>(resolve => close = resolve)
  transport.setOnClose(close)
  const connection = transport.connect()
  const code = await closed
  await connection
  transport.close()
  if (capabilities.blocked.length) throw new Error('Original SSE terminal capability escape: ' + capabilities.blocked.join(', '))
  process.stdout.write(JSON.stringify({ code: code ?? null, incoming, capabilities }))
  process.exit(0)
}
if (input.localWithoutIngress) {
  const fs = await import('node:fs')
  // A complete private Git root keeps original branch discovery within this
  // allocation; no ancestor filesystem reads or process capability is granted.
  fs.mkdirSync(process.cwd() + '/.git', { recursive: true })
  fs.writeFileSync(process.cwd() + '/.git/HEAD', 'ref: refs/heads/source-fixture\n')
  const state = await import('../../src/bootstrap/state')
  state.setCwdState(process.cwd()); state.setOriginalCwd(process.cwd()); state.setProjectRoot(process.cwd()); state.setIsInteractive(false); state.setAllowedSettingSources(['userSettings']); state.switchSession('70707070-7070-4070-8070-707070707070')
  fs.mkdirSync(process.env.CLAUDE_CONFIG_DIR!, { recursive: true })
  const config = await import('../../src/utils/config')
  config.enableConfigs()
  const storage = await import('../../src/utils/sessionStorage')
  await storage.recordTranscript(input.messages)
  await storage.recordFileHistorySnapshot('local-user', { messageId: 'local-user', trackedFileBackups: {}, timestamp: '2026-10-01T00:00:00.000Z', future: { opaque: true } }, false)
  await storage.flushSessionStorage()
  const rows = fs.readFileSync(storage.getTranscriptPath(), 'utf8').trim().split('\n').map(line => JSON.parse(line))
  if (capabilities.blocked.length) throw new Error('Source local persistence capability escape: ' + capabilities.blocked.join(', '))
  process.stdout.write(JSON.stringify({ rows, capabilities }))
  process.exit(0)
}
if(input.legacy){
  const ingress=await import('../../src/services/api/sessionIngress')
  if(input.resume){
    const fs=await import('node:fs')
    const state=await import('../../src/bootstrap/state')
    state.setCwdState(process.cwd());state.setOriginalCwd(process.cwd());state.setProjectRoot(process.cwd());state.setIsInteractive(false);state.setAllowedSettingSources(['userSettings']);state.switchSession('90909090-9090-4090-8090-909090909090')
    fs.mkdirSync(process.env.CLAUDE_CONFIG_DIR!,{recursive:true})
    const config=await import('../../src/utils/config');config.enableConfigs()
    const storage=await import('../../src/utils/sessionStorage')
    const recovery=await import('../../src/utils/conversationRecovery')
    const sid='70707070-7070-4070-8070-707070707070'
    await storage.hydrateRemoteSession(sid,input.url)
    const loaded=await recovery.loadConversationForResume(sid)
    if(!loaded)throw new Error('Original hydrated history did not load')
    state.switchSession(loaded.sessionId!)
    await storage.recordTranscript(loaded.messages)
    await storage.flushSessionStorage()
    if (input.persistAfterRestore) {
      if (capabilities.blocked.length) throw new Error('Original v1 persistence capability escape before append: ' + capabilities.blocked.join(', '))
      await storage.recordTranscript([...loaded.messages, { type: 'user', uuid: 'source-new-v1', timestamp: '2026-10-01T00:00:02.000Z', message: { role: 'user', content: 'SOURCE_V1_NEW_USER' } }])
      await storage.flushSessionStorage()
    }
    if(capabilities.blocked.length)throw new Error('Original v1 resume capability escape: '+capabilities.blocked.join(', '))
    process.stdout.write(JSON.stringify({sessionId:loaded.sessionId,messageTypes:loaded.messages.map(row=>row.type),capabilities}))
    process.exit(0)
  }
  const logs=await ingress.getSessionLogs('fixture-v1',input.url)
  const results=[]
  for(const entry of input.messages??[])results.push(await ingress.appendSessionLog('fixture-v1',entry,input.url))
  if(capabilities.blocked.length)throw new Error('Original v1 ingress capability escape: '+capabilities.blocked.join(', '))
  process.stdout.write(JSON.stringify({logs,results,capabilities}))
  process.exit(0)
}
const { getTransportForUrl } = await import('../../src/cli/transports/transportUtils')
const { CCRClient } = await import('../../src/cli/transports/ccrClient')
const transport = getTransportForUrl(new URL(input.url), {}, 'source-session')
let receivedResolve!: (value: string) => void
const received = new Promise<string>(resolve => { receivedResolve = resolve })
transport.setOnData(receivedResolve)
const client = new CCRClient(transport, new URL(input.url), { onEpochMismatch: () => { throw new Error('fixture_epoch_mismatch') } })
let connection: Promise<void> | undefined
try {
  const initialization = client.initialize()
  connection = transport.connect()
  const metadata = await initialization
  const incoming = await received
  for (const row of input.messages ?? []) await client.writeEvent(row)
  for (const row of input.internal ?? []) await client.writeInternalEvent(row.type, row.payload, row.options)
  for (const row of input.delivery ?? []) client.reportDelivery(row.id, row.status)
  client.reportState('running')
  client.reportMetadata({ model: 'restored-fixture-model', permission_mode: 'plan' })
  await client.flush()
  await client.flushInternalEvents()
  await (client as any).deliveryUploader.flush()
  const state = (client as any).workerState
  while (state.inflight) await state.inflight
  const foreground = await client.readInternalEvents()
  const subagents = await client.readSubagentInternalEvents()
  if (capabilities.blocked.length) throw new Error(`Unexpected outside access: ${capabilities.blocked.join(', ')}`)
  process.stdout.write(JSON.stringify({ metadata, incoming, foreground, subagents, capabilities }))
} finally {
  client.close()
  transport.close()
  await connection
}
