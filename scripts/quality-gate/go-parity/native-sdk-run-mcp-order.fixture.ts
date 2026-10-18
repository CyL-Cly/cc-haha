import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

if (Bun.version !== '1.3.14' || process.env.CC_HAHA_SDK_RUN_MCP_ORDER !== '1')
  throw new Error('Explicit pinned SDK run MCP order fixture required')
const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(process.env.HOME, 'Actual SDK run MCP ordering')
const origin = new URL(input.origin)
if (origin.origin !== input.origin || origin.protocol !== 'http:' || origin.hostname !== '127.0.0.1' || !origin.port || process.env.ANTHROPIC_BASE_URL !== input.origin || process.env.ANTHROPIC_API_KEY !== 'offline-fixture-key')
  throw new Error('Exact disposable loopback model origin required')
const absent = [...absentManagedSettingsFixturePaths()]
for (let directory = dirname(home);; directory = dirname(directory)) {
  absent.push(join(directory, '.git'))
  if (dirname(directory) === directory) break
}
const capabilities = await installClientFixtureCapabilities(home, { loopbackFetchOrigin: input.origin, absentPaths: absent })
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
const lifecycle = await import('../../../src/utils/commandLifecycle')
const phases: string[] = []
let dequeues = 0, starts = 0
const dequeue = queue.dequeue
spyOn(queue, 'dequeue').mockImplementation((...args) => {
  const result = dequeue(...args)
  if (result) {
    dequeues++
    phases.push('dequeue')
  }
  return result
})
const notify = lifecycle.notifyCommandLifecycle
spyOn(lifecycle, 'notifyCommandLifecycle').mockImplementation((id, event) => {
  if (event === 'started') starts++
  return notify(id, event)
})
const { Server } = await import('@modelcontextprotocol/sdk/server/index.js')
const { ListToolsRequestSchema } = await import('@modelcontextprotocol/sdk/types.js')
const { SdkControlServerTransport } = await import('../../../src/services/mcp/SdkControlTransport')
const pending = new Map<string, string>()
const responses: string[] = []
let wake: (() => void) | undefined, closed = false
const observations: unknown[] = []
const transport = new SdkControlServerTransport(message => {
  const requestId = pending.get(String(message.id))
  if (!requestId) throw new Error('SDK response must belong to its actual pending control request')
  pending.delete(String(message.id))
  responses.push(JSON.stringify({ type: 'control_response', response: { subtype: 'success', request_id: requestId, response: { mcp_response: message } } }) + '\n')
  wake?.()
})
const server = new Server({ name: 'actual-order-peer', version: '1' }, { capabilities: { tools: {} } })
server.setRequestHandler(ListToolsRequestSchema, async () => ({ tools: [] }))
await server.connect(transport)
const { Stream } = await import('../../../src/utils/stream')
const enqueue = Stream.prototype.enqueue
spyOn(Stream.prototype, 'enqueue').mockImplementation(function (value) {
  const result = enqueue.call(this, value)
  if (value?.type === 'control_request' && value.request?.subtype === 'mcp_message') {
    const message = value.request.message
    if (message.method === 'initialize') {
      phases.push('SDK initialize')
      observations.push({ phase: 'SDK initialize', dequeues, starts, queued: queue.getCommandQueue().map(command => ({ value: command.value, uuid: command.uuid })) })
    }
    if (message.id !== undefined) pending.set(String(message.id), value.request_id)
    transport.onmessage?.(message)
    if (message.id === undefined) {
      responses.push(JSON.stringify({ type: 'control_response', response: { subtype: 'success', request_id: value.request_id, response: {} } }) + '\n')
      wake?.()
    }
  }
  if (value?.type === 'result') {
    closed = true
    wake?.()
  }
  return result
})
const accepted = async function* () {
  yield JSON.stringify({ type: 'control_request', request_id: 'order-init', request: { subtype: 'initialize', sdkMcpServers: ['order-peer'] } }) + '\n'
  yield JSON.stringify({ type: 'user', uuid: 'order-user', message: { role: 'user', content: 'SDK_ORDER_HUMAN' } }) + '\n'
  while (!closed) {
    if (responses.length) yield responses.shift()!
    else await new Promise<void>(resolve => { wake = resolve })
  }
}
process.on('exit', code => {
  fs.writeFileSync(join(home, 'sdk-run-mcp-order-result.json'), JSON.stringify({ code, observations, phases, dequeues, starts, capabilities }))
})
const { runHeadless } = await import('../../../src/cli/print')
await runHeadless(accepted(), store.getState, store.setState, [], [], {}, [], {
  verbose: true, outputFormat: 'stream-json', replayUserMessages: true, includePartialMessages: true,
  userSpecifiedModel: 'fixture-model', thinkingConfig: { type: 'disabled' }, maxTurns: 1,
  systemPrompt: 'original SDK ordering system', allowedTools: [], enableAuthStatus: false,
} as Parameters<typeof runHeadless>[7])
