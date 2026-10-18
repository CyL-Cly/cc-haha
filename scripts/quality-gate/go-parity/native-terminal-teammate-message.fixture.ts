import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { PassThrough } from 'node:stream'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Original delivered teammate Message renderer')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_TEAMMATE_MESSAGE_SOURCE !== '1') throw new Error('Pinned teammate Message Source required')
const absentPaths = absentManagedSettingsFixturePaths()
for (const path of ['/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token', join(dirname(process.execPath), 'rg')]) if (!fs.existsSync(path)) absentPaths.push(path)
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
const { parseTeammateMessages } = await import('../../../src/components/messages/UserTeammateMessage')

const results = []
for (const row of input.rows) {
  process.env.CC_HAHA_AGENT_TEAMS_ENABLED = row.teamsEnabled === false ? '0' : '1'
  const formatted = formatTeammateMessages(row.messages)
  const originalRecord = row.attachment
    ? { type: 'attachment', uuid: 'owned-teammate-attachment', attachment: { type: 'teammate_mailbox', messages: row.messages } }
    : createUserMessage({ content: formatted, uuid: 'owned-teammate-input' })
  const normalized = normalizeMessages([originalRecord] as any)
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
  const node = (transcript: boolean) => React.createElement(ThemeProvider, { initialState: row.theme ?? 'dark' },
    React.createElement(AppStateProvider, { initialState: appState }, normalized.map((message: any) => React.createElement(Message, {
    key: message.uuid, message, addMargin: false, tools: [], commands: [], lookups: {},
    verbose: false, inProgressToolUseIDs: new Set(), progressMessagesForMessage: [],
    shouldAnimate: false, shouldShowDot: false, isTranscriptMode: transcript, isStatic: false,
  }))))
  const app = await render(node(row.transcript === true),
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
  const inspect = (node: any): any => ({ name: node.nodeName, value: node.nodeValue, style: node.style, box: node.yogaNode ? { x: node.yogaNode.getComputedLeft(), y: node.yogaNode.getComputedTop(), width: node.yogaNode.getComputedWidth(), height: node.yogaNode.getComputedHeight(), basis: node.yogaNode.yoga._flexBasis, main: node.yogaNode.yoga._mainSize, unroundedWidth: node.yogaNode.yoga._lOutW, unroundedHeight: node.yogaNode.yoga._lOutH, constraint: node.yogaNode.yoga._lW, measureWidth: node.yogaNode.yoga._mW, measureOutputWidth: node.yogaNode.yoga._mOutW, measureOutputHeight: node.yogaNode.yoga._mOutH, cacheInput: node.yogaNode.yoga._cIn, cacheOutput: node.yogaNode.yoga._cOut } : undefined, children: node.childNodes?.map(inspect) })
  const layout = input.captureLayout ? inspect(ink.rootNode) : undefined
  const teamsEnabled = isAgentSwarmsEnabled()
  const gateFrames = []
  for (const step of row.gateSteps ?? []) {
    process.env.CC_HAHA_AGENT_TEAMS_ENABLED = step.teamsEnabled ? '1' : '0'
    // render() adds this original outer ThemeProvider. Keep that same root
    // type and all message keys/record identities across the actual rerender.
    app.rerender(React.createElement(ThemeProvider, null, node(step.transcript === true)))
    for (let i = 0; i < 10; i++) await new Promise(resolve => setImmediate(resolve))
    await new Promise(resolve => setTimeout(resolve, 25))
    const screen = ink.frontFrame.screen
    const rows = [], paintRows = []
    for (let y = 0; y < screen.height; y++) {
      let text = '', painted = ''
      for (let x = 0; x < screen.width; x++) {
        const cell = cellAt(screen, x, y)
        text += cell.char
        if (cell.char) painted += ink.stylePool.get(cell.styleId).map((style: any) => style.code).join('') + cell.char + '\x1b[0m'
      }
      rows.push(text.trimEnd())
      paintRows.push(painted)
    }
    gateFrames.push({ teamsEnabled: isAgentSwarmsEnabled(), transcript: step.transcript === true, rows, paintRows })
  }
  app.unmount()
  results.push({ layout, teamsEnabled, gateFrames, formatted, parsed: parseTeammateMessages(formatted), originalRecord, normalized, rows, paintRows, output })
}
if (caps.blocked.length || caps.secureReads.length) throw new Error('Original teammate Message escaped private fixture ' + JSON.stringify(caps))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ results, blocked: caps.blocked, secureReads: caps.secureReads }), error => error ? reject(error) : resolve()))
process.exit(0)
