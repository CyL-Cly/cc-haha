import fs from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Whole original keybindings slash caller and private filesystem')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_KEYBINDINGS_SLASH_SOURCE !== '1') throw new Error('Pinned full keybindings Source required')
const absent = absentManagedSettingsFixturePaths()
for (const path of ['/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token', join(dirname(process.execPath), 'rg')]) if (!fs.existsSync(path)) absent.push(path)
for (let parent = home; ; parent = dirname(parent)) {
  const path = join(parent, '.git')
  if (!fs.existsSync(path)) absent.push(path)
  if (dirname(parent) === parent) break
}
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent })
;(globalThis as any).MACRO = { VERSION: '999.0.0-keybindings-slash-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const portable = await import('../../../src/utils/execFileNoThrow')
mock.module('../../../src/utils/execFileNoThrow', () => ({ ...portable, execSyncWithDefaults_DEPRECATED(command: string) {
  if (!command.startsWith('security find-generic-password ')) throw new Error('Unexpected OS primitive')
  caps.secureReads.push('owned-empty-legacy')
  return null
} }))
const growthbook = await import('../../../src/services/analytics/growthbook')
mock.module('../../../src/services/analytics/growthbook', () => ({ ...growthbook, getFeatureValue_CACHED_MAY_BE_STALE(name: string, fallback: unknown) {
  return name === 'tengu_keybinding_customization_release' ? true : fallback
} }))
const editors: unknown[] = []
const promptEditor = await import('../../../src/utils/promptEditor')
mock.module('../../../src/utils/promptEditor', () => ({ ...promptEditor, editFileInEditor(path: string) {
  editors.push({ path, before: fs.readFileSync(path, 'utf8'), directory: process.cwd() })
  if (input.editorThrow) throw new Error(input.editorThrow)
  return { content: null }
} }))
const bootstrap = await import('../../../src/bootstrap/state')
const config = await import('../../../src/utils/config')
bootstrap.setAllowedSettingSources(['userSettings'])
bootstrap.setIsInteractive(true)
config.enableConfigs()
const { getDefaultAppState } = await import('../../../src/state/AppState')
const { processSlashCommand } = await import('../../../src/utils/processUserInput/processSlashCommand')
const declaration = (await import('../../../src/commands/keybindings/index')).default
const logging = await import('../../../src/utils/log')
const logs: string[] = []
logging.attachErrorLogSink({ logError: error => { logs.push(String(error)) }, logMCPError() {}, logMCPDebug() {}, getErrorsPath: () => join(home, 'errors'), getMCPLogsPath: () => join(home, 'mcp') })
const work = join(home, 'owned-work')
fs.mkdirSync(work, { recursive: true })
process.chdir(work)
bootstrap.setCwdState(work)
bootstrap.setOriginalCwd(work)
bootstrap.setProjectRoot(work)
if (input.config !== undefined) process.env.CLAUDE_CONFIG_DIR = input.config
else delete process.env.CLAUDE_CONFIG_DIR
if (input.blockedAncestor) {
  fs.mkdirSync(dirname(input.blockedAncestor), { recursive: true })
  fs.writeFileSync(input.blockedAncestor, 'PRIVATE_REGULAR_PARENT')
}
// Logging is a real Source consumer. Its attached sink is the sole substituted
// output primitive; public transport and native credentials remain denied.
delete process.env.CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC
let app = getDefaultAppState()
const context = { getAppState: () => app, setAppState: (update: any) => { app = update(app) }, abortController: new AbortController(), messages: [], options: { commands: [declaration], tools: [], agentDefinitions: { activeAgents: [] } } }
const result = await processSlashCommand('/keybindings unused arguments', [], [], [], context as any, () => { throw new Error('Keybindings local command mounted JSX') })
const bindings = await import('../../../src/keybindings/loadUserBindings')
const path = bindings.getKeybindingsPath()
const physical = resolve(path)
if (caps.blocked.length || caps.secureReads.some(value => value !== 'owned-empty-legacy')) throw new Error('Keybindings full Source escaped private fixture ' + JSON.stringify(caps))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ path, physical, work, content: fs.existsSync(path) ? fs.readFileSync(path, 'utf8') : null, editors, logs, result, resultTextPresent: Object.hasOwn(result, 'resultText'), caps }), error => error ? reject(error) : resolve()))
process.exit(0)
