import fs from 'node:fs'
import { join } from 'node:path'
import { mock } from 'bun:test'
import axios, { AxiosError } from 'axios'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await new Response(Bun.stdin.stream()).json()
const home = assertDisposableFixtureHome(input.home, 'Actual remote session manager')
if (process.env.CC_HAHA_REMOTE_MANAGER_FIXTURE !== '1' || Bun.version !== '1.3.14') throw new Error('Owned pinned manager required')
const peer = new URL(input.peer)
if (peer.protocol !== 'https:' || peer.hostname !== '127.0.0.1' || !peer.port || peer.origin !== input.peer || process.env.CLAUDE_LOCAL_OAUTH_API_BASE !== input.peer) throw new Error('Exact trusted private API required')
const OriginalWebSocket = globalThis.WebSocket
process.stderr.write("source: capabilities\n")
const absentPaths = absentManagedSettingsFixturePaths()
for (const path of ['/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token']) if(!fs.existsSync(path)) absentPaths.push(path)
const caps = await installClientFixtureCapabilities(home, { absentPaths, loopbackFetchOrigin: input.peer })
const connections: unknown[] = []
globalThis.WebSocket = class extends OriginalWebSocket {
  constructor(url: string | URL, options: any) {
    const target = new URL(String(url))
    if (target.origin !== input.peer.replace('https:', 'wss:') || target.pathname !== '/v1/sessions/ws/session_owned_manager/subscribe' || target.search !== '?organization_uuid=owned-org' || options.headers.Authorization !== 'Bearer owned-manager-token' || options.headers['anthropic-version'] !== '2023-06-01') throw new Error('Unowned WebSocket primitive')
    connections.push({ url: String(url), headers: options.headers })
    super(url, options)
  }
} as typeof WebSocket
;(globalThis as any).MACRO = { VERSION: '999.0.0-remote-fixture', BUILD_TIME: '' }
await import('../../../preload')
process.stderr.write('source: preload\n')
const secure = await import('../../../src/utils/secureStorage/index')
mock.module('../../../src/utils/secureStorage/index', () => ({ ...secure, getSecureStorage: () => ({ read: () => {
  caps.secureReads.push('owned-manager-credentials')
  return JSON.parse(fs.readFileSync(join(home, '.claude', '.credentials.json'), 'utf8'))
} }) }))
const state = await import('../../../src/bootstrap/state')
state.setCwdState(home); state.setOriginalCwd(home); state.setProjectRoot(home); state.setAllowedSettingSources(['userSettings']); state.setIsInteractive(true)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const http: unknown[] = []
axios.defaults.adapter = async request => {
  const url = new URL(request.url!)
  if (url.origin !== input.peer) throw new Error('Unowned Source HTTP URL')
  http.push({ url: request.url, method: request.method, timeout: request.timeout, headers: request.headers.toJSON(), body: JSON.parse(request.data) })
  const response = await fetch(request.url!, { method: request.method?.toUpperCase(), headers: request.headers.toJSON() as any, body: request.data, signal: AbortSignal.timeout(request.timeout || 30000) })
  const result = { data: await response.json(), status: response.status, statusText: response.statusText, headers: {}, config: request }
  if (response.status >= 500) throw new AxiosError('Private HTTP failure', AxiosError.ERR_BAD_RESPONSE, request, undefined, result)
  return result
}
const { getFeatureValue_CACHED_MAY_BE_STALE } = await import('../../../src/services/analytics/growthbook')
const gate = getFeatureValue_CACHED_MAY_BE_STALE('tengu_remote_backend', false)
const { RemoteSessionManager, createRemoteSessionConfig } = await import('../../../src/remote/RemoteSessionManager')
const incoming: unknown[] = [], states: string[] = [], permissions: unknown[] = []
let finish: () => void = () => {}
const disconnected = new Promise<void>(resolve => { finish = resolve })
process.stderr.write('source: manager\n')
const manager = new RemoteSessionManager(createRemoteSessionConfig('session_owned_manager', () => 'owned-manager-token', 'owned-org'), {
  onMessage: message => { process.stderr.write('source: SDK ' + message.type + '\n'); incoming.push(message) },
  onConnected: () => { process.stderr.write('source: connected\n'); states.push('connected'); void manager.sendMessage('OWNED_MANAGER_HUMAN', { uuid: 'owned-manager-human' }).then(success => { if(!success) throw new Error('Actual Source manager HTTP consumer returned false') }) },
  onPermissionRequest: (request, requestId) => {
    permissions.push({ request, requestId })
    manager.respondToPermissionRequest(requestId, { behavior: 'allow', updatedInput: request.input })
    manager.cancelSession()
  },
  onPermissionCancelled: (requestId, toolUseId) => permissions.push({ cancelled: requestId, toolUseId }),
  onReconnecting: () => states.push('reconnecting'),
  onDisconnected: () => { states.push('disconnected'); finish() },
  onError: error => { throw error },
})
manager.connect()
process.stderr.write("source: connect invoked\n")
await disconnected
manager.disconnect()
if (caps.blocked.length) throw new Error('Source manager escaped private capabilities: ' + JSON.stringify(caps.blocked))
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ gate, incoming, states, permissions, connections, http, caps }), () => resolve()))
process.exit(0)
