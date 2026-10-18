import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual processSlashCommand local-JSX completion')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_LOCAL_JSX_SOURCE !== '1') throw new Error('Pinned local-JSX Source required')
const absentPaths = absentManagedSettingsFixturePaths()
for (const path of ['/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token', join(dirname(process.execPath), 'rg')]) if (!fs.existsSync(path)) absentPaths.push(path)
for (let parent = home; ; parent = dirname(parent)) { const candidate = join(parent, '.git'); if (!fs.existsSync(candidate)) absentPaths.push(candidate); if (dirname(parent) === parent) break }
const caps = await installClientFixtureCapabilities(home, { absentPaths })
;(globalThis as any).MACRO = { VERSION: '999.0.0-local-jsx-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const portable = await import('../../../src/utils/execFileNoThrow')
mock.module('../../../src/utils/execFileNoThrow', () => ({ ...portable, execSyncWithDefaults_DEPRECATED(command: string) { if (!command.startsWith('security find-generic-password ')) throw new Error('Unexpected OS primitive'); caps.secureReads.push('owned-empty-legacy'); return null } }))
const state = await import('../../../src/bootstrap/state')
state.setCwdState(home); state.setOriginalCwd(home); state.setProjectRoot(home); state.setAllowedSettingSources(['userSettings']); state.setIsInteractive(true)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const { getDefaultAppState } = await import('../../../src/state/AppState')
const { processSlashCommand } = await import('../../../src/utils/processUserInput/processSlashCommand')
const declarations = {
  mobile: (await import('../../../src/commands/mobile/index')).default,
  plan: (await import('../../../src/commands/plan/index')).default,
  branch: (await import('../../../src/commands/branch/index')).default,
  resume: (await import('../../../src/commands/resume/index')).default,
  diff: (await import('../../../src/commands/diff/index')).default,
}
const results = []
for (const row of input.rows) {
  const declaration = declarations[row.name as keyof typeof declarations]
  if (!declaration) throw new Error('Unowned local-JSX name')
  // Only the command-to-onDone boundary is a supplied completion. The entire
  // original processSlashCommand caller, factories and outer caveat policy run.
  const command = { ...declaration, load: async () => ({ call: (onDone: any) => { onDone(row.hasOutput ? row.output : undefined, { display: row.display || undefined, shouldQuery: row.shouldQuery }); return null } }) }
  let app = getDefaultAppState()
  const value = await processSlashCommand('/' + row.name + ' ' + row.args, [], [], [], { getAppState: () => app, setAppState: (update: any) => { app = update(app) }, abortController: new AbortController(), messages: [], options: { commands: [command], tools: [], agentDefinitions: { activeAgents: [] } } } as any, () => {})
  results.push(value)
}
if (caps.blocked.length) throw new Error('Local-JSX Source escaped private fixture ' + JSON.stringify(caps.blocked))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ results, caps: { blocked: caps.blocked, secureReads: caps.secureReads } }), error => error ? reject(error) : resolve()))
process.exit(0)
