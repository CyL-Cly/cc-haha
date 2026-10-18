import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

if (Bun.version !== '1.3.14' || process.env.CC_HAHA_SDK_WORKLOAD_RETURN !== '1')
  throw new Error('Explicit pinned SDK workload return fixture required')
const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(process.env.HOME, 'Actual SDK workload return')
const origin = new URL(input.origin)
if (origin.origin !== input.origin || origin.protocol !== 'http:' || origin.hostname !== '127.0.0.1' || !origin.port || process.env.ANTHROPIC_BASE_URL !== input.origin || process.env.ANTHROPIC_API_KEY !== 'offline-fixture-key')
  throw new Error('Exact disposable loopback model origin required')
const absent = [...absentManagedSettingsFixturePaths()]
for (let directory = dirname(home);; directory = dirname(directory)) {
  const path = join(directory, '.git')
  if (fs.existsSync(path)) throw new Error('The isolated ancestor must have no repository metadata')
  absent.push(path)
  if (dirname(directory) === directory) break
}
for (const name of ['sdk-workload-input.fifo', 'sdk-workload-enqueued.fifo']) {
  if (!fs.lstatSync(join(home, name)).isFIFO()) throw new Error('Actual private named pipes required')
}
const capabilities = await installClientFixtureCapabilities(home, { loopbackFetchOrigin: input.origin, absentPaths: absent })
const accepted = fs.createReadStream(join(home, 'sdk-workload-input.fifo'), { encoding: 'utf8' })
const enqueued = fs.openSync(join(home, 'sdk-workload-enqueued.fifo'), 'w')
await import('../../../preload')
Object.assign(globalThis, { MACRO: { VERSION: 'fixture', BUILD_TIME: '' } })
const state = await import('../../../src/bootstrap/state')
state.setCwdState(home)
state.setOriginalCwd(home)
state.setProjectRoot(home)
state.setAllowedSettingSources(['userSettings'])
state.setIsInteractive(false)
state.setSessionPersistenceDisabled(true)
;(await import('../../../src/utils/config')).enableConfigs()
const permissions = await import('../../../src/utils/permissions/permissionSetup')
const permission = await permissions.initializeToolPermissionContext({ allowedToolsCli: [], disallowedToolsCli: [], baseToolsCli: [''], permissionMode: 'default', allowDangerouslySkipPermissions: false, addDirs: [] })
const { getDefaultAppState } = await import('../../../src/state/AppStateStore')
const { createStore } = await import('../../../src/state/store')
const store = createStore({ ...getDefaultAppState(), toolPermissionContext: permission.toolPermissionContext })
const queue = await import('../../../src/utils/messageQueueManager')
const workload = await import('../../../src/utils/workloadContext')
const events: string[] = []
const observations: Array<{ dequeues: number; queued: boolean }> = []
const identities: boolean[] = []
let dequeues = 0, enqueues = 0
const enqueue = queue.enqueue
spyOn(queue, 'enqueue').mockImplementation(command => {
  const returned = enqueue(command)
  enqueues++
  if (enqueues === 2) fs.writeSync(enqueued, 'second command actually enqueued\n')
  return returned
})
const dequeue = queue.dequeue
spyOn(queue, 'dequeue').mockImplementation((...args) => {
  const returned = dequeue(...args)
  if (returned) {
    dequeues++
    events.push('dequeue')
  }
  return returned
})
const runWithWorkload = workload.runWithWorkload
spyOn(workload, 'runWithWorkload').mockImplementation(<T>(tag: string | undefined, callback: () => T): T => {
  // Observe the original async callback's Promise before print awaits it.
  // Return that exact object: no wrapper callback, adoption, I/O or extra await.
  const returned = runWithWorkload(tag, callback)
  if (!(returned instanceof Promise)) throw new Error('Actual print async callback must return its Promise')
  identities.push(Promise.resolve(returned) === returned)
  void returned.then(() => {
    events.push('callback-returned')
    observations.push({ dequeues, queued: queue.getCommandQueueLength() !== 0 })
  }, () => { events.push('callback-rejected') })
  return returned
})
process.on('exit', code => {
  fs.writeFileSync(join(home, 'sdk-workload-return-result.json'), JSON.stringify({ code, events, observations, identities, dequeues, enqueues, capabilities }))
})
const { runHeadless } = await import('../../../src/cli/print')
await runHeadless(accepted, store.getState, store.setState, [], [], {}, [], {
  verbose: true, outputFormat: 'stream-json', replayUserMessages: true, includePartialMessages: true,
  userSpecifiedModel: 'fixture-model', thinkingConfig: { type: 'disabled' }, maxTurns: 1,
  systemPrompt: 'original SDK workload return system', allowedTools: [], enableAuthStatus: false,
} as Parameters<typeof runHeadless>[7])
