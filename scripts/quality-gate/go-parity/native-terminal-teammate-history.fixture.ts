import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { PassThrough } from 'node:stream'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Original saved teammate transcript loader and Message renderer')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_TEAMMATE_HISTORY_SOURCE !== '1') throw new Error('Pinned teammate Message Source required')
const absentPaths = absentManagedSettingsFixturePaths()
for (const path of ['/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token', join(dirname(process.execPath), 'rg')]) if (!fs.existsSync(path)) absentPaths.push(path)
for (let parent = home; ; parent = dirname(parent)) {
  const path = join(parent, '.git')
  if (!fs.existsSync(path)) absentPaths.push(path)
  if (dirname(parent) === parent) break
}
const caps = await installClientFixtureCapabilities(home, { absentPaths })
;(globalThis as any).MACRO = { VERSION: '999.0.0-teammate-message-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(home)
bootstrap.setOriginalCwd(home)
bootstrap.setProjectRoot(home)
bootstrap.setAllowedSettingSources(['userSettings'])
bootstrap.setIsInteractive(true)
await Bun.write(join(home, '.claude', 'settings.json'), JSON.stringify({ syntaxHighlightingDisabled: true }))
const config = await import('../../../src/utils/config')
config.enableConfigs()
const React = await import('react')
const { render } = await import('../../../src/ink')
const instances = (await import('../../../src/ink/instances')).default
const { cellAt } = await import('../../../src/ink/screen')
const { AppStateProvider, getDefaultAppState } = await import('../../../src/state/AppState')
const { ThemeProvider } = await import('../../../src/components/design-system/ThemeProvider')
const { Message } = await import('../../../src/components/Message')
const { isAgentSwarmsEnabled } = await import('../../../src/utils/agentSwarmsEnabled')
const { createUserMessage, normalizeMessages } = await import('../../../src/utils/messages')
const { formatTeammateMessages } = await import('../../../src/utils/teammateMailbox')
const storage = await import('../../../src/utils/sessionStorage')
const { parseTeammateMessages } = await import('../../../src/components/messages/UserTeammateMessage')

const results = []
for (const [index, row] of input.rows.entries()) {
  process.env.CC_HAHA_AGENT_TEAMS_ENABLED = row.teamsEnabled === false ? '0' : '1'
  const formatted = formatTeammateMessages(row.messages)
  const originalRecord = row.attachment
    ? { type: 'attachment', uuid: 'owned-teammate-attachment', attachment: { type: 'teammate_mailbox', messages: row.messages } }
    : createUserMessage({ content: formatted, uuid: 'owned-teammate-input' })
  const sid = `00000000-0000-4000-8000-${String(index + 1).padStart(12, '0')}`
  bootstrap.switchSession(sid, null)
  const path = storage.getTranscriptPathForSession(sid)
  const record = { ...originalRecord, sessionId: sid, cwd: home, parentUuid: null, isSidechain: false, timestamp: '2026-10-01T00:00:00.000Z' }
  const physicalBefore = JSON.stringify(record) + '\n'
  fs.mkdirSync(dirname(path), { recursive: true, mode: 0o700 })
  fs.writeFileSync(path, physicalBefore, { mode: 0o600 })
  const loaded = await storage.loadFullLog({ messages: [], sessionId: sid, fullPath: path } as any)
  if (loaded.messages.length !== 1) throw new Error('Original saved transcript did not return its actual user message: ' + JSON.stringify(loaded))
  const normalized = normalizeMessages(loaded.messages)
  const physicalAfter = fs.readFileSync(path, 'utf8')
  if (physicalAfter !== physicalBefore) throw new Error('Original read-only transcript load mutated private saved records')
  const stdin = new PassThrough() as any
  stdin.isTTY = true
  stdin.setRawMode = () => {}
  stdin.ref = () => stdin
  stdin.unref = () => stdin
  const stdout = new PassThrough() as any
  stdout.isTTY = false
  stdout.columns = row.width
  stdout.rows = row.height ?? 36
  let output = ''
  stdout.on('data', chunk => output += chunk.toString())
  const appState = { ...getDefaultAppState(), showTeammateMessagePreview: row.preview === true, expandedView: row.expanded ?? 'none' }
  const children = normalized.map((message: any) => React.createElement(Message, {
    key: message.uuid, message, addMargin: false, tools: [], commands: [], lookups: {},
    verbose: false, inProgressToolUseIDs: new Set(), progressMessagesForMessage: [],
    shouldAnimate: false, shouldShowDot: false, isTranscriptMode: row.transcript === true, isStatic: false,
  }))
  const app = await render(React.createElement(ThemeProvider, { initialState: row.theme ?? 'dark' },
    React.createElement(AppStateProvider, { initialState: appState }, children)),
  { stdin, stdout, stderr: stdout, patchConsole: false, exitOnCtrlC: false })
  for (let i = 0; i < 10; i++) await new Promise(resolve => setImmediate(resolve))
  await new Promise(resolve => setTimeout(resolve, 25))
  const ink = instances.get(stdout) as any
  const screen = ink.frontFrame.screen
  const rows = [], paintRows = []
  for (let y = 0; y < screen.height; y++) {
    let text = '', painted = ''
    for (let x = 0; x < screen.width; x++) {
      const cell = cellAt(screen, x, y)
      const value = cell.char
      text += value
      if (value) painted += ink.stylePool.get(cell.styleId).map((style: any) => style.code).join('') + value + '\x1b[0m'
    }
    rows.push(text.trimEnd())
    paintRows.push(painted)
  }
  app.unmount()
  results.push({ teamsEnabled: isAgentSwarmsEnabled(), formatted, parsed: parseTeammateMessages(formatted), originalRecord: record, loadedRecords: loaded.messages, normalized, rows, paintRows, output, path, physicalBefore, physicalAfter })
}
if (caps.blocked.length || caps.secureReads.length) throw new Error('Original teammate Message escaped private fixture ' + JSON.stringify(caps))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ results, blocked: caps.blocked, secureReads: caps.secureReads }), error => error ? reject(error) : resolve()))
process.exit(0)
