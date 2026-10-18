import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual remote force reconnect')
const peer = new URL(input.peer)
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_REMOTE_FORCE_FIXTURE !== '1' || peer.protocol !== 'https:' || peer.hostname !== '127.0.0.1' || !peer.port || peer.origin !== input.peer || process.env.CLAUDE_LOCAL_OAUTH_API_BASE !== input.peer) {
  throw new Error('Owned pinned Source and exact private TLS API required')
}
const absentPaths = absentManagedSettingsFixturePaths()
for (const path of ['/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token']) absentPaths.push(path)
const NativeWebSocket = globalThis.WebSocket
const caps = await installClientFixtureCapabilities(home, { absentPaths, loopbackFetchOrigin: input.peer })
const connections: unknown[] = []
globalThis.WebSocket = class extends NativeWebSocket {
  constructor(url: string, options: any) {
    if (new URL(url).origin.replace('wss:', 'https:') !== input.peer) throw new Error('Unowned Source WS origin')
    connections.push({ url, headers: options.headers })
    super(url, options)
  }
} as typeof WebSocket
;(globalThis as any).MACRO = { VERSION: '999.0.0-remote-force-fixture', BUILD_TIME: '' }
await import('../../../preload')
const secure = await import('../../../src/utils/secureStorage/index')
mock.module('../../../src/utils/secureStorage/index', () => ({ ...secure, getSecureStorage: () => ({ read: () => {
  caps.secureReads.push('owned-empty-remote-force-store')
  return null
} }) }))
const state = await import('../../../src/bootstrap/state')
state.setCwdState(home)
state.setOriginalCwd(home)
state.setProjectRoot(home)
state.setAllowedSettingSources(['userSettings'])
state.setIsInteractive(true)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const { RemoteSessionManager, createRemoteSessionConfig } = await import('../../../src/remote/RemoteSessionManager')
let resolveConnected: () => void = () => {}
const connected = new Promise<void>(resolve => { resolveConnected = resolve })
const errors: string[] = []
const incoming: unknown[] = []
const manager = new RemoteSessionManager(createRemoteSessionConfig('session_owned_force', () => 'owned-force-token', 'owned-org'), {
  onMessage: message => incoming.push(message),
  onPermissionRequest: () => { throw new Error('Unexpected Source permission request') },
  onConnected: resolveConnected,
  // The real useRemoteSession caller logs transport errors without throwing.
  // Closing a connecting native WebSocket may emit that original error event.
  onError: error => { errors.push(error.message) },
})
manager.connect()
// Only the external peer supplies this rendezvous. The actual Source WS
// handshake remains held and all Manager/500ms reconnect business is original.
const stage = await fetch(input.peer + '/fixture/first-upgrade-entered')
if (stage.status !== 200 || (await stage.json()).entered !== true) throw new Error('Actual held handshake did not enter')
manager.reconnect()
await connected
manager.disconnect()
const observed = await fetch(input.peer + '/fixture/observed')
const transport = await observed.json()
if (caps.blocked.length) throw new Error('Source escaped owned capabilities: ' + JSON.stringify(caps.blocked))
process.stdout.write(JSON.stringify({ connections, errors, incoming, transport, caps }) + '\n')
