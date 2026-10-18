import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual QueryEngine initial flush')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_INITIAL_FLUSH_FIXTURE !== '1') throw new Error('Pinned owned initial flush profile required')
const cwd = join(home, 'project')
const absent = absentManagedSettingsFixturePaths()
for (const spelling of new Set([home, fs.realpathSync(home)])) for (let path = dirname(spelling); ; path = dirname(path)) {
  for (const name of ['.git', '.claude/skills', '.claude/workflows', '.claude/commands', '.claude/rules', '.claude/output-styles', '.agents/skills', 'CLAUDE.md', '.claude/CLAUDE.md', 'CLAUDE.local.md', 'AGENTS.md', '.claude/AGENTS.md']) {
    const candidate = join(path, name)
    if (!fs.existsSync(candidate)) absent.push(candidate)
  }
  if (dirname(path) === path) break
}
for (const candidate of ['/Library/Application Support/ClaudeCode/CLAUDE.md', '/Library/Application Support/ClaudeCode/.claude/rules', '/Library/Application Support/ClaudeCode/.claude/skills', '/home/claude/.claude/remote/.oauth_token', '/home/claude/.claude/remote/.api_key']) if (!fs.existsSync(candidate)) absent.push(candidate)
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent, loopbackFetchOrigin: input.peer })
;(globalThis as any).MACRO = { VERSION: '999.0.0-initial-flush', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(cwd)
bootstrap.setOriginalCwd(cwd)
bootstrap.setProjectRoot(cwd)
bootstrap.setIsInteractive(false)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const { getDefaultAppState } = await import('../../../src/state/AppStateStore')
const { initializeToolPermissionContext } = await import('../../../src/utils/permissions/permissionSetup')
const permissions = await initializeToolPermissionContext({ allowedToolsCli: [], disallowedToolsCli: [], baseToolsCli: [''], permissionMode: 'default', allowDangerouslySkipPermissions: false, addDirs: [] })
let state = { ...getDefaultAppState(), toolPermissionContext: permissions.toolPermissionContext }
const { QueryEngine } = await import('../../../src/QueryEngine')
const storage = await import('../../../src/utils/sessionStorage')
const { createSessionMessageInbox } = await import('../../../src/utils/sessionMessageInbox')
const engine = new QueryEngine({ initialMessages: [], cwd, tools: [], commands: [], mcpClients: [], agents: [], readFileCache: new Map() as any,
  customSystemPrompt: 'private actual initial flush', userSpecifiedModel: 'fixture-model', thinkingConfig: { type: 'disabled' }, maxTurns: 2,
  canUseTool: async () => ({ behavior: 'allow', updatedInput: {} }), getAppState: () => state, setAppState: update => { state = update(state) },
} as any)
let prompt = 'ACTUAL_INITIAL_FLUSH_ACCEPTED_USER', submitOptions: any
const receipts: any[] = []
const inbox = createSessionMessageInbox(command => { prompt = String(command.value); submitOptions = { uuid: command.uuid, isMeta: command.isMeta } }, receipt => receipts.push(receipt))
if (input.sessionInput) inbox.accept({ subtype: 'enqueue_session_message', message_id: 'initial-flush-delivery', sender_session_id: 'private-peer', text: prompt })

// Pure observation returns the exact original export Promise. No async spy,
// replacement writer, transformed product module, or synthetic query is used.
const originalFlush = storage.flushSessionStorage
let flushes = 0
const exportPromisesSame: boolean[] = []
const flushSpy = spyOn(storage, 'flushSessionStorage').mockImplementation(() => {
  const promise = originalFlush()
  flushes++
  exportPromisesSame.push(Promise.resolve(promise) === promise)
  return promise
})
const originalFetch = globalThis.fetch
let posts = 0, flushesBeforeFirstPost = -1, persistedBeforeFirstPost = false
const canonicalPath = storage.getTranscriptPath()
globalThis.fetch = ((request: any, init?: RequestInit) => {
  const url = new URL(request instanceof Request ? request.url : String(request))
  const method = init?.method ?? (request instanceof Request ? request.method : 'GET')
  if (url.pathname === '/v1/messages' && method.toUpperCase() === 'POST') {
    posts++
    if (posts === 1) {
      flushesBeforeFirstPost = flushes
      persistedBeforeFirstPost = fs.existsSync(canonicalPath) && fs.readFileSync(canonicalPath, 'utf8').includes('ACTUAL_INITIAL_FLUSH_ACCEPTED_USER')
    }
  }
  return originalFetch(request, init)
}) as typeof fetch
const results: any[] = []
try {
  for await (const value of engine.submitMessage(prompt, submitOptions)) if (value.type === 'result') results.push(value)
} finally {
  globalThis.fetch = originalFetch
  flushSpy.mockRestore()
  inbox.dispose()
}
await originalFlush()
const persisted = fs.readFileSync(canonicalPath, 'utf8').includes('ACTUAL_INITIAL_FLUSH_ACCEPTED_USER')
if (caps.blocked.length) throw new Error('Initial flush escaped private fixture: ' + JSON.stringify(caps.blocked))
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ posts, flushesBeforeFirstPost, persistedBeforeFirstPost, persisted, exportPromisesSame, receipts, results, blocked: caps.blocked, scope: 'full original QueryEngine initial persistence block; not cold priority or REPL authority' }), () => resolve()))
process.exit(0)
