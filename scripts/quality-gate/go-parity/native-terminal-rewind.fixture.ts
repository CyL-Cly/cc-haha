import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual rewind local command and slash caller')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_REWIND_SOURCE !== '1') throw new Error('Pinned rewind Source required')
const absentPaths = absentManagedSettingsFixturePaths()
for (const path of ['/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token', join(dirname(process.execPath), 'rg')]) if (!fs.existsSync(path)) absentPaths.push(path)
for (let parent = home; ; parent = dirname(parent)) {
  const candidate = join(parent, '.git')
  if (!fs.existsSync(candidate)) absentPaths.push(candidate)
  if (dirname(parent) === parent) break
}
const caps = await installClientFixtureCapabilities(home, { absentPaths })
;(globalThis as any).MACRO = { VERSION: '999.0.0-rewind-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const portable = await import('../../../src/utils/execFileNoThrow')
mock.module('../../../src/utils/execFileNoThrow', () => ({ ...portable, execSyncWithDefaults_DEPRECATED(command: string) {
  if (!command.startsWith('security find-generic-password ')) throw new Error('Unexpected OS primitive')
  caps.secureReads.push('owned-empty-legacy')
  return null
} }))
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(home)
bootstrap.setOriginalCwd(home)
bootstrap.setProjectRoot(home)
bootstrap.setAllowedSettingSources(['userSettings'])
bootstrap.setIsInteractive(true)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const { getDefaultAppState } = await import('../../../src/state/AppState')
const { processSlashCommand } = await import('../../../src/utils/processUserInput/processSlashCommand')
const declaration = (await import('../../../src/commands/rewind/index')).default
const rows = []
for (const row of input.rows) {
  let app = getDefaultAppState()
  let opened = 0
  const context = {
    getAppState: () => app, setAppState: (update: any) => { app = update(app) },
    abortController: new AbortController(), messages: [],
    options: { commands: [declaration], tools: [], agentDefinitions: { activeAgents: [] } },
    ...(row.withContext ? { openMessageSelector: () => { opened++ } } : {}),
  }
  const result = await processSlashCommand(row.input, [], [], [], context as any, () => { throw new Error('Rewind mounted local JSX') })
  rows.push({ opened, result })
}
if (caps.blocked.length || caps.secureReads.some(value => value !== 'owned-empty-legacy')) throw new Error('Rewind Source escaped private fixture ' + JSON.stringify(caps))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ declaration, rows, caps }), error => error ? reject(error) : resolve()))
process.exit(0)
