import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { mock } from 'bun:test'
import { feature } from 'bun:bundle'

const input = await new Response(Bun.stdin.stream()).json()
if (input.arguments) process.argv.push(...input.arguments)
const home = assertDisposableFixtureHome(input.home, 'Actual conversation export renderer')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_TERMINAL_EXPORT_FIXTURE !== '1') throw new Error('Original renderer guard required')
fs.mkdirSync(join(input.cwd ?? home, '.git'), { recursive: true })
fs.mkdirSync(join(home, 'bin'), { recursive: true })
process.env.PATH = join(home, 'bin')
const packagedRg = join(dirname(process.execPath), process.platform === 'win32' ? 'rg.exe' : 'rg')
const absentPaths = absentManagedSettingsFixturePaths()
if (!fs.existsSync(packagedRg)) absentPaths.push(packagedRg)
for (const path of ['/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token', '/Library/Application Support/ClaudeCode/CLAUDE.md', '/Library/Application Support/ClaudeCode/.claude/rules']) {
  if (!fs.existsSync(path)) absentPaths.push(path)
}
if (!input.cachedSubscriber) process.env.ANTHROPIC_API_KEY = 'fake-export-renderer-only'
process.env.ANTHROPIC_BASE_URL = 'http://127.0.0.1:9'
const caps = await installClientFixtureCapabilities(home, { absentPaths })
const layoutTrace: any[] = []
if (input.captureLayout) {
  const dom = await import('../../../src/ink/dom')
  const createNodeOriginal = dom.createNode
  mock.module('../../../src/ink/dom', () => ({ ...dom, createNode(name: string) {
    const node = createNodeOriginal(name as any)
    if (name === 'ink-root') {
      let compute: any
      Object.defineProperty(node, 'onComputeLayout', { configurable: true, get: () => compute, set(value) {
        compute = () => {
          value()
          const rows: any[] = []
          const walk = (entry: any, depth = 0) => {
            rows.push({ depth, name: entry.nodeName, text: entry.textContent, layout: entry.yogaNode ? { top: entry.yogaNode.getComputedTop(), left: entry.yogaNode.getComputedLeft(), width: entry.yogaNode.getComputedWidth(), height: entry.yogaNode.getComputedHeight() } : undefined, style: entry.style })
            for (const child of entry.childNodes ?? []) walk(child, depth + 1)
          }
          walk(node)
          if (!layoutTrace.length) layoutTrace.push(...rows)
        }
      } })
    }
    return node
  } }))
}
// Only the native legacy keychain read is replaced. The actual auth selection,
// cache and notice business predicates still consume the private global file.
const portable = await import('../../../src/utils/execFileNoThrow')
mock.module('../../../src/utils/execFileNoThrow', () => ({ ...portable, execSyncWithDefaults_DEPRECATED(command: string) {
  if (!command.startsWith('security find-generic-password ')) throw new Error('Unexpected native legacy read')
  caps.secureReads.push('legacy-read')
  return input.legacyAPIKey ?? null
} }))
;(globalThis as any).MACRO = { VERSION: '999.0.0-export-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
if (input.cachedSubscriber) {
  const secure = await import('../../../src/utils/secureStorage/index')
  mock.module('../../../src/utils/secureStorage/index', () => ({ ...secure, getSecureStorage: () => ({ read: () => {
    caps.secureReads.push('private-export-credentials')
    try { return JSON.parse(fs.readFileSync(join(home, '.claude', '.credentials.json'), 'utf8')) } catch { return null }
  } }) }))
}
const state = await import('../../../src/bootstrap/state')
state.setCwdState(home)
state.setOriginalCwd(input.cwd ?? home)
state.setProjectRoot(input.cwd ?? home)
state.setAllowedSettingSources(['userSettings'])
state.setIsInteractive(false)
if (input.sid) state.switchSession(input.sid)
if (input.now) {
  const OriginalDate = globalThis.Date
  const fixed = new OriginalDate(input.now).getTime()
  globalThis.Date = class extends OriginalDate { constructor(...args: any[]) { super(...(args.length ? args : [fixed]) as [any]) }; static now() { return fixed } } as DateConstructor
}
if (input.model) state.setMainLoopModelOverride(input.model)
if (input.cwd) state.setCwdState(input.cwd)
process.env.CLAUDE_CODE_LOCAL_VERSION = input.version ?? '999.0.0-export-fixture'
MACRO.VERSION = process.env.CLAUDE_CODE_LOCAL_VERSION
const { enableConfigs, getGlobalConfig } = await import('../../../src/utils/config')
enableConfigs()
if (input.preload) {
  const { checkForReleaseNotes } = await import('../../../src/utils/releaseNotes')
  const result = await checkForReleaseNotes(getGlobalConfig().lastReleaseNotesSeen)
  if (result.hasReleaseNotes) {
    const { getRecentActivity } = await import('../../../src/utils/logoV2Utils')
    await getRecentActivity()
  }
}
if (input.changedChangelog !== undefined) fs.writeFileSync(join(home, '.claude', 'cache', 'changelog.md'), input.changedChangelog)
const { renderMessagesToPlainText } = await import('../../../src/utils/exportRenderer')
const { SandboxManager } = await import('../../../src/utils/sandbox/sandbox-adapter')
const { getLogoDisplayData } = await import('../../../src/utils/logoV2Utils')
const { getDefaultMainLoopModelSetting, parseUserSpecifiedModel, renderModelSetting } = await import('../../../src/utils/model/model')
// The actual export's new AppState has null mainLoopModel/ForSession. This is
// exactly useMainLoopModel's branch in the mounted static export tree.
const presentation = { ...getLogoDisplayData(), model: renderModelSetting(parseUserSpecifiedModel(getDefaultMainLoopModelSetting())) }
const results = []
for (const row of input.rows) {
  results.push(await renderMessagesToPlainText(row.messages, [], row.columns))
}
if (caps.blocked.length) throw new Error('Original export renderer escaped fixture: ' + JSON.stringify(caps.blocked))
const output = Buffer.from(JSON.stringify({ results, presentation, layoutTrace, sandboxed: SandboxManager.isSandboxingEnabled(), globalConfig: getGlobalConfig(), featureGates: { voice: feature('VOICE_MODE') ? true : false, channels: feature('KAIROS') || feature('KAIROS_CHANNELS') ? true : false }, caps: { blocked: caps.blocked, secureReads: caps.secureReads } }))
for (let offset = 0; offset < output.length;) offset += fs.writeSync(1, output, offset, output.length - offset)
process.exit(0)
