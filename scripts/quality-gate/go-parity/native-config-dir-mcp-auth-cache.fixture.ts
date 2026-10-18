import fs from 'node:fs'
import * as files from 'node:fs/promises'
import { join } from 'node:path'
import { mock, spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Current MCP authentication-cache and refresh-lock consumers')
const profiles = ['warm', 'held-read', 'queued-mark', 'queued-clear', 'queued-clear-all', 'refresh-lock', 'held-refresh-directory']
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_CONFIG_DIR_MCP_AUTH_SOURCE !== '1' || !profiles.includes(input.profile)) throw new Error('Pinned declared current MCP authentication profile required')
const initial = join(home, '.claude'), next = join(home, 'owned-next'), third = join(home, 'owned-third')
const now = 1791244800000
Date.now = () => now
const pathFor = (directory: string) => join(directory, 'mcp-needs-auth-cache.json')
const seed = (label: string) => ({ [label]: { timestamp: now, future: { keep: label } }, target: { timestamp: now }, future: { label } })
for (const [directory, label] of [[initial, 'old'], [next, 'next'], [third, 'third']]) {
  fs.mkdirSync(directory!, { recursive: true })
  fs.writeFileSync(pathFor(directory!), JSON.stringify(seed(label!)), { mode: 0o600 })
}
const beforeFiles = Object.fromEntries([initial, next, third].map(directory => [directory, fs.readFileSync(pathFor(directory), 'utf8')]))
const caps = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
;(globalThis as any).MACRO = { VERSION: '999.0.0-current-mcp-cache', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setOriginalCwd(home)
bootstrap.setCwdState(home)

// Append observations only. All original declarations and every business body
// remain byte-for-byte unchanged inside their original imported module graph.
Bun.plugin({ name: 'actual-private-mcp-cache-consumers', setup(build) {
  build.onLoad({ filter: /\/src\/services\/mcp\/client\.ts$/ }, args => ({ loader: 'ts', contents: fs.readFileSync(args.path, 'utf8') + '\nexport { getMcpAuthCache, isMcpAuthCached, setMcpAuthCacheEntry }; export function fixtureJoinAuthWrites() { return writeChain }\n' }))
} })
const events: Array<{ operation: string, path: string, directory: string | undefined }> = []
let enteredResolve: (() => void) | undefined, releaseResolve: (() => void) | undefined
const entered = new Promise<void>(resolve => { enteredResolve = resolve })
const release = new Promise<void>(resolve => { releaseResolve = resolve })
let held = false
const readFile = files.readFile, writeFile = files.writeFile, mkdir = files.mkdir, unlink = files.unlink
const observe = (operation: string, path: unknown) => {
  const text = String(path)
  if (text.includes('mcp-needs-auth-cache.json') || text.includes('/mcp-refresh-') || text === initial || text === next || text === third) events.push({ operation, path: text, directory: process.env.CLAUDE_CONFIG_DIR })
}
mock.module('fs/promises', () => ({ ...files,
  readFile: async (path: any, ...args: any[]) => {
    observe('read', path)
    const data = await (readFile as any)(path, ...args)
    if (!held && (input.profile === 'held-read' || input.profile.startsWith('queued-') && input.holdStage === 'read') && String(path) === pathFor(initial)) { held = true; enteredResolve!(); await release }
    return data
  },
  writeFile: async (path: any, ...args: any[]) => {
    observe('write', path)
    const result = await (writeFile as any)(path, ...args)
    if (!held && input.profile.startsWith('queued-') && input.holdStage !== 'read' && String(path) === pathFor(initial)) { held = true; enteredResolve!(); await release }
    return result
  },
  mkdir: async (path: any, ...args: any[]) => {
    observe('mkdir', path)
    const result = await (mkdir as any)(path, ...args)
    if (!held && input.profile === 'held-refresh-directory' && String(path) === next) { held = true; enteredResolve!(); await release }
    return result
  },
  unlink: async (path: any) => { observe('unlink', path); return unlink(path) },
}))
process.env.CLAUDE_CONFIG_DIR = initial
const client: any = await import('../../../src/services/mcp/client')
let outcome: any = null
if (input.profile === 'warm') {
  const before = await client.isMcpAuthCached('old')
  process.env.CLAUDE_CONFIG_DIR = next
  const warm = await client.isMcpAuthCached('old')
  const warmNext = await client.isMcpAuthCached('next')
  await client.getMcpToolsCommandsAndResources(() => { throw new Error('Empty original discovery must not connect') }, {})
  outcome = { before, warm, warmNext, coldNext: await client.isMcpAuthCached('next'), coldOld: await client.isMcpAuthCached('old') }
} else if (input.profile === 'held-read') {
  const old = client.isMcpAuthCached('old')
  await entered
  process.env.CLAUDE_CONFIG_DIR = next
  const shared = client.isMcpAuthCached('next')
  releaseResolve!()
  outcome = { old: await old, shared: await shared }
  await client.getMcpToolsCommandsAndResources(() => { throw new Error('Empty original discovery must not connect') }, {})
  outcome.coldNext = await client.isMcpAuthCached('next')
} else if (input.profile.startsWith('queued-')) {
  client.setMcpAuthCacheEntry('first')
  await entered
  process.env.CLAUDE_CONFIG_DIR = next
  let cleared: Promise<void> | undefined
  if (input.profile === 'queued-mark') client.setMcpAuthCacheEntry('second')
  else cleared = client.clearMcpAuthCache(input.profile === 'queued-clear' ? 'target' : undefined)
  process.env.CLAUDE_CONFIG_DIR = third
  releaseResolve!()
  await (cleared ?? client.fixtureJoinAuthWrites())
  outcome = { afterCurrentThird: await client.isMcpAuthCached('third') }
} else {
  const { plainTextStorage } = await import('../../../src/utils/secureStorage/plainTextStorage')
  const { createFallbackStorage } = await import('../../../src/utils/secureStorage/fallbackStorage')
  const storage = createFallbackStorage({ name: 'owned-unavailable-keychain', read: () => null, readAsync: async () => null, update: () => ({ success: false }), delete: () => false }, plainTextStorage)
  mock.module('../../../src/utils/secureStorage/index.ts', () => ({ getSecureStorage: () => storage }))
  const { ClaudeAuthProvider, getServerKey } = await import('../../../src/services/mcp/auth')
  const config = { type: 'http' as const, url: 'http://127.0.0.1:1/never-request', scope: 'user' as const }
  const name = 'owned-current-lock', key = getServerKey(name, config)
  for (const directory of [initial, next, third]) fs.writeFileSync(join(directory, '.credentials.json'), JSON.stringify({ mcpOAuth: { [key]: { accessToken: 'fake-current-cache-token', refreshToken: 'fake-current-cache-refresh', expiresAt: now + 600000, scope: 'owned-scope' } } }), { mode: 0o600 })
  const locks = await import('../../../src/utils/lockfile')
  const original = locks.lock
  spyOn(locks, 'lock').mockImplementation(async (path, options) => {
    observe('lock', path)
    const unlock = await original(path, options)
    if (!fs.statSync(path + '.lock').isDirectory()) throw new Error('Actual refresh lock was not acquired')
    return async () => { await unlock(); observe('release', path) }
  })
  const provider = new ClaudeAuthProvider(name, config, 'http://localhost:3118/callback')
  process.env.CLAUDE_CONFIG_DIR = next
  const work = provider.refreshAuthorization('fake-current-cache-refresh')
  if (input.profile === 'held-refresh-directory') { await entered; process.env.CLAUDE_CONFIG_DIR = third; releaseResolve!() }
  outcome = await work
}
const afterFiles = Object.fromEntries([initial, next, third].map(directory => [directory, fs.existsSync(pathFor(directory)) ? fs.readFileSync(pathFor(directory), 'utf8') : null]))
if (caps.blocked.length || caps.secureReads.length) throw new Error('Current MCP auth consumers escaped private boundary ' + JSON.stringify(caps))
process.stdout.write(JSON.stringify({ initial, next, third, profile: input.profile, now, beforeFiles, afterFiles, outcome, events, caps, scope: 'unchanged original whole client/auth modules; private cache observers only; actual private filesystem and refresh lease; no provider refresh/HTTP or ordinary Main constructor' }))
process.exit(0)
