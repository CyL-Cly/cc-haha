import fs from 'node:fs'
import * as fsp from 'node:fs/promises'
import { dirname, join } from 'node:path'
import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual nonbare initial transcript')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_INITIAL_TRANSCRIPT_FIXTURE !== '1' || process.env.CLAUDE_CODE_SIMPLE) throw new Error('Owned nonbare transcript profile required')
const cwd = join(home, 'project')
const absent = absentManagedSettingsFixturePaths()
for (const spelling of new Set([home, fs.realpathSync(home)])) for (let path = dirname(spelling); ; path = dirname(path)) {
  for (const name of ['.git', '.claude/skills', '.claude/workflows', '.claude/commands', '.claude/rules', '.agents/skills', 'CLAUDE.md', '.claude/CLAUDE.md', 'CLAUDE.local.md', 'AGENTS.md', '.claude/AGENTS.md']) {
    const candidate = join(path, name)
    if (!fs.existsSync(candidate)) absent.push(candidate)
  }
  if (dirname(path) === path) break
}
for (const candidate of ['/Library/Application Support/ClaudeCode/CLAUDE.md', '/Library/Application Support/ClaudeCode/.claude/rules', '/Library/Application Support/ClaudeCode/.claude/skills', '/home/claude/.claude/remote/.oauth_token', '/home/claude/.claude/remote/.api_key']) if (!fs.existsSync(candidate)) absent.push(candidate)
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent, loopbackFetchOrigin: input.peer })
;(globalThis as any).MACRO = { VERSION: '999.0.0-initial-transcript', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(cwd); bootstrap.setOriginalCwd(cwd); bootstrap.setProjectRoot(cwd); bootstrap.setIsInteractive(false)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const { getDefaultAppState } = await import('../../../src/state/AppStateStore')
const { initializeToolPermissionContext } = await import('../../../src/utils/permissions/permissionSetup')
const permissions = await initializeToolPermissionContext({ allowedToolsCli: [], disallowedToolsCli: [], baseToolsCli: [''], permissionMode: 'default', allowDangerouslySkipPermissions: false, addDirs: [] })
let state = { ...getDefaultAppState(), toolPermissionContext: permissions.toolPermissionContext }
const { QueryEngine } = await import('../../../src/QueryEngine')
const storage = await import('../../../src/utils/sessionStorage')
const createEngine = (initialMessages: any[] = []) => new QueryEngine({ initialMessages, cwd, tools: [], commands: [], mcpClients: [], agents: [], readFileCache: new Map() as any,
  customSystemPrompt: 'private actual initial transcript', userSpecifiedModel: 'fixture-model', thinkingConfig: { type: 'disabled' }, maxTurns: 2,
  canUseTool: async () => ({ behavior: 'allow', updatedInput: {} }), getAppState: () => state, setAppState: update => { state = update(state) },
} as any)
let engine = createEngine()
const results: any[] = []
async function turn(text: string) {
  try { for await (const value of engine.submitMessage(text)) if (value.type === 'result') results.push(value) }
  catch (error: any) { return error.message }
  return ''
}
const firstError = await turn('INITIAL_RECORD_FIRST')
await storage.flushSessionStorage()
const path = storage.getTranscriptPath()
const swapped = await fetch(input.peer + '/__fixture/swap-fifo?path=' + encodeURIComponent(path))
if (!swapped.ok) throw new Error('Private canonical FIFO was not created')
const open = fsp.open as any
const originalOpen = open.getMockImplementation?.() ?? open
let opened!: () => void
const openStarted = new Promise<void>(resolve => { opened = resolve })
spyOn(fsp, 'open').mockImplementation(((name: any, ...args: any[]) => {
  const result = originalOpen(name, ...args)
  if (String(name) === path) opened()
  return result
}) as any)
const second = turn('INITIAL_RECORD_HELD_REJECTED')
await openStarted
if (input.cancelled) engine.interrupt()
const held = { pending: Bun.peek.status(second) === 'pending', fifo: fs.statSync(path).isFIFO(), cancelled: Boolean(input.cancelled) }
const reader = fs.openSync(path, fs.constants.O_RDONLY | fs.constants.O_NONBLOCK)
const secondError = await second
fs.closeSync(reader)
fs.unlinkSync(path)
if (input.cancelled) engine = createEngine([...engine.getMessages()])
const thirdError = await turn('INITIAL_RECORD_NEXT_HUMAN')
await storage.flushSessionStorage()
const canonical = fs.readFileSync(path, 'utf8').trim().split('\n').map(line => JSON.parse(line))
storage.clearSessionMessagesCache()
const restored = await (await import('../../../src/utils/conversationRecovery')).loadConversationForResume(bootstrap.getSessionId(), undefined)
if (!restored) throw new Error('Actual source physical recovery lost next human')
if (caps.blocked.length) throw new Error('Initial record escaped private fixture: ' + JSON.stringify(caps.blocked))
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ firstError, held, secondError, thirdError, results, canonical, restoredMessages: restored.messages, sessionId: bootstrap.getSessionId(), caps: { blocked: caps.blocked } }), () => resolve()))
process.exit(0)
