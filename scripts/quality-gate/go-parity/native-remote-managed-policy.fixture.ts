import { mock } from 'bun:test'
import { existsSync, mkdirSync, readFileSync, statSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(process.env.HOME, 'Actual remote managed settings Source')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_REMOTE_MANAGED_FIXTURE !== '1') throw new Error('Missing remote managed fixture marker')
const proxy = new URL(process.env.HTTPS_PROXY!)
if (proxy.hostname !== '127.0.0.1' || proxy.protocol !== 'http:' || !proxy.port) throw new Error('Unowned settings network primitive')
const config = join(home, '.claude'), managed = join(home, 'managed'), workspace = join(home, 'workspace')
for (const directory of [config, managed, workspace]) mkdirSync(directory, { recursive: true })
writeFileSync(join(config, '.claude.json'), JSON.stringify(input.global ?? {}))
writeFileSync(join(config, 'settings.json'), JSON.stringify(input.user ?? {}))
writeFileSync(join(managed, 'managed-settings.json'), JSON.stringify(input.fallback ?? {}))
if (input.credentials) writeFileSync(join(config, '.credentials.json'), JSON.stringify(input.credentials))
if (input.cached !== undefined) writeFileSync(join(config, 'remote-settings.json'), JSON.stringify(input.cached), { mode: 0o600 })
Date.now = () => 1000000
process.env.CLAUDE_CODE_LOCAL_VERSION = '2.1.288'
process.env.CLAUDE_CODE_LOCAL_BUILD_TIME = 'fixture'
// Only this synchronous mutation observation denies all network primitives.
// Other existing fixture actions retain their original transport boundary.
const mutationCaps = input.action === 'policy-mutations'
  ? await (await import('./client-options-capabilities')).installClientFixtureCapabilities(home, { originalSecureStorage: true })
  : undefined
await import('../../../preload')
// The only substituted primitives are the same private Keychain/process and
// managed-file location boundaries used by existing real Source fixtures.
const portable = await import('../../../src/utils/execFileNoThrowPortable')
const files = await import('../../../src/utils/execFileNoThrow')
const execa = await import('execa')
mock.module('../../../src/utils/execFileNoThrowPortable', () => ({ ...portable, execSyncWithDefaults_DEPRECATED() { throw new Error('private unavailable Keychain') } }))
mock.module('../../../src/utils/execFileNoThrow', () => ({ ...files, async execFileNoThrow(file: string, ...args: any[]) {
  if (file === 'security' || file === 'defaults') return { code: 1, stdout: '', stderr: 'private unavailable OS store' }
  throw new Error('Unexpected subprocess in remote settings Source: ' + file)
} }))
mock.module('execa', () => ({ ...execa, execaSync() { return { exitCode: 1 } } }))
mock.module('../../../src/utils/settings/managedPath', () => ({ getManagedFilePath: () => managed, getManagedSettingsDropInDir: () => join(managed, 'managed-settings.d') }))
const state = await import('../../../src/bootstrap/state')
state.setOriginalCwd(workspace); state.setCwdState(workspace); state.setProjectRoot(workspace)
state.setIsInteractive(false)
const { enableConfigs } = await import('../../../src/utils/config')
enableConfigs()
const { applySafeConfigEnvironmentVariables } = await import('../../../src/utils/managedEnv')
applySafeConfigEnvironmentVariables()
if (input.action === 'policy-mutations') {
  const sync = await import('../../../src/services/remoteManagedSettings/syncCache')
  const leaf = await import('../../../src/services/remoteManagedSettings/syncCacheState')
  const settings = await import('../../../src/utils/settings/settings')
  const read = (base: string) => ({
    base,
    eligible: sync.isRemoteManagedSettingsEligible(),
    leaf: leaf.getRemoteManagedSettingsSyncFromCache(),
    policy: settings.getSettingsForSource('policySettings'),
  })
  const initial = read(process.env.ANTHROPIC_BASE_URL ?? '')
  const rows = input.bases.map((base: string) => {
    process.env.ANTHROPIC_BASE_URL = base
    return read(base)
  })
  // No resetSyncCache, settings reset or remote loader is called here.
  const result = { initial, rows, caps: mutationCaps }
  await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify(result) + '\n', error => error ? reject(error) : resolve()))
  process.exit(0)
}
if (input.action === 'primitives') {
  const utils = await import('../../../src/components/ManagedSettingsSecurityDialog/utils')
  const remote = await import('../../../src/services/remoteManagedSettings/index')
  const rows = input.rows.map((row: any) => {
    const before = utils.extractDangerousSettings(row.before)
    const after = utils.extractDangerousSettings(row.after)
    const snapshot = (value: typeof before) => JSON.stringify({ shellSettings: value.shellSettings, envVars: value.envVars, hooks: value.hooks })
    return { before: snapshot(before), after: snapshot(after), names: utils.formatDangerousSettingsList(after), changed: utils.hasDangerousSettingsChanged(row.before, row.after), checksum: remote.computeChecksumFromSettings(row.after) }
  })
  await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify(rows) + '\n', error => error ? reject(error) : resolve()))
  process.exit(0)
}
const settings = await import('../../../src/utils/settings/settings')
const remote = await import('../../../src/services/remoteManagedSettings/index')
const leaf = await import('../../../src/services/remoteManagedSettings/syncCacheState')
const { settingsChangeDetector } = await import('../../../src/utils/settings/changeDetector')
const changes: string[] = []
settingsChangeDetector.subscribe(source => { changes.push(source) })
const eligible = remote.isEligibleForRemoteManagedSettings()
const before = leaf.getRemoteManagedSettingsSyncFromCache()
if (input.action === 'clear') await remote.clearRemoteManagedSettingsCache()
else if (input.action === 'refresh') await remote.refreshRemoteManagedSettings()
else await remote.loadRemoteManagedSettings()
const cachePath = join(config, 'remote-settings.json')
const result = {
  eligible, before, after: leaf.getRemoteManagedSettingsSyncFromCache(),
  policy: settings.getSettingsForSource('policySettings'), changes,
  checksum: input.checksumInput ? remote.computeChecksumFromSettings(input.checksumInput) : null,
  disk: existsSync(cachePath) ? JSON.parse(readFileSync(cachePath, 'utf8')) : null,
  mode: existsSync(cachePath) ? statSync(cachePath).mode & 0o777 : null,
  ...(input.includeCredentials ? { credentials: JSON.parse(readFileSync(join(config, '.credentials.json'), 'utf8')) } : {}),
}
remote.stopBackgroundPolling()
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify(result) + '\n', error => error ? reject(error) : resolve()))
// This finite module fixture has captured the actual completed operation. The
// production initialization timeout is intentionally not rewritten/cancelled.
process.exit(0)
