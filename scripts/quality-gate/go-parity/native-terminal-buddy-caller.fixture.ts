import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { PassThrough } from 'node:stream'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Original Buddy full slash completion and Message')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_BUDDY_CALLER_SOURCE !== '1') throw new Error('Pinned Buddy full caller required')
process.env.USER_TYPE = 'external'
process.env.ANTHROPIC_API_KEY = 'buddy-caller-private-api-key'
process.env.CLAUDE_CODE_TMPDIR = home
fs.mkdirSync(join(home, '.claude'), { recursive: true })
fs.writeFileSync(join(home, '.claude', 'settings.json'), '{}')
fs.writeFileSync(join(home, '.claude', '.claude.json'), JSON.stringify({ companion: { name: 'Public Buddy', personality: 'Existing soul', hatchedAt: 0, appearanceSeed: 'public-buddy-seed', futureSoul: { keep: true } }, futureBuddy: { keep: true }, customApiKeyResponses: { approved: [process.env.ANTHROPIC_API_KEY.slice(-20)], rejected: [] } }))
const absentPaths = absentManagedSettingsFixturePaths()
for (const path of ['/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token']) if (!fs.existsSync(path)) absentPaths.push(path)
for (let directory = dirname(home);; directory = dirname(directory)) {
  const probe = join(directory, '.git')
  if (!fs.existsSync(probe)) absentPaths.push(probe)
  if (dirname(directory) === directory) break
}
const caps = await installClientFixtureCapabilities(home, { absentPaths })
;(globalThis as any).MACRO = { VERSION: '999.0.0-buddy-caller', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package', VERSION_CHANGELOG: '', FEEDBACK_CHANNEL: 'local' }
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
state.setCwdState(home); state.setOriginalCwd(home); state.setProjectRoot(home); state.setAllowedSettingSources(['userSettings']); state.setIsInteractive(true); state.setMainLoopModelOverride('claude-sonnet-4-6')
const config = await import('../../../src/utils/config')
config.enableConfigs()
const React = await import('react')
const { render } = await import('../../../src/ink')
const instances = (await import('../../../src/ink/instances')).default
const { charInCellAt } = await import('../../../src/ink/screen')
const { getDefaultAppState, AppStateProvider } = await import('../../../src/state/AppState')
const { createFileStateCacheWithSizeLimit } = await import('../../../src/utils/fileStateCache')
const { processSlashCommand } = await import('../../../src/utils/processUserInput/processSlashCommand')
const { normalizeMessages, normalizeMessagesForAPI } = await import('../../../src/utils/messages')
const { Message } = await import('../../../src/components/Message')
const buddy = (await import('../../../src/commands/buddy/index')).default
const settle = async () => { for (let index = 0; index < 12; index++) await new Promise(resolve => setImmediate(resolve)); await new Promise(resolve => setTimeout(resolve, 25)) }
function streams() {
  const stdin = new PassThrough() as any
  stdin.isTTY = true; stdin.setRawMode = () => {}; stdin.ref = () => stdin; stdin.unref = () => stdin
  const stdout = new PassThrough() as any
  stdout.isTTY = false; stdout.columns = input.width ?? 120; stdout.rows = 42; stdout.on('data', () => {})
  return { stdin, stdout, stderr: stdout, patchConsole: false, exitOnCtrlC: false }
}
const results = []
async function messageFrame(messages: any[], appState: any) {
  const options = streams()
  const rows = messages.map((message: any) => React.createElement(Message, { key: message.uuid, message, tools: [], commands: [buddy], lookups: {}, inProgressToolUseIDs: new Set(), progressMessagesForMessage: [], addMargin: false, verbose: false }))
  const mounted = await render(React.createElement(AppStateProvider, { initialState: appState }, React.createElement(React.Fragment, null, ...rows)), options)
  await settle()
  const screen = (instances.get(options.stdout) as any).frontFrame.screen
  const lines = []
  for (let y = 0; y < screen.height; y++) { let text = ''; for (let x = 0; x < screen.width; x++) text += charInCellAt(screen, x, y) ?? ' '; lines.push(text.trimEnd()) }
  mounted.unmount()
  return lines.join('\n')
}
for (const args of input.args) {
  let appState = getDefaultAppState()
  const context: any = { readFileState: createFileStateCacheWithSizeLimit(), discoveredSkillNames: new Set(), loadedNestedMemoryPaths: new Set(), getAppState: () => appState, setAppState: (update: any) => { appState = update(appState) }, abortController: new AbortController(), messages: [], options: { commands: [buddy], tools: [], agentDefinitions: { activeAgents: [] }, isNonInteractiveSession: false } }
  let mountedJSX: any
  const nodeReady = new Promise<void>(resolve => {
    const pending = processSlashCommand('/buddy ' + args, [], [], [], context, value => { if (value?.jsx != null) { mountedJSX = value.jsx; resolve() } })
    context.actualCompletion = pending
  })
  await nodeReady
  const effectOptions = streams()
  const effect = await render(React.createElement(AppStateProvider, { initialState: appState }, mountedJSX), effectOptions)
  const caller = await context.actualCompletion
  effect.unmount()
  const normalized = normalizeMessages(caller.messages)
  const frame = await messageFrame(normalized, appState)
  const messageFrames = []
  for (const message of normalized) messageFrames.push(await messageFrame([message], appState))
  results.push({ args, messages: caller.messages, normalized, normalizedAPI: normalizeMessagesForAPI(caller.messages, [], 'claude-sonnet-4-6'), frame, messageFrames, global: config.getGlobalConfig() })
}
if (caps.blocked.length || caps.secureReads.length) throw new Error('Original Buddy full caller escaped private fixture ' + JSON.stringify(caps))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ results, caps }), error => error ? reject(error) : resolve()))
process.exit(0)
