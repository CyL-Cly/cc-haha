import fs from 'node:fs'
import { join } from 'node:path'
import { mock } from 'bun:test'
import axios, { AxiosError } from 'axios'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual remote-agent restore')
if (process.env.CC_HAHA_REMOTE_AGENT_RESTORE !== '1' || Bun.version !== '1.3.14' || !Array.isArray(input.metadata)) throw new Error('Owned pinned restore fixture required')
const peer = new URL(input.peer)
if (peer.protocol !== 'https:' || peer.hostname !== '127.0.0.1' || peer.origin !== input.peer || !peer.port) throw new Error('Exact trusted private API required')
const absentPaths = absentManagedSettingsFixturePaths()
for (const path of ['/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token']) if (!fs.existsSync(path)) absentPaths.push(path)
const caps = await installClientFixtureCapabilities(home, { absentPaths, loopbackFetchOrigin: input.peer })
;(globalThis as any).MACRO = { VERSION: '999.0.0-remote-task-fixture', BUILD_TIME: '' }
await import('../../../preload')
// Only the OS secure-storage primitive is replaced. The ordinary OAuth token,
// account/org getters, original Sessions API and task bodies execute unchanged.
const secure = await import('../../../src/utils/secureStorage/index')
mock.module('../../../src/utils/secureStorage/index', () => ({ ...secure, getSecureStorage: () => ({ read: () => {
  caps.secureReads.push('owned-remote-task-credentials')
  return JSON.parse(fs.readFileSync(join(home, '.claude', '.credentials.json'), 'utf8'))
} }) }))
const state = await import('../../../src/bootstrap/state')
state.setCwdState(home)
state.setOriginalCwd(home)
state.setProjectRoot(home)
state.setAllowedSettingSources(['userSettings'])
state.setIsInteractive(true)
state.switchSession(input.sid)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const http: unknown[] = []
axios.defaults.adapter = async request => {
  const original = new URL(request.url!)
  if (original.origin !== 'https://api.anthropic.com' || !original.pathname.startsWith('/v1/sessions/')) throw new Error('Unowned Source Sessions API URL')
  const target = new URL(original.pathname + original.search, input.peer)
  if (request.params?.after_id) target.searchParams.set('after_id', request.params.after_id)
  const wire = { url: request.url, method: request.method, timeout: request.timeout, headers: request.headers.toJSON(), body: request.data ? JSON.parse(request.data) : null, query: target.search }
  http.push(wire)
  // The actual official URL/auth producer remains visible above. The isolated
  // transport routes only that request to a real trusted private TLS socket.
  const response = await fetch(target, { method: request.method?.toUpperCase(), headers: request.headers.toJSON() as any, body: request.data, signal: AbortSignal.timeout(request.timeout || 30000) })
  const result = { data: await response.json(), status: response.status, statusText: response.statusText, headers: {}, config: request }
  if (!(request.validateStatus ?? (status => status >= 200 && status < 300))(response.status)) throw new AxiosError(`Request failed with status code ${response.status}`, response.status >= 500 ? AxiosError.ERR_BAD_RESPONSE : AxiosError.ERR_BAD_REQUEST, request, undefined, result)
  return result
}
const { getDefaultAppState } = await import('../../../src/state/AppStateStore')
const { createStore } = await import('../../../src/state/store')
const store = createStore(getDefaultAppState())
const storage = await import('../../../src/utils/sessionStorage')
const output = await import('../../../src/utils/task/diskOutput')
const task = await import('../../../src/tasks/RemoteAgentTask/RemoteAgentTask')
for (const meta of input.metadata) await storage.writeRemoteAgentMetadata(meta.taskId, meta)
const context = { abortController: new AbortController(), getAppState: store.getState, setAppState: store.setState }
const before = { ...store.getState().tasks }
await task.restoreRemoteAgentTasks(context)
const restored = { ...store.getState().tasks }
if (input.stopTask) await task.RemoteAgentTask.kill(input.stopTask, store.setState)
if (input.waitResult || input.waitLog) {
  // Observe the actual poll/setAppState completion, without replacing its
  // timer, fetching prepared events or issuing an extra business query.
  const expected = (input.waitResult || input.waitLog) as string
  await new Promise<void>((resolve, reject) => {
    const timeout = setTimeout(() => { unsubscribe(); process.stderr.write(JSON.stringify({ tasks: store.getState().tasks, http, secureReads: caps.secureReads, blocked: caps.blocked }) + '\n'); reject(new Error('Original remote task did not settle')) }, 12000)
    const inspect = () => {
      const value = store.getState().tasks[expected]
      if (value && (input.waitLog ? (value as any).log.length >= (input.logLength ?? 1) : value.status !== 'running' && value.notified)) { clearTimeout(timeout); unsubscribe(); resolve() }
    }
    const unsubscribe = store.subscribe(inspect)
    inspect()
  })
}
// Drain only existing real output work for the receipt after the task state
// consumer completed. It adds no await to the original restore/poller bodies.
for (const meta of input.metadata) await output.flushTaskOutput(meta.taskId)
const queue = await import('../../../src/utils/messageQueueManager')
const sdk = await import('../../../src/utils/sdkEventQueue')
const dir = join(storage.getProjectDir(home), input.sid, 'remote-agents')
const files = fs.existsSync(dir) ? fs.readdirSync(dir).map(name => ({ name, content: fs.readFileSync(join(dir, name), 'utf8') })) : []
const outputs = input.metadata.map(meta => {
  const path = output.getTaskOutputPath(meta.taskId)
  return { taskId: meta.taskId, path, exists: fs.existsSync(path), content: fs.existsSync(path) ? fs.readFileSync(path, 'utf8') : '' }
})
if (caps.blocked.length) throw new Error('Remote task restore escaped private capabilities: ' + JSON.stringify(caps.blocked))
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ before, restored, after: store.getState().tasks, files, outputs, queue: queue.getCommandQueueSnapshot(), sdk: sdk.drainSdkEvents(), http, caps }), () => resolve()))
process.exit(0)
