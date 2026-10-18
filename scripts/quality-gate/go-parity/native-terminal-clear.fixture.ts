import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { PassThrough } from 'node:stream'
import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await new Response(Bun.stdin.stream()).json()
const home = assertDisposableFixtureHome(input.home, 'Actual clear local-command caller')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_CLEAR_FIXTURE !== '1') throw new Error('Pinned owned clear fixture required')
const absentPaths = absentManagedSettingsFixturePaths()
for (const path of ['/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token']) if (!fs.existsSync(path)) absentPaths.push(path)
for (let parent = dirname(home); ; parent = dirname(parent)) {
  const candidate = join(parent, '.git')
  if (!fs.existsSync(candidate)) absentPaths.push(candidate)
  if (dirname(parent) === parent) break
}
const caps = await installClientFixtureCapabilities(home, { absentPaths })
;(globalThis as any).MACRO = { VERSION: '999.0.0-clear-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const secure = await import('../../../src/utils/secureStorage/index')
mock.module('../../../src/utils/secureStorage/index', () => ({ ...secure, getSecureStorage: () => ({ read: () => { caps.secureReads.push('owned-empty-credentials'); return null } }) }))
const portable = await import('../../../src/utils/execFileNoThrow')
mock.module('../../../src/utils/execFileNoThrow', () => ({ ...portable, execSyncWithDefaults_DEPRECATED(command: string) {
  if (!command.startsWith('security find-generic-password ')) throw new Error('Unexpected Source OS primitive')
  caps.secureReads.push('owned-empty-legacy'); return null
} }))
const state = await import('../../../src/bootstrap/state')
state.setCwdState(home); state.setOriginalCwd(home); state.setProjectRoot(home); state.setAllowedSettingSources(['userSettings']); state.setIsInteractive(true)
state.switchSession(input.sid)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const storage = await import('../../../src/utils/sessionStorage')
const { createFileStateCacheWithSizeLimit } = await import('../../../src/utils/fileStateCache')
const { getDefaultAppState } = await import('../../../src/state/AppState')
const clear = (await import('../../../src/commands/clear/index')).default
const original = await clear.load()
let app = getDefaultAppState()
const aborted: string[] = []
app.tasks = Object.fromEntries(input.tasks.map((row: any) => {
  const controller = new AbortController()
  controller.signal.addEventListener('abort', () => aborted.push(row.id))
  return [row.id, { ...row, abortController: controller }]
}))
app.standaloneAgentContext = { name: 'OLD_SOURCE_NAME', color: 'red' }
app.fileHistory = { snapshots: [{ messageId: 'old' }], trackedFiles: new Set(['old']), snapshotSequence: 7 }
app.mcp.pluginReconnectKey = 19
const readFileState = createFileStateCacheWithSizeLimit(100)
readFileState.set(join(home, 'old-read'), { content: 'old', timestamp: 1, offset: undefined, limit: undefined })
const discoveredSkillNames = new Set(['old-skill']), loadedNestedMemoryPaths = new Set(['old-memory'])
for (const agent of [null, 'kept-agent', 'dropped-agent']) state.addInvokedSkill('owned-skill', join(home, 'skill'), 'owned full skill text', agent)
state.setLastEmittedDate('1900-01-01')
storage.restoreSessionMetadata({ customTitle: 'OLD_SOURCE_TITLE', agentName: 'OLD_SOURCE_NAME', agentColor: 'red', tag: 'OLD_TAG' })
const oldFile = join(home, 'old-transcript.jsonl')
fs.writeFileSync(oldFile, input.transcript)
storage.setSessionFileForTesting(oldFile)
let messages = [{ type: 'user', uuid: 'old-message', message: { role: 'user', content: 'OLD_SOURCE_USER' } }]
let conversationId: string | undefined
const context: any = {
  setMessages: (update: any) => { messages = update(messages) }, readFileState, discoveredSkillNames, loadedNestedMemoryPaths,
  getAppState: () => app, setAppState: (update: any) => { app = update(app) }, setConversationId: (value: string) => { conversationId = value },
  abortController: new AbortController(), messages, options: { commands: [clear], tools: [], agentDefinitions: { activeAgents: [] } },
}
let result: any
let caller: any
if (input.fullCaller) {
  const { processSlashCommand } = await import('../../../src/utils/processUserInput/processSlashCommand')
  caller = await processSlashCommand('/' + (input.alias ?? 'clear') + (input.args ? ' ' + input.args : ''), [], [], [], context, () => {})
  // REPL onQuery appends this original producer's messages to the fresh hook
  // history. Retain the producer result and actual API normalization separately.
  const { normalizeMessages, normalizeMessagesForAPI } = await import('../../../src/utils/messages')
  caller.normalized = normalizeMessages([...messages, ...caller.messages])
  caller.normalizedAPI = normalizeMessagesForAPI([...messages, ...caller.messages], [], 'claude-sonnet-4-5')
  result = { type: 'text', value: caller.resultText }
} else result = await original.call('', context)
// Await actual fire-and-forget module cache clear and task-output jobs.
await new Promise(resolve => setTimeout(resolve, 0))
let renderedCaller: { text: string, width: number } | undefined
if (input.renderCaller) {
  if (!input.fullCaller) throw new Error('Mounted Message proof requires the original full slash caller')
  const React = await import('react')
  const { render } = await import('../../../src/ink')
  const instances = (await import('../../../src/ink/instances')).default
  const { charInCellAt } = await import('../../../src/ink/screen')
  const { AppStateProvider } = await import('../../../src/state/AppState')
  const { Message } = await import('../../../src/components/Message')
  const stdin = new PassThrough() as any
  stdin.isTTY = true; stdin.setRawMode = () => {}; stdin.ref = () => stdin; stdin.unref = () => stdin
  const stdout = new PassThrough() as any
  stdout.isTTY = false; stdout.columns = input.width ?? 132
  stdout.on('data', () => {})
  const rows = caller.normalized.map((message: any) => React.createElement(Message, { key: message.uuid, message, tools: [], commands: [clear], lookups: {}, inProgressToolUseIDs: new Set(), progressMessagesForMessage: [], addMargin: false, verbose: false }))
  const node = React.createElement(AppStateProvider, { initialState: app }, React.createElement(React.Fragment, null, ...rows))
  const mounted = await render(node, { stdin, stdout, stderr: stdout, patchConsole: false, exitOnCtrlC: false })
  for (let i = 0; i < 10; i++) await new Promise(resolve => setImmediate(resolve))
  await new Promise(resolve => setTimeout(resolve, 25))
  const screen = (instances.get(stdout) as any).frontFrame.screen
  const lines = []
  for (let y = 0; y < screen.height; y++) {
    let line = ''
    for (let x = 0; x < screen.width; x++) line += charInCellAt(screen, x, y) ?? ' '
    lines.push(line.trimEnd())
  }
  renderedCaller = { text: lines.join('\n'), width: stdout.columns }
  mounted.unmount()
}
if (caps.blocked.length) throw new Error('Source clear escaped private boundary: ' + JSON.stringify(caps.blocked))
const output = {
  registration: { name: clear.name, kind: clear.type, aliases: clear.aliases, nonInteractive: clear.supportsNonInteractive },
  result, caller, renderedCaller, sid: state.getSessionId(), parent: state.getParentSessionId(), conversationId, messages,
  tasks: Object.keys(app.tasks), aborted, fileHistory: { snapshots: app.fileHistory.snapshots.length, tracked: app.fileHistory.trackedFiles.size, sequence: app.fileHistory.snapshotSequence },
  mcp: { clients: app.mcp.clients.length, tools: app.mcp.tools.length, commands: app.mcp.commands.length, resources: Object.keys(app.mcp.resources).length, key: app.mcp.pluginReconnectKey },
  standalone: app.standaloneAgentContext, title: storage.getCurrentSessionTitle(state.getSessionId()), color: storage.getCurrentSessionAgentColor(),
  readFiles: readFileState.size, discovered: discoveredSkillNames.size, nested: loadedNestedMemoryPaths.size,
  skills: [...state.getInvokedSkills().keys()], lastDate: state.getLastEmittedDate(), oldTranscript: fs.readFileSync(oldFile, 'utf8'),
  caps: { blocked: caps.blocked, secureReads: caps.secureReads },
}
await new Promise<void>(resolve => process.stdout.write(JSON.stringify(output), () => resolve()))
process.exit(0)
