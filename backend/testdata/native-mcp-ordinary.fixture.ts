import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { resolve } from 'node:path'
const home = assertDisposableFixtureHome(process.env.HOME, 'ordinary native MCP Source')
if (process.env.CC_HAHA_MCP_ORDINARY_SOURCE_FIXTURE !== '1') throw new Error('Unowned ordinary MCP Source')
if (process.env.CLAUDE_CODE_SIMPLE) throw new Error('Ordinary MCP fixture must not use simple/bare')
const cwd = resolve(process.env.CC_HAHA_MCP_ORDINARY_CWD ?? home)
if (cwd !== home && !cwd.startsWith(home + '/')) throw new Error('Unowned ordinary MCP cwd')
const peer = new URL(process.env.ANTHROPIC_BASE_URL ?? '')
if (!['127.0.0.1', 'localhost', '[::1]'].includes(peer.hostname)) throw new Error('Ordinary MCP Source requires a loopback model transport')
const originalFetch = globalThis.fetch
const guardedFetch = ((input: RequestInfo | URL, options?: RequestInit) => {
  const target = new URL(input instanceof Request ? input.url : String(input))
  if (!['127.0.0.1', 'localhost', '[::1]'].includes(target.hostname)) throw new Error('Unowned ordinary MCP Source outbound fetch: ' + target.hostname)
  return originalFetch(input, options)
}) as typeof fetch
Object.assign(guardedFetch, originalFetch)
globalThis.fetch = guardedFetch
// Only external policy/credential stores are absent. Tool calls, filesystem
// effects inside HOME, shell processes and real HTTP transport stay original.
const { mock, spyOn } = await import('bun:test')
const fs = await import('node:fs')
const mutableFS = require('node:fs')
const managed = '/Library/Application Support/ClaudeCode'
for (const name of ['readFileSync', 'readdirSync', 'statSync', 'lstatSync', 'existsSync']) {
  const original = mutableFS[name]
  const wrapper = (...args: any[]) => {
    if (typeof args[0] === 'string' && (args[0] === managed || args[0].startsWith(managed + '/'))) {
      if (name === 'existsSync') return false
      throw Object.assign(new Error('fixture absent managed policy'), { code: 'ENOENT' })
    }
    return original(...args)
  }
  mutableFS[name] = wrapper
  spyOn(fs as any, name).mockImplementation(wrapper)
}
for (const name of ['readFile', 'readdir', 'stat', 'lstat', 'access']) {
  const original = mutableFS.promises[name]
  mutableFS.promises[name] = async (...args: any[]) => {
    if (typeof args[0] === 'string' && (args[0] === managed || args[0].startsWith(managed + '/'))) throw Object.assign(new Error('fixture absent managed policy'), { code: 'ENOENT' })
    return original(...args)
  }
}
let fixtureSecureReads = 0
process.on('exit', () => { if (process.env.CC_HAHA_MCP_SECURE_READ_RECEIPT === '1') process.stderr.write('MCP_SECURE_READ_RECEIPT:'+JSON.stringify({reads:fixtureSecureReads})+'\n') })
const fixtureSecure = process.env.CC_HAHA_MCP_FAKE_SECURE_JSON ? JSON.parse(process.env.CC_HAHA_MCP_FAKE_SECURE_JSON) : null
if (fixtureSecure && fixtureSecure.claudeAiOauth?.accessToken !== 'fixture-subscriber') throw new Error('Only the owned fake subscriber identity is admitted')
mock.module(new URL('../../src/utils/secureStorage/index.ts', import.meta.url).pathname, () => ({ getSecureStorage: () => ({ name: 'ordinary-MCP-empty-credential-store', read: () => { fixtureSecureReads++; return fixtureSecure }, readAsync: async () => { fixtureSecureReads++; return fixtureSecure }, update: () => { throw new Error('Unowned secure update') }, delete: () => { throw new Error('Unowned secure delete') } }) }))
const emptyPolicy = { settings: {}, errors: [] }
mock.module(new URL('../../src/utils/settings/mdm/settings.ts', import.meta.url).pathname, () => ({ startMdmSettingsLoad() {}, ensureMdmSettingsLoaded: async () => {}, getMdmSettings: () => emptyPolicy, getHkcuSettings: () => emptyPolicy, clearMdmSettingsCache() {}, setMdmSettingsCache() {}, refreshMdmSettings: async () => ({ mdm: emptyPolicy, hkcu: emptyPolicy }) }))
// src/main.tsx initializeEntrypoint sets this before the real MCP action.
process.env.CLAUDE_CODE_ENTRYPOINT ??= 'mcp'
await import('../../preload')
const state = await import('../../src/bootstrap/state')
state.setOriginalCwd(cwd)
state.setCwdState(cwd)
state.setProjectRoot(cwd)
process.chdir(cwd)
const { enableConfigs } = await import('../../src/utils/config')
enableConfigs()
const { startMCPServer } = await import('../../src/entrypoints/mcp')
await startMCPServer(cwd, false, false)
